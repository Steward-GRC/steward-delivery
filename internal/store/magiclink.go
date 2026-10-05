// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package store provides Postgres-backed repositories for the delivery
// service's domain aggregates (magic-link tokens, PDF jobs).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	postgres "github.com/Bugs5382/go-postgres"
	"github.com/jackc/pgx/v5"
)

// Sentinel errors from MagicLinkRepo.Resolve; branch on them with errors.Is.
var (
	ErrTokenNotFound = errors.New("magic link token not found")
	ErrTokenExpired  = errors.New("magic link token has expired")
	ErrTokenRevoked  = errors.New("magic link token has been revoked")
)

// MagicLink is one row of magic_link_tokens. The pointer fields are nil until
// the link is revoked.
type MagicLink struct {
	Token           string
	PolicyVersionID string
	CreatedByUserID string
	Sensitive       bool
	ExpiresAt       time.Time
	RevokedAt       *time.Time
	RevokedByUserID *string
	CreatedAt       time.Time
}

// Revoked reports whether the token has been explicitly revoked.
func (m MagicLink) Revoked() bool { return m.RevokedAt != nil }

// MagicLinkRepo persists magic-link tokens in Postgres. It is safe for
// concurrent use.
type MagicLinkRepo struct{ db *postgres.DB }

// NewMagicLinkRepo returns a MagicLinkRepo on db.
func NewMagicLinkRepo(db *postgres.DB) *MagicLinkRepo {
	return &MagicLinkRepo{db: db}
}

// Create inserts a magic link. The token is the primary key, so it must be
// unique (the service uses 256 random bits).
func (r *MagicLinkRepo) Create(ctx context.Context, ml MagicLink) error {
	_, err := r.db.Querier().Exec(ctx, `
		INSERT INTO magic_link_tokens
		  (token, policy_version_id, created_by_user_id, sensitive, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		ml.Token, ml.PolicyVersionID, ml.CreatedByUserID, ml.Sensitive, ml.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("MagicLinkRepo.Create: %w", err)
	}
	return nil
}

// Resolve returns the link for token if it is neither revoked nor expired,
// with a distinct sentinel error for each refusal.
func (r *MagicLinkRepo) Resolve(ctx context.Context, token string) (MagicLink, error) {
	var ml MagicLink
	err := r.db.Querier().QueryRow(ctx, `
		SELECT token, policy_version_id, created_by_user_id, sensitive,
		       expires_at, revoked_at, revoked_by_user_id, created_at
		  FROM magic_link_tokens WHERE token = $1`, token).
		Scan(&ml.Token, &ml.PolicyVersionID, &ml.CreatedByUserID, &ml.Sensitive,
			&ml.ExpiresAt, &ml.RevokedAt, &ml.RevokedByUserID, &ml.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MagicLink{}, fmt.Errorf("%w: %s", ErrTokenNotFound, token)
		}
		return MagicLink{}, fmt.Errorf("MagicLinkRepo.Resolve: %w", err)
	}
	if ml.Revoked() {
		return MagicLink{}, ErrTokenRevoked
	}
	if time.Now().After(ml.ExpiresAt) {
		return MagicLink{}, ErrTokenExpired
	}
	return ml, nil
}

// Revoke marks a link revoked. Revoking a revoked or unknown token succeeds,
// so a retry is safe.
func (r *MagicLinkRepo) Revoke(ctx context.Context, token, revokedByUserID string) error {
	now := time.Now().UTC()
	_, err := r.db.Querier().Exec(ctx, `
		UPDATE magic_link_tokens
		   SET revoked_at = $1,
		       revoked_by_user_id = $2
		 WHERE token = $3
		   AND revoked_at IS NULL`,
		now, revokedByUserID, token,
	)
	if err != nil {
		return fmt.Errorf("MagicLinkRepo.Revoke: %w", err)
	}
	return nil
}
