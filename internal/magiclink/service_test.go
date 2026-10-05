// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package magiclink_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/store"
)

// fakeRepo implements magiclink.Repo for unit tests — no Postgres required.
type fakeRepo struct {
	tokens map[string]store.MagicLink
}

func newFakeRepo() *fakeRepo { return &fakeRepo{tokens: map[string]store.MagicLink{}} }

func (f *fakeRepo) Create(ctx context.Context, ml store.MagicLink) error {
	f.tokens[ml.Token] = ml
	return nil
}

func (f *fakeRepo) Resolve(ctx context.Context, token string) (store.MagicLink, error) {
	ml, ok := f.tokens[token]
	if !ok {
		return store.MagicLink{}, store.ErrTokenNotFound
	}
	if ml.Revoked() {
		return store.MagicLink{}, store.ErrTokenRevoked
	}
	if time.Now().After(ml.ExpiresAt) {
		return store.MagicLink{}, store.ErrTokenExpired
	}
	return ml, nil
}

func (f *fakeRepo) Revoke(ctx context.Context, token, by string) error {
	ml, ok := f.tokens[token]
	if !ok {
		return nil
	}
	now := time.Now()
	ml.RevokedAt = &now
	ml.RevokedByUserID = &by
	f.tokens[token] = ml
	return nil
}

// fakeEmitter records emitted audit events.
type fakeEmitter struct{ actions []string }

func (f *fakeEmitter) Emit(ctx context.Context, action, subject, actorUserID string, attrs map[string]string) error {
	f.actions = append(f.actions, action)
	return nil
}

func TestCreateMagicLinkNonSensitiveExpiry(t *testing.T) {
	repo := newFakeRepo()
	emitter := &fakeEmitter{}
	svc := magiclink.NewService(repo, emitter)

	resp, err := svc.Create(context.Background(), magiclink.CreateRequest{
		PolicyVersionID: "pv-001",
		CreatedByUserID: "user-001",
		Sensitive:       false,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Non-sensitive: expiry should be approximately 30 days out.
	in30 := time.Now().Add(30 * 24 * time.Hour)
	if resp.ExpiresAt.Before(in30.Add(-5*time.Minute)) || resp.ExpiresAt.After(in30.Add(5*time.Minute)) {
		t.Errorf("non-sensitive expiry = %v, want ~%v", resp.ExpiresAt, in30)
	}
	if len(emitter.actions) != 1 || emitter.actions[0] != "magic_link.created" {
		t.Errorf("expected audit event magic_link.created, got %v", emitter.actions)
	}
}

func TestCreateMagicLinkSensitiveShorterExpiry(t *testing.T) {
	repo := newFakeRepo()
	svc := magiclink.NewService(repo, &fakeEmitter{})

	resp, err := svc.Create(context.Background(), magiclink.CreateRequest{
		PolicyVersionID: "pv-002",
		CreatedByUserID: "user-001",
		Sensitive:       true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Sensitive: expiry should be approximately 48 hours out.
	in48h := time.Now().Add(48 * time.Hour)
	if resp.ExpiresAt.Before(in48h.Add(-5*time.Minute)) || resp.ExpiresAt.After(in48h.Add(5*time.Minute)) {
		t.Errorf("sensitive expiry = %v, want ~%v", resp.ExpiresAt, in48h)
	}
}

func TestResolveEmitsActivityAuditEvent(t *testing.T) {
	repo := newFakeRepo()
	emitter := &fakeEmitter{}
	svc := magiclink.NewService(repo, emitter)

	_, _ = svc.Create(context.Background(), magiclink.CreateRequest{
		PolicyVersionID: "pv-003", CreatedByUserID: "user-001", Sensitive: false,
	})

	// Retrieve the token string from the repo (inserted by Create above).
	var tok string
	for k := range repo.tokens {
		tok = k
	}

	// Reset recorded events so we only check what Resolve emits.
	emitter.actions = nil
	if _, err := svc.Resolve(context.Background(), magiclink.ResolveRequest{Token: tok, ViewerEmail: "viewer@example.com"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(emitter.actions) != 1 || emitter.actions[0] != "magic_link.accessed" {
		t.Errorf("expected activity event magic_link.accessed, got %v", emitter.actions)
	}
}

func TestRevokeBlocksSubsequentResolve(t *testing.T) {
	repo := newFakeRepo()
	svc := magiclink.NewService(repo, &fakeEmitter{})

	_, _ = svc.Create(context.Background(), magiclink.CreateRequest{
		PolicyVersionID: "pv-004", CreatedByUserID: "user-001", Sensitive: false,
	})
	var tok string
	for k := range repo.tokens {
		tok = k
	}

	if err := svc.Revoke(context.Background(), magiclink.RevokeRequest{Token: tok, RevokedByUserID: "admin-001"}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := svc.Resolve(context.Background(), magiclink.ResolveRequest{Token: tok, ViewerEmail: "ivan@example.org"}); !errors.Is(err, store.ErrTokenRevoked) {
		t.Errorf("expected ErrTokenRevoked after revoke, got %v", err)
	}
}
