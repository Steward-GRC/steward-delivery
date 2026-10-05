// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package magiclink_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
)

type event struct {
	action, subject string
	attrs           map[string]string
}

type recordingEmitter struct{ events []event }

func (r *recordingEmitter) Emit(_ context.Context, action, subject, _ string, attrs map[string]string) error {
	r.events = append(r.events, event{action, subject, attrs})
	return nil
}

func near(t *testing.T, got time.Time, d time.Duration) {
	t.Helper()
	want := time.Now().Add(d)
	if got.Before(want.Add(-time.Minute)) || got.After(want.Add(time.Minute)) {
		t.Errorf("expiry = %v, want ~%v", got, want)
	}
}

func TestCreateUsesTheConfiguredTTLs(t *testing.T) {
	svc := magiclink.NewService(newFakeRepo(), &fakeEmitter{}, magiclink.WithTTLs(6*time.Hour, 30*time.Minute))
	ctx := context.Background()

	resp, err := svc.Create(ctx, magiclink.CreateRequest{PolicyVersionID: "pv-1", CreatedByUserID: "bob"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	near(t, resp.ExpiresAt, 6*time.Hour)

	resp, err = svc.Create(ctx, magiclink.CreateRequest{PolicyVersionID: "pv-1", CreatedByUserID: "bob", Sensitive: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	near(t, resp.ExpiresAt, 30*time.Minute)
}

func TestAuditEventsNeverCarryTheToken(t *testing.T) {
	repo := newFakeRepo()
	rec := &recordingEmitter{}
	svc := magiclink.NewService(repo, rec)
	ctx := context.Background()

	created, err := svc.Create(ctx, magiclink.CreateRequest{PolicyVersionID: "pv-1", CreatedByUserID: "bob"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Resolve(ctx, magiclink.ResolveRequest{Token: created.Token, ViewerEmail: "ivan@example.org"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := svc.Revoke(ctx, magiclink.RevokeRequest{Token: created.Token, RevokedByUserID: "bob"}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(rec.events) != 3 {
		t.Fatalf("got %d events, want 3", len(rec.events))
	}
	fp := magiclink.Fingerprint(created.Token)
	for _, e := range rec.events {
		if strings.Contains(e.subject, created.Token) {
			t.Errorf("%s: subject carries the token", e.action)
		}
		for k, v := range e.attrs {
			if strings.Contains(v, created.Token) {
				t.Errorf("%s: attribute %s carries the token", e.action, k)
			}
		}
		if e.action != "magic_link.revoked" && e.attrs["link"] != fp {
			t.Errorf("%s: link attribute = %q, want the fingerprint %q", e.action, e.attrs["link"], fp)
		}
	}
	if rec.events[2].subject != "magic_link:"+fp {
		t.Errorf("revoke subject = %q", rec.events[2].subject)
	}
	if len(fp) != 16 {
		t.Errorf("fingerprint %q: want 16 hex characters", fp)
	}
}
