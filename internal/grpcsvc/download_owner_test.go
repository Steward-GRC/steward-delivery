// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc_test

import (
	"context"
	"fmt"
	"testing"

	grpcactor "github.com/Bugs5382/go-grpc-actor"
	"github.com/stretchr/testify/require"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/grpcsvc"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/store"
)

// ownedJobs holds one done job requested by bob, and counts artifact-key
// reads so a test can tell whether a refused caller learned the job's state.
type ownedJobs struct{ keyReads int }

func (o *ownedJobs) Create(context.Context, string, string, string) error { return nil }

func (o *ownedJobs) Get(_ context.Context, jobID string) (store.PDFJob, error) {
	if jobID != "job-1" {
		return store.PDFJob{}, fmt.Errorf("%w: %s", store.ErrPDFJobNotFound, jobID)
	}
	return store.PDFJob{JobID: "job-1", PolicyVersionID: "pv-1", RequesterUserID: "bob", Status: store.PDFJobStatusDone}, nil
}

func (o *ownedJobs) GetArtifactKey(context.Context, string) (string, error) {
	o.keyReads++
	return "artifacts/pv-1/job-1.pdf", nil
}

func downloadHandler(jobs grpcsvc.PDFJobStore) *grpcsvc.DeliveryHandler {
	return grpcsvc.NewDeliveryHandlerFull(&stubPolicyClient{}, magiclink.NewService(newFakeMLRepo(), &noopEmitter{}),
		nil, grpcsvc.PDFConfig{}, jobs, &stubSigner{url: "https://objects.example.org/signed"})
}

func as(user string) context.Context {
	return grpcactor.WithActor(context.Background(), grpcactor.Actor{Subject: user})
}

func TestGetPDFDownloadLinkServesTheRequester(t *testing.T) {
	resp, err := downloadHandler(&ownedJobs{}).GetPDFDownloadLink(as("bob"), &deliveryv1.GetPDFDownloadLinkRequest{JobId: "job-1"})
	require.NoError(t, err)
	require.Equal(t, "https://objects.example.org/signed", resp.GetSignedUrl())
}

func TestGetPDFDownloadLinkRefusesAnotherUserAsNotFound(t *testing.T) {
	jobs := &ownedJobs{}
	resp, err := downloadHandler(jobs).GetPDFDownloadLink(as("mallory"), &deliveryv1.GetPDFDownloadLinkRequest{JobId: "job-1"})
	require.Nil(t, resp)
	require.Equal(t, "PDF_EXPORT_NOT_FOUND", symbol(t, err))
	require.Zero(t, jobs.keyReads, "a refused caller must not learn the job's state")
}

func TestGetPDFDownloadLinkWithoutAUserIsNotFound(t *testing.T) {
	jobs := &ownedJobs{}
	resp, err := downloadHandler(jobs).GetPDFDownloadLink(context.Background(), &deliveryv1.GetPDFDownloadLinkRequest{JobId: "job-1"})
	require.Nil(t, resp)
	require.Equal(t, "PDF_EXPORT_NOT_FOUND", symbol(t, err))
	require.Zero(t, jobs.keyReads)
}

func TestGetPDFDownloadLinkUnknownJobIsNotFound(t *testing.T) {
	_, err := downloadHandler(&ownedJobs{}).GetPDFDownloadLink(as("bob"), &deliveryv1.GetPDFDownloadLinkRequest{JobId: "job-2"})
	require.Equal(t, "PDF_EXPORT_NOT_FOUND", symbol(t, err))
}
