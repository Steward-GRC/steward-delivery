// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Bugs5382/go-apperr/apperrgrpc"
	"github.com/Bugs5382/go-objectstore/memstore"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/grpcsvc"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/pdfrender"
	"github.com/Steward-GRC/steward-delivery/internal/policyhttp"
	"github.com/Steward-GRC/steward-delivery/internal/store"
)

func symbol(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	info, ok := apperrgrpc.FromError(err)
	require.True(t, ok, "a coded status: %v", err)
	return info.Symbol
}

type missingPolicy struct{ stubPolicyClient }

func (*missingPolicy) GetContent(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("version: %w", policyhttp.ErrNotFound)
}

func TestGetRenderedContentUnknownVersionIsNotFound(t *testing.T) {
	h := grpcsvc.NewDeliveryHandler(&missingPolicy{}, magiclink.NewService(newFakeMLRepo(), &noopEmitter{}))
	_, err := h.GetRenderedContent(context.Background(), &deliveryv1.GetRenderedContentRequest{PolicyVersionId: "pv-x"})
	require.Equal(t, "POLICY_VERSION_NOT_FOUND", symbol(t, err))
}

func TestEmptyVersionIDIsRefused(t *testing.T) {
	h := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, magiclink.NewService(newFakeMLRepo(), &noopEmitter{}))
	_, err := h.GetRenderedContent(context.Background(), &deliveryv1.GetRenderedContentRequest{})
	require.Equal(t, "INVALID_REQUEST", symbol(t, err))
	_, err = h.CreateMagicLink(context.Background(), &deliveryv1.CreateMagicLinkRequest{CreatedByUserId: "bob"})
	require.Equal(t, "INVALID_REQUEST", symbol(t, err))
}

type stateRepo struct{ err error }

func (r stateRepo) Create(context.Context, store.MagicLink) error { return nil }
func (r stateRepo) Resolve(context.Context, string) (store.MagicLink, error) {
	if r.err != nil {
		return store.MagicLink{}, r.err
	}
	return store.MagicLink{PolicyVersionID: "pv-1", Sensitive: true, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (r stateRepo) Revoke(context.Context, string, string) error { return nil }

func TestResolveMagicLinkStatesAreCoded(t *testing.T) {
	cases := map[string]error{
		"MAGIC_LINK_NOT_FOUND": fmt.Errorf("%w: tok", store.ErrTokenNotFound),
		"MAGIC_LINK_EXPIRED":   store.ErrTokenExpired,
		"MAGIC_LINK_REVOKED":   store.ErrTokenRevoked,
	}
	for want, repoErr := range cases {
		h := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, magiclink.NewService(stateRepo{err: repoErr}, &noopEmitter{}))
		_, err := h.ResolveMagicLink(context.Background(), &deliveryv1.ResolveMagicLinkRequest{Token: "tok", ViewerEmail: "ivan@example.org"})
		require.Equal(t, want, symbol(t, err))
	}
	h := grpcsvc.NewDeliveryHandler(&stubPolicyClient{}, magiclink.NewService(stateRepo{}, &noopEmitter{}))
	_, err := h.ResolveMagicLink(context.Background(), &deliveryv1.ResolveMagicLinkRequest{Token: "tok"})
	require.Equal(t, "VIEWER_EMAIL_REQUIRED", symbol(t, err))
}

type stateJobs struct{ err error }

func (s stateJobs) Create(context.Context, string, string, string) error { return nil }
func (s stateJobs) Get(_ context.Context, jobID string) (store.PDFJob, error) {
	return store.PDFJob{JobID: jobID, RequesterUserID: "bob"}, nil
}
func (s stateJobs) GetArtifactKey(context.Context, string) (string, error) {
	return "", s.err
}

func TestGetPDFDownloadLinkStatesAreCoded(t *testing.T) {
	cases := map[string]error{
		"PDF_EXPORT_NOT_FOUND": fmt.Errorf("%w: job", store.ErrPDFJobNotFound),
		"PDF_EXPORT_NOT_READY": fmt.Errorf("%w: job", store.ErrPDFJobNotReady),
		"PDF_EXPORT_FAILED":    fmt.Errorf("%w (%w): job", store.ErrPDFJobFailed, store.ErrPDFJobNotReady),
	}
	for want, jobErr := range cases {
		h := grpcsvc.NewDeliveryHandlerFull(&stubPolicyClient{}, magiclink.NewService(newFakeMLRepo(), &noopEmitter{}),
			nil, grpcsvc.PDFConfig{}, stateJobs{err: jobErr}, &stubSigner{url: "https://objects.example.org/signed"})
		_, err := h.GetPDFDownloadLink(as("bob"), &deliveryv1.GetPDFDownloadLinkRequest{JobId: "job"})
		require.Equal(t, want, symbol(t, err))
	}
}

func TestObjectStoreSignerPresignsTheKey(t *testing.T) {
	signer := grpcsvc.ObjectStoreSigner{Store: memstore.New(memstore.WithBaseURL("http://objects.example.org"))}
	url, err := signer.SignedURL(context.Background(), "artifacts/pv-1/job-1.pdf", 15*time.Minute)
	require.NoError(t, err)
	require.Contains(t, url, "artifacts/pv-1/job-1.pdf")
}

type sensitiveLookup bool

func (s sensitiveLookup) Sensitive(context.Context, string) (bool, error) { return bool(s), nil }

func TestExportWatermarksASensitivePolicyEvenWhenTheContentSaysOtherwise(t *testing.T) {
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		pdfrender.GVR: "PdfRenderList",
	})
	h := grpcsvc.NewDeliveryHandlerWithPDF(&stubPolicyClient{}, magiclink.NewService(newFakeMLRepo(), &noopEmitter{}),
		pdfrender.NewClient(dyn, "steward"), grpcsvc.PDFConfig{FetchURLBase: "http://delivery.example.org:8081", OutputBucket: "pdfs"}).
		WithSensitivity(sensitiveLookup(true))
	resp, err := h.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{PolicyVersionId: "pv-1", RequesterUserId: "bob"})
	require.NoError(t, err)
	got, err := dyn.Resource(pdfrender.GVR).Namespace("steward").Get(context.Background(), resp.JobId, metav1.GetOptions{})
	require.NoError(t, err)
	sen, _, _ := unstructured.NestedString(got.Object, "spec", "sensitivity")
	require.Equal(t, "sensitive", sen)
}
