// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package store provides Postgres-backed repositories for the delivery
// service's domain aggregates (magic-link tokens, PDF jobs).
package store

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	postgres "github.com/Bugs5382/go-postgres"
	"github.com/jackc/pgx/v5"
)

// Sentinel errors from MagicLinkRepo.Resolve; branch on them with errors.Is.
var (
	ErrTokenNotFound = errors.New("magic link token not found")
	ErrTokenExpired  = errors.New("magic link token has expired")
	ErrTokenRevoked  = errors.New("magic link token has been revoked")
	// ErrTokenActive is Remove's refusal of a link that still works.
	ErrTokenActive = errors.New("magic link token is still active")
	// ErrInvalidPageToken is List's refusal of a page token it did not issue.
	ErrInvalidPageToken = errors.New("invalid page token")
)

// The statuses a link reports.
const (
	MagicLinkActive  = "active"
	MagicLinkRevoked = "revoked"
	MagicLinkExpired = "expired"
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

// Status is "revoked" once revoked (even past its expiry), else "expired"
// past its expiry, else "active".
func (m MagicLink) Status(now time.Time) string {
	switch {
	case m.Revoked():
		return MagicLinkRevoked
	case !now.Before(m.ExpiresAt):
		return MagicLinkExpired
	default:
		return MagicLinkActive
	}
}

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

// MagicLinkFilter narrows List.
type MagicLinkFilter struct {
	// PolicyVersionIDs limits the list to these versions; empty means all.
	PolicyVersionIDs []string
	// PageSize is 1 to 200; anything else means 50.
	PageSize  int
	PageToken string
}

const defaultLinkPage, maxLinkPage = 50, 200

// List returns links newest first, and the token of the next page when there
// may be one.
func (r *MagicLinkRepo) List(ctx context.Context, f MagicLinkFilter) ([]MagicLink, string, error) {
	limit := f.PageSize
	if limit < 1 || limit > maxLinkPage {
		limit = defaultLinkPage
	}
	var (
		afterAt    *time.Time
		afterToken string
	)
	if f.PageToken != "" {
		at, tok, err := decodeLinkCursor(f.PageToken)
		if err != nil {
			return nil, "", err
		}
		afterAt, afterToken = &at, tok
	}
	versions := f.PolicyVersionIDs
	if versions == nil {
		versions = []string{}
	}
	rows, err := r.db.Querier().Query(ctx, `
		SELECT token, policy_version_id, created_by_user_id, sensitive,
		       expires_at, revoked_at, revoked_by_user_id, created_at
		  FROM magic_link_tokens
		 WHERE (cardinality($1::text[]) = 0 OR policy_version_id = ANY($1))
		   AND ($2::timestamptz IS NULL OR (created_at, token) < ($2, $3))
		 ORDER BY created_at DESC, token DESC
		 LIMIT $4`, versions, afterAt, afterToken, limit)
	if err != nil {
		return nil, "", fmt.Errorf("MagicLinkRepo.List: %w", err)
	}
	defer rows.Close()
	var out []MagicLink
	for rows.Next() {
		var ml MagicLink
		if err := rows.Scan(&ml.Token, &ml.PolicyVersionID, &ml.CreatedByUserID, &ml.Sensitive,
			&ml.ExpiresAt, &ml.RevokedAt, &ml.RevokedByUserID, &ml.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("MagicLinkRepo.List: scan: %w", err)
		}
		out = append(out, ml)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("MagicLinkRepo.List: %w", err)
	}
	next := ""
	if len(out) == limit {
		last := out[len(out)-1]
		next = encodeLinkCursor(last.CreatedAt, last.Token)
	}
	return out, next, nil
}

func encodeLinkCursor(at time.Time, token string) string {
	raw := strconv.FormatInt(at.UnixMicro(), 10) + "|" + token
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeLinkCursor(s string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", ErrInvalidPageToken
	}
	micro, token, ok := strings.Cut(string(raw), "|")
	if !ok || token == "" {
		return time.Time{}, "", ErrInvalidPageToken
	}
	n, err := strconv.ParseInt(micro, 10, 64)
	if err != nil {
		return time.Time{}, "", ErrInvalidPageToken
	}
	return time.UnixMicro(n).UTC(), token, nil
}

// Remove deletes a revoked or expired link. A link that still works is
// ErrTokenActive and stays; an unknown token is ErrTokenNotFound.
func (r *MagicLinkRepo) Remove(ctx context.Context, token string) error {
	tag, err := r.db.Querier().Exec(ctx, `
		DELETE FROM magic_link_tokens
		 WHERE token = $1
		   AND (revoked_at IS NOT NULL OR expires_at <= now())`, token)
	if err != nil {
		return fmt.Errorf("MagicLinkRepo.Remove: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := r.db.Querier().QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM magic_link_tokens WHERE token = $1)`, token).Scan(&exists); err != nil {
		return fmt.Errorf("MagicLinkRepo.Remove: %w", err)
	}
	if exists {
		return ErrTokenActive
	}
	return ErrTokenNotFound
}
