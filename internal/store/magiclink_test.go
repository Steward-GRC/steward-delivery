// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/store"
)

func TestMagicLinkCreateAndResolve(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewMagicLinkRepo(pool)
	ctx := context.Background()

	ml := store.MagicLink{
		Token:           "tok-abc123",
		PolicyVersionID: "pv-001",
		CreatedByUserID: "user-001",
		Sensitive:       false,
		ExpiresAt:       time.Now().Add(30 * 24 * time.Hour).UTC().Truncate(time.Second),
	}
	if err := repo.Create(ctx, ml); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Resolve(ctx, "tok-abc123")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.PolicyVersionID != ml.PolicyVersionID {
		t.Errorf("got PolicyVersionID=%q, want %q", got.PolicyVersionID, ml.PolicyVersionID)
	}
	if got.CreatedByUserID != ml.CreatedByUserID {
		t.Errorf("got CreatedByUserID=%q, want %q", got.CreatedByUserID, ml.CreatedByUserID)
	}
	if got.Sensitive != ml.Sensitive {
		t.Errorf("got Sensitive=%v, want %v", got.Sensitive, ml.Sensitive)
	}
	if got.Revoked() {
		t.Error("token should not be revoked")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set by the database default")
	}
}

func TestMagicLinkResolveExpiredReturnsError(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewMagicLinkRepo(pool)
	ctx := context.Background()

	ml := store.MagicLink{
		Token:           "tok-expired",
		PolicyVersionID: "pv-002",
		CreatedByUserID: "user-001",
		Sensitive:       false,
		ExpiresAt:       time.Now().Add(-1 * time.Hour).UTC(),
	}
	if err := repo.Create(ctx, ml); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err := repo.Resolve(ctx, "tok-expired")
	if err == nil {
		t.Fatal("expected error resolving expired token")
	}
	if !errors.Is(err, store.ErrTokenExpired) {
		t.Errorf("got error %v, want ErrTokenExpired", err)
	}
}

func TestMagicLinkRevoke(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewMagicLinkRepo(pool)
	ctx := context.Background()

	ml := store.MagicLink{
		Token:           "tok-revoke",
		PolicyVersionID: "pv-003",
		CreatedByUserID: "user-001",
		Sensitive:       true,
		ExpiresAt:       time.Now().Add(48 * time.Hour).UTC(),
	}
	if err := repo.Create(ctx, ml); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Revoke(ctx, "tok-revoke", "admin-001"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	_, err := repo.Resolve(ctx, "tok-revoke")
	if err == nil {
		t.Fatal("expected error resolving revoked token")
	}
	if !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("got error %v, want ErrTokenRevoked", err)
	}
}

func TestMagicLinkRevokeIsIdempotent(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewMagicLinkRepo(pool)
	ctx := context.Background()

	ml := store.MagicLink{
		Token:           "tok-revoke-twice",
		PolicyVersionID: "pv-004",
		CreatedByUserID: "user-001",
		Sensitive:       false,
		ExpiresAt:       time.Now().Add(24 * time.Hour).UTC(),
	}
	if err := repo.Create(ctx, ml); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Revoke(ctx, "tok-revoke-twice", "admin-001"); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}
	if err := repo.Revoke(ctx, "tok-revoke-twice", "admin-002"); err != nil {
		t.Fatalf("second Revoke (should be no-op): %v", err)
	}
}

func TestMagicLinkResolveUnknownTokenReturnsNotFound(t *testing.T) {
	pool := newTestDB(t)
	repo := store.NewMagicLinkRepo(pool)
	ctx := context.Background()

	_, err := repo.Resolve(ctx, "no-such-token")
	if err == nil {
		t.Fatal("expected error resolving unknown token")
	}
	if !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("got error %v, want ErrTokenNotFound", err)
	}
}
