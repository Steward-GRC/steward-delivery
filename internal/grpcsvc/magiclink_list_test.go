// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc_test

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/grpcsvc"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/store"
)

func seededLinks(repo *fakeMLRepo) {
	now := time.Now()
	revoked := now.Add(-time.Minute)
	by := "admin"
	repo.tokens["live"] = store.MagicLink{Token: "live", PolicyVersionID: "pv-1", CreatedByUserID: "u1", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	repo.tokens["gone"] = store.MagicLink{Token: "gone", PolicyVersionID: "pv-2", CreatedByUserID: "u2", Sensitive: true, ExpiresAt: now.Add(time.Hour), RevokedAt: &revoked, RevokedByUserID: &by, CreatedAt: now}
	repo.tokens["old"] = store.MagicLink{Token: "old", PolicyVersionID: "pv-1", CreatedByUserID: "u1", ExpiresAt: now.Add(-time.Hour), CreatedAt: now}
}

func TestListMagicLinksReportsEachLinkWithItsStatus(t *testing.T) {
	repo := newFakeMLRepo()
	seededLinks(repo)
	h := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, magiclink.NewService(repo, &noopEmitter{}))

	resp, err := h.ListMagicLinks(context.Background(), &deliveryv1.ListMagicLinksRequest{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*deliveryv1.MagicLinkInfo{}
	for _, l := range resp.GetLinks() {
		got[l.GetToken()] = l
	}
	if len(got) != 3 {
		t.Fatalf("links %v", resp.GetLinks())
	}
	for token, want := range map[string]string{"live": "active", "gone": "revoked", "old": "expired"} {
		if got[token].GetStatus() != want {
			t.Errorf("%s: status %q, want %q", token, got[token].GetStatus(), want)
		}
	}
	g := got["gone"]
	if g.GetPolicyVersionId() != "pv-2" || g.GetCreatedByUserId() != "u2" || !g.GetSensitive() ||
		g.GetFingerprint() != magiclink.Fingerprint("gone") || g.GetRevokedByUserId() != "admin" ||
		g.GetCreatedAt() == nil || g.GetExpiresAt() == nil || g.GetRevokedAt() == nil {
		t.Errorf("gone: %+v", g)
	}
	if got["live"].GetRevokedAt() != nil {
		t.Error("live link has a revoked time")
	}
}

func TestRemoveMagicLinkCodes(t *testing.T) {
	repo := newFakeMLRepo()
	seededLinks(repo)
	h := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, magiclink.NewService(repo, &noopEmitter{}))
	ctx := context.Background()

	if _, err := h.RemoveMagicLink(ctx, &deliveryv1.RemoveMagicLinkRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Errorf("empty token: %v", err)
	}
	if _, err := h.RemoveMagicLink(ctx, &deliveryv1.RemoveMagicLinkRequest{Token: "nope"}); status.Code(err) != codes.NotFound {
		t.Errorf("unknown: %v", err)
	}
	if _, err := h.RemoveMagicLink(ctx, &deliveryv1.RemoveMagicLinkRequest{Token: "live"}); status.Code(err) != codes.FailedPrecondition {
		t.Errorf("active: %v", err)
	}
	for _, tok := range []string{"gone", "old"} {
		resp, err := h.RemoveMagicLink(ctx, &deliveryv1.RemoveMagicLinkRequest{Token: tok, RemovedByUserId: "admin"})
		if err != nil || !resp.GetRemoved() {
			t.Errorf("remove %s: %v %v", tok, resp, err)
		}
	}
	if len(repo.tokens) != 1 {
		t.Errorf("left %v", repo.tokens)
	}
}

func TestCallerPolicyCoversTheNewMethods(t *testing.T) {
	p := grpcsvc.CallerPolicy()
	for _, m := range []string{"ListMagicLinks", "RemoveMagicLink"} {
		if _, ok := p["/steward.delivery.v1.DeliveryService/"+m]; !ok {
			t.Errorf("%s not in the caller policy", m)
		}
	}
}
