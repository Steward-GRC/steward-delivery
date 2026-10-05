// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Steward-GRC/steward-delivery/internal/store"
)

func TestPDFJobCreateAndGet(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	if err := repo.Create(ctx, "job-001", "pv-001", "user-001"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "job-001")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.JobID != "job-001" {
		t.Errorf("got JobID=%q, want %q", got.JobID, "job-001")
	}
	if got.PolicyVersionID != "pv-001" {
		t.Errorf("got PolicyVersionID=%q, want %q", got.PolicyVersionID, "pv-001")
	}
	if got.RequesterUserID != "user-001" {
		t.Errorf("got RequesterUserID=%q, want %q", got.RequesterUserID, "user-001")
	}
	if got.Status != store.PDFJobStatusPending {
		t.Errorf("got Status=%q, want %q", got.Status, store.PDFJobStatusPending)
	}
	if got.ArtifactKey != nil {
		t.Errorf("got ArtifactKey=%v, want nil", got.ArtifactKey)
	}
	if got.CompletedAt != nil {
		t.Errorf("got CompletedAt=%v, want nil", got.CompletedAt)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set by the database default")
	}
}

func TestPDFJobMarkDoneSetsArtifactKey(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	if err := repo.Create(ctx, "job-002", "pv-002", "user-001"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkDone(ctx, "job-002", "artifacts/pv-002/job-002.pdf"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	got, err := repo.Get(ctx, "job-002")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != store.PDFJobStatusDone {
		t.Errorf("got Status=%q, want %q", got.Status, store.PDFJobStatusDone)
	}
	if got.ArtifactKey == nil || *got.ArtifactKey != "artifacts/pv-002/job-002.pdf" {
		t.Errorf("got ArtifactKey=%v, want artifacts/pv-002/job-002.pdf", got.ArtifactKey)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt should be set after MarkDone")
	}
}

func TestPDFJobGetArtifactKeyReturnsKeyWhenDone(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	if err := repo.Create(ctx, "job-003", "pv-003", "user-001"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkDone(ctx, "job-003", "artifacts/pv-003/job-003.pdf"); err != nil {
		t.Fatalf("MarkDone: %v", err)
	}

	key, err := repo.GetArtifactKey(ctx, "job-003")
	if err != nil {
		t.Fatalf("GetArtifactKey: %v", err)
	}
	if key != "artifacts/pv-003/job-003.pdf" {
		t.Errorf("got key=%q, want artifacts/pv-003/job-003.pdf", key)
	}
}

func TestPDFJobGetArtifactKeyPendingReturnsNotReady(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	if err := repo.Create(ctx, "job-pending", "pv-001", "user-001"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := repo.GetArtifactKey(ctx, "job-pending")
	if err == nil {
		t.Fatal("expected error for pending job")
	}
	if !errors.Is(err, store.ErrPDFJobNotReady) {
		t.Errorf("got error %v, want ErrPDFJobNotReady", err)
	}
}

func TestPDFJobGetArtifactKeyUnknownReturnsNotFound(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	_, err := repo.GetArtifactKey(ctx, "no-such-job")
	if err == nil {
		t.Fatal("expected error for unknown job")
	}
	if !errors.Is(err, store.ErrPDFJobNotFound) {
		t.Errorf("got error %v, want ErrPDFJobNotFound", err)
	}
}

func TestPDFJobMarkFailedRecordsErrorMessage(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	if err := repo.Create(ctx, "job-failed", "pv-004", "user-001"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkFailed(ctx, "job-failed", "chromedp: oom"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	got, err := repo.Get(ctx, "job-failed")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != store.PDFJobStatusFailed {
		t.Errorf("got Status=%q, want %q", got.Status, store.PDFJobStatusFailed)
	}
	if got.ErrorMsg == nil || *got.ErrorMsg != "chromedp: oom" {
		t.Errorf("got ErrorMsg=%v, want \"chromedp: oom\"", got.ErrorMsg)
	}
	if got.ArtifactKey != nil {
		t.Errorf("got ArtifactKey=%v, want nil on failed job", got.ArtifactKey)
	}

	// A failed job must not satisfy GetArtifactKey.
	_, err = repo.GetArtifactKey(ctx, "job-failed")
	if !errors.Is(err, store.ErrPDFJobNotReady) {
		t.Errorf("GetArtifactKey on failed job: got %v, want ErrPDFJobNotReady", err)
	}
}

func TestPDFJobMarkDoneOnMissingReturnsNotFound(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	err := repo.MarkDone(ctx, "no-such-job", "artifacts/x.pdf")
	if err == nil {
		t.Fatal("expected error MarkDone on missing job")
	}
	if !errors.Is(err, store.ErrPDFJobNotFound) {
		t.Errorf("got error %v, want ErrPDFJobNotFound", err)
	}
}

func TestPDFJobGetMissingReturnsNotFound(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	_, err := repo.Get(ctx, "no-such-job")
	if err == nil {
		t.Fatal("expected error for missing job")
	}
	if !errors.Is(err, store.ErrPDFJobNotFound) {
		t.Errorf("got error %v, want ErrPDFJobNotFound", err)
	}
}

func TestPDFJobGetArtifactKeyTellsFailedFromPending(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewPDFJobRepo(pool)
	ctx := context.Background()

	if err := repo.Create(ctx, "job-pending-2", "pv-005", "user-001"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Create(ctx, "job-failed-2", "pv-005", "user-001"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.MarkFailed(ctx, "job-failed-2", "renderer timed out"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	_, err := repo.GetArtifactKey(ctx, "job-pending-2")
	if errors.Is(err, store.ErrPDFJobFailed) {
		t.Errorf("pending job reported as failed: %v", err)
	}
	_, err = repo.GetArtifactKey(ctx, "job-failed-2")
	if !errors.Is(err, store.ErrPDFJobFailed) {
		t.Errorf("failed job: got %v, want ErrPDFJobFailed", err)
	}
}
