// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package corepolicy_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"

	policyv1 "github.com/Steward-GRC/steward-delivery/gen/go/thirdparty/core/v1"
	"github.com/Steward-GRC/steward-delivery/internal/corepolicy"
	"github.com/Steward-GRC/steward-delivery/internal/policyhttp"
)

// fakeCore is an in-memory policyv1.PolicyService used to prove the corepolicy
// client forwards real data from core's RPCs into the delivery shapes without
// standing up a gRPC server.
type fakeCore struct {
	getResp  *policyv1.GetPolicyVersionResponse
	getErr   error
	diffResp *policyv1.DiffVersionsResponse
	diffErr  error

	gotGetID   string
	gotDiffReq *policyv1.DiffVersionsRequest
}

func (f *fakeCore) GetPolicyVersion(_ context.Context, in *policyv1.GetPolicyVersionRequest, _ ...grpc.CallOption) (*policyv1.GetPolicyVersionResponse, error) {
	f.gotGetID = in.GetId()
	return f.getResp, f.getErr
}

func (f *fakeCore) DiffVersions(_ context.Context, in *policyv1.DiffVersionsRequest, _ ...grpc.CallOption) (*policyv1.DiffVersionsResponse, error) {
	f.gotDiffReq = in
	return f.diffResp, f.diffErr
}

func TestGetContentReturnsVersionContentJSON(t *testing.T) {
	want := `{"root":{"type":"root","children":[{"type":"SectionNode","sectionKey":"s1"}]},"sensitivity":true}`
	core := &fakeCore{
		getResp: &policyv1.GetPolicyVersionResponse{
			Version: &policyv1.PolicyVersion{Id: "pv-1", ContentJson: want},
		},
	}
	c := corepolicy.New(core)

	got, err := c.GetContent(context.Background(), "pv-1")
	if err != nil {
		t.Fatalf("GetContent: %v", err)
	}
	if string(got) != want {
		t.Errorf("content:\n got %q\nwant %q", got, want)
	}
	if core.gotGetID != "pv-1" {
		t.Errorf("GetPolicyVersion id: got %q want pv-1", core.gotGetID)
	}
}

func TestGetContentNilVersionIsNotFound(t *testing.T) {
	core := &fakeCore{getResp: &policyv1.GetPolicyVersionResponse{Version: nil}}
	c := corepolicy.New(core)

	_, err := c.GetContent(context.Background(), "missing")
	if !errors.Is(err, policyhttp.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetContentPropagatesRPCError(t *testing.T) {
	core := &fakeCore{getErr: errors.New("boom")}
	c := corepolicy.New(core)

	if _, err := c.GetContent(context.Background(), "pv-1"); err == nil {
		t.Fatal("expected error from RPC failure")
	}
}

func TestGetDiffMapsCoreSectionDiffs(t *testing.T) {
	core := &fakeCore{
		diffResp: &policyv1.DiffVersionsResponse{
			Diffs: []*policyv1.SectionDiff{
				{
					SectionKey:    "s1",
					SectionTitle:  "Purpose", // dropped: no delivery field
					ChangeType:    "changed",
					WordDiffHtml:  "<ins>new</ins>",
					IsBoilerplate: false,
				},
				{
					SectionKey:    "s2",
					ChangeType:    "added",
					WordDiffHtml:  "<ins>added section</ins>",
					IsBoilerplate: true,
				},
			},
		},
	}
	c := corepolicy.New(core)

	got, err := c.GetDiff(context.Background(), "pv-1", "pv-2")
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if core.gotDiffReq.GetFromVersionId() != "pv-1" || core.gotDiffReq.GetToVersionId() != "pv-2" {
		t.Errorf("diff req: from=%q to=%q", core.gotDiffReq.GetFromVersionId(), core.gotDiffReq.GetToVersionId())
	}
	if len(got) != 2 {
		t.Fatalf("got %d diffs, want 2", len(got))
	}

	d0 := got[0]
	if d0.GetSectionKey() != "s1" || d0.GetChangeType() != "changed" ||
		d0.GetDiffHtml() != "<ins>new</ins>" || d0.GetBoilerplate() {
		t.Errorf("diff[0] mapping wrong: %+v", d0)
	}
	d1 := got[1]
	if d1.GetSectionKey() != "s2" || d1.GetChangeType() != "added" ||
		d1.GetDiffHtml() != "<ins>added section</ins>" || !d1.GetBoilerplate() {
		t.Errorf("diff[1] mapping wrong: %+v", d1)
	}
}

func TestGetDiffEmptyReturnsEmptySlice(t *testing.T) {
	core := &fakeCore{diffResp: &policyv1.DiffVersionsResponse{}}
	c := corepolicy.New(core)

	got, err := c.GetDiff(context.Background(), "a", "b")
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty diff slice, got %d", len(got))
	}
}

func TestGetDiffPropagatesRPCError(t *testing.T) {
	core := &fakeCore{diffErr: errors.New("boom")}
	c := corepolicy.New(core)

	if _, err := c.GetDiff(context.Background(), "a", "b"); err == nil {
		t.Fatal("expected error from RPC failure")
	}
}
