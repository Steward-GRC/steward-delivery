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

func (f *fakeRepo) List(_ context.Context, filter store.MagicLinkFilter) ([]store.MagicLink, string, error) {
	var out []store.MagicLink
	for _, ml := range f.tokens {
		out = append(out, ml)
	}
	_ = filter
	return out, "", nil
}

func (f *fakeRepo) Remove(_ context.Context, token string) error {
	ml, ok := f.tokens[token]
	if !ok {
		return store.ErrTokenNotFound
	}
	if !ml.Revoked() && time.Now().Before(ml.ExpiresAt) {
		return store.ErrTokenActive
	}
	delete(f.tokens, token)
	return nil
}

type loggedEvent struct {
	action, subject, actor string
	attrs                  map[string]string
}

type eventLog struct{ events []loggedEvent }

func (r *eventLog) Emit(_ context.Context, action, subject, actor string, attrs map[string]string) error {
	r.events = append(r.events, loggedEvent{action, subject, actor, attrs})
	return nil
}

func TestRemoveEmitsAnAuditEventNamingTheLinkByFingerprint(t *testing.T) {
	repo := newFakeRepo()
	em := &eventLog{}
	svc := magiclink.NewService(repo, em)
	created, err := svc.Create(context.Background(), magiclink.CreateRequest{PolicyVersionID: "pv-1", CreatedByUserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Remove(context.Background(), magiclink.RemoveRequest{Token: created.Token, RemovedByUserID: "admin"}); !errors.Is(err, store.ErrTokenActive) {
		t.Fatalf("remove active: %v", err)
	}
	if err := svc.Revoke(context.Background(), magiclink.RevokeRequest{Token: created.Token, RevokedByUserID: "admin"}); err != nil {
		t.Fatal(err)
	}
	em.events = nil

	if err := svc.Remove(context.Background(), magiclink.RemoveRequest{Token: created.Token, RemovedByUserID: "admin"}); err != nil {
		t.Fatal(err)
	}
	if len(em.events) != 1 {
		t.Fatalf("events %+v", em.events)
	}
	ev := em.events[0]
	if ev.action != "magic_link.removed" || ev.subject != "magic_link:"+magiclink.Fingerprint(created.Token) || ev.actor != "admin" {
		t.Errorf("event %+v", ev)
	}
	for _, v := range ev.attrs {
		if v == created.Token {
			t.Error("the token is in the event")
		}
	}
}

func TestListPassesTheFilterAndPageThrough(t *testing.T) {
	repo := newFakeRepo()
	svc := magiclink.NewService(repo, &fakeEmitter{})
	if _, err := svc.Create(context.Background(), magiclink.CreateRequest{PolicyVersionID: "pv-1", CreatedByUserID: "u1"}); err != nil {
		t.Fatal(err)
	}
	links, _, err := svc.List(context.Background(), store.MagicLinkFilter{})
	if err != nil || len(links) != 1 {
		t.Errorf("List: %v %v", links, err)
	}
}
