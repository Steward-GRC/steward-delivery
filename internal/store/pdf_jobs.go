// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	postgres "github.com/Bugs5382/go-postgres"
	"github.com/jackc/pgx/v5"
)

// The statuses the store writes to pdf_jobs.status.
const (
	PDFJobStatusPending    = "pending"
	PDFJobStatusProcessing = "processing"
	PDFJobStatusDone       = "done"
	PDFJobStatusFailed     = "failed"
)

// Sentinel errors from PDFJobRepo. A failed job's error matches both
// ErrPDFJobFailed and ErrPDFJobNotReady.
var (
	ErrPDFJobNotFound = errors.New("pdf job not found")
	ErrPDFJobNotReady = errors.New("pdf job artifact not ready")
	ErrPDFJobFailed   = errors.New("pdf job failed")
)

// PDFJob is one row of pdf_jobs. The pointer fields are nil until the job
// finishes.
type PDFJob struct {
	JobID           string
	PolicyVersionID string
	RequesterUserID string
	Sensitive       bool
	Status          string
	ArtifactKey     *string
	ErrorMsg        *string
	CreatedAt       time.Time
	CompletedAt     *time.Time
}

// PDFJobRepo persists pdf_jobs rows. The export handler creates rows and reads
// the artifact key; the PdfRender informer moves them through their states.
type PDFJobRepo struct{ db *postgres.DB }

// NewPDFJobRepo returns a PDFJobRepo on db.
func NewPDFJobRepo(db *postgres.DB) *PDFJobRepo {
	return &PDFJobRepo{db: db}
}

// Create inserts a job as pending. The sensitive column keeps its default:
// the sensitivity travels on the PdfRender resource.
func (r *PDFJobRepo) Create(ctx context.Context, jobID, policyVersionID, requesterUserID string) error {
	_, err := r.db.Querier().Exec(ctx, `
		INSERT INTO pdf_jobs
		  (job_id, policy_version_id, requester_user_id, status)
		VALUES ($1, $2, $3, $4)`,
		jobID, policyVersionID, requesterUserID, PDFJobStatusPending,
	)
	if err != nil {
		return fmt.Errorf("PDFJobRepo.Create: %w", err)
	}
	return nil
}

// MarkProcessing moves a pending job to processing when the renderer reports
// Running.
func (r *PDFJobRepo) MarkProcessing(ctx context.Context, jobID string) error {
	// No row changed is fine: the informer's resync replays Running for rows
	// that have already moved on.
	_, err := r.db.Querier().Exec(ctx, `
		UPDATE pdf_jobs
		   SET status = $1
		 WHERE job_id = $2
		   AND status = $3`,
		PDFJobStatusProcessing, jobID, PDFJobStatusPending,
	)
	if err != nil {
		return fmt.Errorf("PDFJobRepo.MarkProcessing: %w", err)
	}
	return nil
}

// MarkDone records a finished render's artifact key. Repeating it overwrites
// the key and bumps completed_at, as the renderer overwrites by key.
func (r *PDFJobRepo) MarkDone(ctx context.Context, jobID, artifactKey string) error {
	tag, err := r.db.Querier().Exec(ctx, `
		UPDATE pdf_jobs
		   SET status       = $1,
		       artifact_key = $2,
		       completed_at = now(),
		       error_msg    = NULL
		 WHERE job_id = $3`,
		PDFJobStatusDone, artifactKey, jobID,
	)
	if err != nil {
		return fmt.Errorf("PDFJobRepo.MarkDone: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrPDFJobNotFound, jobID)
	}
	return nil
}

// MarkFailed records a failed render with the renderer's message; the
// artifact key stays NULL.
func (r *PDFJobRepo) MarkFailed(ctx context.Context, jobID, errMsg string) error {
	tag, err := r.db.Querier().Exec(ctx, `
		UPDATE pdf_jobs
		   SET status       = $1,
		       error_msg    = $2,
		       completed_at = now()
		 WHERE job_id = $3`,
		PDFJobStatusFailed, errMsg, jobID,
	)
	if err != nil {
		return fmt.Errorf("PDFJobRepo.MarkFailed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s", ErrPDFJobNotFound, jobID)
	}
	return nil
}

// Get returns the job, or ErrPDFJobNotFound.
func (r *PDFJobRepo) Get(ctx context.Context, jobID string) (PDFJob, error) {
	var j PDFJob
	err := r.db.Querier().QueryRow(ctx, `
		SELECT job_id, policy_version_id, requester_user_id, sensitive,
		       status, artifact_key, error_msg, created_at, completed_at
		  FROM pdf_jobs WHERE job_id = $1`, jobID).
		Scan(&j.JobID, &j.PolicyVersionID, &j.RequesterUserID, &j.Sensitive,
			&j.Status, &j.ArtifactKey, &j.ErrorMsg, &j.CreatedAt, &j.CompletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PDFJob{}, fmt.Errorf("%w: %s", ErrPDFJobNotFound, jobID)
		}
		return PDFJob{}, fmt.Errorf("PDFJobRepo.Get: %w", err)
	}
	return j, nil
}

// GetArtifactKey returns a done job's object key. A pending or processing job
// returns ErrPDFJobNotReady, a failed one ErrPDFJobFailed, an unknown one
// ErrPDFJobNotFound.
func (r *PDFJobRepo) GetArtifactKey(ctx context.Context, jobID string) (string, error) {
	var (
		status string
		key    *string
	)
	err := r.db.Querier().QueryRow(ctx, `
		SELECT status, artifact_key FROM pdf_jobs WHERE job_id = $1`, jobID).
		Scan(&status, &key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: %s", ErrPDFJobNotFound, jobID)
		}
		return "", fmt.Errorf("PDFJobRepo.GetArtifactKey: %w", err)
	}
	if status == PDFJobStatusFailed {
		return "", fmt.Errorf("%w (%w): job_id=%s", ErrPDFJobFailed, ErrPDFJobNotReady, jobID)
	}
	if status != PDFJobStatusDone || key == nil {
		return "", fmt.Errorf("%w: job_id=%s status=%s", ErrPDFJobNotReady, jobID, status)
	}
	return *key, nil
}
