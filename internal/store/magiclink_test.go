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

func seedLink(t *testing.T, repo *store.MagicLinkRepo, token, version string, expires time.Time) {
	t.Helper()
	if err := repo.Create(context.Background(), store.MagicLink{
		Token: token, PolicyVersionID: version, CreatedByUserID: "user-001", ExpiresAt: expires,
	}); err != nil {
		t.Fatalf("Create %s: %v", token, err)
	}
}

func TestMagicLinkListNewestFirstFilteredAndPaged(t *testing.T) {
	repo := store.NewMagicLinkRepo(newTestDB(t))
	ctx := context.Background()
	soon := time.Now().Add(time.Hour)
	seedLink(t, repo, "t1", "pv-a", soon)
	seedLink(t, repo, "t2", "pv-b", soon)
	seedLink(t, repo, "t3", "pv-a", soon)

	all, next, err := repo.List(ctx, store.MagicLinkFilter{})
	if err != nil || next != "" {
		t.Fatalf("List: %v next=%q", err, next)
	}
	if got := tokens(all); got != "t3,t2,t1" {
		t.Errorf("order %s, want t3,t2,t1", got)
	}

	only, _, err := repo.List(ctx, store.MagicLinkFilter{PolicyVersionIDs: []string{"pv-a"}})
	if err != nil || tokens(only) != "t3,t1" {
		t.Errorf("filtered %s %v", tokens(only), err)
	}

	first, next, err := repo.List(ctx, store.MagicLinkFilter{PageSize: 2})
	if err != nil || tokens(first) != "t3,t2" || next == "" {
		t.Fatalf("page 1 %s next=%q %v", tokens(first), next, err)
	}
	second, next, err := repo.List(ctx, store.MagicLinkFilter{PageSize: 2, PageToken: next})
	if err != nil || tokens(second) != "t1" || next != "" {
		t.Errorf("page 2 %s next=%q %v", tokens(second), next, err)
	}
	if _, _, err := repo.List(ctx, store.MagicLinkFilter{PageToken: "not-a-token"}); !errors.Is(err, store.ErrInvalidPageToken) {
		t.Errorf("bad token: %v", err)
	}
}

func tokens(links []store.MagicLink) string {
	out := ""
	for i, l := range links {
		if i > 0 {
			out += ","
		}
		out += l.Token
	}
	return out
}

func TestMagicLinkStatus(t *testing.T) {
	now := time.Now()
	revoked := now.Add(-time.Minute)
	for _, tc := range []struct {
		name string
		ml   store.MagicLink
		want string
	}{
		{"active", store.MagicLink{ExpiresAt: now.Add(time.Hour)}, store.MagicLinkActive},
		{"expired", store.MagicLink{ExpiresAt: now.Add(-time.Hour)}, store.MagicLinkExpired},
		{"revoked", store.MagicLink{ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked}, store.MagicLinkRevoked},
		{"revoked beats expired", store.MagicLink{ExpiresAt: now.Add(-time.Hour), RevokedAt: &revoked}, store.MagicLinkRevoked},
	} {
		if got := tc.ml.Status(now); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestMagicLinkRemoveOnlyRevokedOrExpired(t *testing.T) {
	repo := store.NewMagicLinkRepo(newTestDB(t))
	ctx := context.Background()
	seedLink(t, repo, "live", "pv-a", time.Now().Add(time.Hour))
	seedLink(t, repo, "revoked", "pv-a", time.Now().Add(time.Hour))
	seedLink(t, repo, "expired", "pv-a", time.Now().Add(-time.Hour))
	if err := repo.Revoke(ctx, "revoked", "admin"); err != nil {
		t.Fatal(err)
	}

	if err := repo.Remove(ctx, "live"); !errors.Is(err, store.ErrTokenActive) {
		t.Errorf("remove live: %v", err)
	}
	if err := repo.Remove(ctx, "nope"); !errors.Is(err, store.ErrTokenNotFound) {
		t.Errorf("remove unknown: %v", err)
	}
	for _, tok := range []string{"revoked", "expired"} {
		if err := repo.Remove(ctx, tok); err != nil {
			t.Errorf("remove %s: %v", tok, err)
		}
		if _, err := repo.Resolve(ctx, tok); !errors.Is(err, store.ErrTokenNotFound) {
			t.Errorf("resolve removed %s: %v", tok, err)
		}
	}
	left, _, _ := repo.List(ctx, store.MagicLinkFilter{})
	if tokens(left) != "live" {
		t.Errorf("left %s, want live", tokens(left))
	}
}
