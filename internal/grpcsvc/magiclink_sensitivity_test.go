// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/grpcsvc"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/policyhttp"
)

// classified is a SensitivityLookup that answers from a fixed value or error.
type classified struct {
	sensitive bool
	err       error
}

func (c classified) Sensitive(context.Context, string) (bool, error) { return c.sensitive, c.err }

func linkHandler(t *testing.T, policy grpcsvc.PolicyClient, lookup grpcsvc.SensitivityLookup) (*grpcsvc.DeliveryHandler, *fakeMLRepo) {
	t.Helper()
	repo := newFakeMLRepo()
	h := grpcsvc.NewDeliveryHandler(policy, magiclink.NewService(repo, &noopEmitter{}))
	if lookup != nil {
		h = h.WithSensitivity(lookup)
	}
	return h, repo
}

func TestCreateMagicLinkTakesSensitivityFromThePolicyNotTheCaller(t *testing.T) {
	h, repo := linkHandler(t, &stubPolicyClient{}, classified{sensitive: true})

	resp, err := h.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{
		PolicyVersionId: "pv-1", CreatedByUserId: "u1", Sensitive: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	ml := repo.tokens[resp.GetToken()]
	if !ml.Sensitive {
		t.Fatal("link to a sensitive policy was stored as not sensitive")
	}
	if got := time.Until(resp.GetExpiresAt().AsTime()); got > 48*time.Hour || got < 47*time.Hour {
		t.Errorf("expiry in %v, want about 48h", got)
	}

	// The viewer is asked for an email and the view is watermarked.
	_, err = h.ResolveMagicLink(context.Background(), &deliveryv1.ResolveMagicLinkRequest{Token: resp.GetToken()})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("resolve without email: %v", err)
	}
	got, err := h.ResolveMagicLink(context.Background(), &deliveryv1.ResolveMagicLinkRequest{Token: resp.GetToken(), ViewerEmail: "v@example.org"})
	if err != nil || !got.GetSensitive() || !got.GetWatermarkRequired() {
		t.Errorf("resolve with email: %+v %v", got, err)
	}
}

func TestCreateMagicLinkIgnoresASensitiveFlagForAPlainPolicy(t *testing.T) {
	h, repo := linkHandler(t, &stubPolicyClient{}, classified{sensitive: false})

	resp, err := h.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{
		PolicyVersionId: "pv-1", CreatedByUserId: "u1", Sensitive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if repo.tokens[resp.GetToken()].Sensitive {
		t.Error("the caller's flag made a plain policy's link sensitive")
	}
	if got := time.Until(resp.GetExpiresAt().AsTime()); got < 29*24*time.Hour {
		t.Errorf("expiry in %v, want about 30 days", got)
	}
}

// contentFlagged is a policy whose content marks itself sensitive.
type contentFlagged struct{ stubPolicyClient }

func (*contentFlagged) GetContent(context.Context, string) ([]byte, error) {
	return []byte(`{"root":{"type":"root","children":[]},"sensitivity":true}`), nil
}

func TestCreateMagicLinkHonoursTheContentFlagToo(t *testing.T) {
	h, repo := linkHandler(t, &contentFlagged{}, classified{sensitive: false})

	resp, err := h.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{PolicyVersionId: "pv-1", CreatedByUserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if !repo.tokens[resp.GetToken()].Sensitive {
		t.Error("content marked sensitive but the link is not")
	}
}

func TestCreateMagicLinkFailsClosedWhenTheClassificationCannotBeRead(t *testing.T) {
	h, repo := linkHandler(t, &stubPolicyClient{}, classified{err: errors.New("core down")})

	_, err := h.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{PolicyVersionId: "pv-1", CreatedByUserId: "u1", Sensitive: false})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("code %v, want Unavailable (%v)", status.Code(err), err)
	}
	if len(repo.tokens) != 0 {
		t.Error("a link was stored")
	}
}

type missingVersion struct{ stubPolicyClient }

func (*missingVersion) GetContent(context.Context, string) ([]byte, error) {
	return nil, policyhttp.ErrNotFound
}

func TestCreateMagicLinkToAnUnknownVersionIsNotFound(t *testing.T) {
	h, repo := linkHandler(t, &missingVersion{}, classified{})

	_, err := h.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{PolicyVersionId: "nope", CreatedByUserId: "u1"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("code %v, want NotFound (%v)", status.Code(err), err)
	}
	if len(repo.tokens) != 0 {
		t.Error("a link was stored")
	}
}

func TestResolveMagicLinkAsksForTheEmailOnceThePolicyBecameSensitive(t *testing.T) {
	// The link was made while the policy was plain; it was classified after.
	repo := newFakeMLRepo()
	svc := magiclink.NewService(repo, &noopEmitter{})
	plain := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, svc).WithSensitivity(classified{sensitive: false})
	created, err := plain.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{PolicyVersionId: "pv-1", CreatedByUserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	h := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, svc).WithSensitivity(classified{sensitive: true})

	_, err = h.ResolveMagicLink(context.Background(), &deliveryv1.ResolveMagicLinkRequest{Token: created.GetToken()})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("resolve without email: %v", err)
	}
	got, err := h.ResolveMagicLink(context.Background(), &deliveryv1.ResolveMagicLinkRequest{Token: created.GetToken(), ViewerEmail: "v@example.org"})
	if err != nil || !got.GetSensitive() || !got.GetWatermarkRequired() {
		t.Errorf("resolve with email: %+v %v", got, err)
	}
}

func TestResolveMagicLinkFailsClosedWhenTheClassificationCannotBeRead(t *testing.T) {
	repo := newFakeMLRepo()
	svc := magiclink.NewService(repo, &noopEmitter{})
	created, err := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, svc).WithSensitivity(classified{}).
		CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{PolicyVersionId: "pv-1", CreatedByUserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	h := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, svc).WithSensitivity(classified{err: errors.New("core down")})

	_, err = h.ResolveMagicLink(context.Background(), &deliveryv1.ResolveMagicLinkRequest{Token: created.GetToken(), ViewerEmail: "v@example.org"})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("code %v, want Unavailable (%v)", status.Code(err), err)
	}
}
