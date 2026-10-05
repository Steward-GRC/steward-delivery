// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/grpcsvc"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/store"
)

// stubPolicyClient returns canned Lexical JSON for any policy version. It
// stands in for the real Policy gRPC client during unit tests of the delivery
// handler — the renderer is real, the diff is not (we just want to confirm the
// handler forwards what the client returns).
type stubPolicyClient struct{}

func (s *stubPolicyClient) GetContent(_ context.Context, _ string) ([]byte, error) {
	return []byte(`{
		"root": { "type": "root", "children": [
			{ "type": "SectionNode", "sectionKey": "s1", "title": "Title", "children": [
				{ "type": "EditableRegionNode", "children": [
					{ "type": "paragraph", "children": [{ "type": "text", "text": "Content." }] }
				]}
			]}
		]},
		"sensitivity": false
	}`), nil
}

func (s *stubPolicyClient) GetDiff(_ context.Context, _, _ string) ([]*deliveryv1.SectionDiff, error) {
	return []*deliveryv1.SectionDiff{{
		SectionKey: "s1", ChangeType: "changed", DiffHtml: "<ins>Content.</ins>", Boilerplate: false,
	}}, nil
}

// fakeMLRepo is an in-memory Repo for the magic-link service. It lets the
// handler tests exercise CreateMagicLink without a Postgres instance — the
// magic-link service contract is already covered by service_test.go.
type fakeMLRepo struct{ tokens map[string]store.MagicLink }

func newFakeMLRepo() *fakeMLRepo { return &fakeMLRepo{tokens: map[string]store.MagicLink{}} }

func (f *fakeMLRepo) Create(_ context.Context, ml store.MagicLink) error {
	f.tokens[ml.Token] = ml
	return nil
}

func (f *fakeMLRepo) Resolve(_ context.Context, token string) (store.MagicLink, error) {
	ml, ok := f.tokens[token]
	if !ok {
		return store.MagicLink{}, store.ErrTokenNotFound
	}
	return ml, nil
}

func (f *fakeMLRepo) Revoke(_ context.Context, _, _ string) error { return nil }

// noopEmitter satisfies magiclink.AuditEmitter without recording anything.
type noopEmitter struct{}

func (n *noopEmitter) Emit(_ context.Context, _, _, _ string, _ map[string]string) error {
	return nil
}

func TestGetRenderedContentReturnsHTML(t *testing.T) {
	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	handler := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, mlSvc)

	resp, err := handler.GetRenderedContent(context.Background(), &deliveryv1.GetRenderedContentRequest{
		PolicyVersionId: "pv-001",
	})
	if err != nil {
		t.Fatalf("GetRenderedContent: %v", err)
	}
	if resp.Html == "" {
		t.Error("expected non-empty HTML")
	}
}

func TestCreateMagicLinkReturnsToken(t *testing.T) {
	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	handler := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, mlSvc)

	resp, err := handler.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{
		PolicyVersionId: "pv-001", CreatedByUserId: "user-001", Sensitive: false,
	})
	if err != nil {
		t.Fatalf("CreateMagicLink: %v", err)
	}
	if resp.Token == "" {
		t.Error("expected non-empty token")
	}
}

// stubPDFJobStore satisfies grpcsvc.PDFJobStore for unit tests of the
// download-link path. It pre-seeds a single "completed" job by key and
// returns ErrPDFJobNotFound (via fmt.Errorf so we can use errors.Is later
// if we want) for any other job ID.
type stubPDFJobStore struct{ key string }

func (s *stubPDFJobStore) Create(_ context.Context, _, _, _ string) error { return nil }
func (s *stubPDFJobStore) GetArtifactKey(_ context.Context, jobID string) (string, error) {
	if jobID == "job-001" {
		return s.key, nil
	}
	return "", fmt.Errorf("pdf job not found: %s", jobID)
}

// stubSigner satisfies grpcsvc.Signer with a canned URL so the handler
// test exercises only the orchestration (job store → signer → response),
// not the real S3 presign logic which is covered by storage/s3_test.go.
type stubSigner struct{ url string }

func (s *stubSigner) SignedURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return s.url, nil
}

func TestGetPDFDownloadLinkReturnedAfterJobComplete(t *testing.T) {
	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	jobStore := &stubPDFJobStore{key: "artifacts/pv-001/job-001.pdf"}
	handler := grpcsvc.NewDeliveryHandlerFull(
		&stubPolicyClient{},
		mlSvc,
		nil, // pdfClient: not exercised on the read path
		grpcsvc.PDFConfig{},
		jobStore,
		&stubSigner{url: "https://minio/signed"},
	)

	resp, err := handler.GetPDFDownloadLink(context.Background(), &deliveryv1.GetPDFDownloadLinkRequest{
		JobId: "job-001",
	})
	if err != nil {
		t.Fatalf("GetPDFDownloadLink: %v", err)
	}
	if resp.SignedUrl != "https://minio/signed" {
		t.Errorf("got SignedUrl=%q, want %q", resp.SignedUrl, "https://minio/signed")
	}
	if resp.ExpiresAt == nil {
		t.Error("expected ExpiresAt to be set")
	}
}

func TestGetPDFDownloadLinkUnknownJobReturnsError(t *testing.T) {
	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	handler := grpcsvc.NewDeliveryHandlerFull(
		&stubPolicyClient{},
		mlSvc,
		nil,
		grpcsvc.PDFConfig{},
		&stubPDFJobStore{key: "artifacts/pv-001/job-001.pdf"},
		&stubSigner{url: "https://minio/signed"},
	)

	_, err := handler.GetPDFDownloadLink(context.Background(), &deliveryv1.GetPDFDownloadLinkRequest{
		JobId: "no-such-job",
	})
	if err == nil {
		t.Fatal("expected error for unknown job")
	}
}

// TestRequestPDFExportDisabledReturnsUnavailable covers the case where PDF
// export is disabled (PDF_EXPORT_ENABLED=false, or no in-cluster config in
// local dev) — main.go leaves pdfClient nil, and the handler must fail fast
// with a typed gRPC error instead of silently no-op'ing.
func TestRequestPDFExportDisabledReturnsUnavailable(t *testing.T) {
	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	handler := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, mlSvc)

	_, err := handler.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{
		PolicyVersionId: "pv-001",
		RequesterUserId: "user-001",
	})
	if err == nil {
		t.Fatal("expected error when PDF export is disabled")
	}
	if got := status.Code(err); got != codes.Unavailable {
		t.Errorf("status code: got %v want %v", got, codes.Unavailable)
	}
}

func TestGetDiffReturnsSections(t *testing.T) {
	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	handler := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, mlSvc)

	resp, err := handler.GetDiff(context.Background(), &deliveryv1.GetDiffRequest{
		PolicyVersionIdFrom: "pv-001", PolicyVersionIdTo: "pv-002",
	})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(resp.Sections) == 0 {
		t.Error("expected at least one section diff")
	}
}
