// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// recordingJobs is a PDFJobStore that also keeps the history it is given.
type recordingJobs struct {
	created   []string
	saved     map[string][]byte
	saveErr   error
	createErr error
}

func (r *recordingJobs) Create(_ context.Context, jobID, _, _ string) error {
	r.created = append(r.created, jobID)
	return r.createErr
}
func (r *recordingJobs) Get(context.Context, string) (store.PDFJob, error) {
	return store.PDFJob{}, store.ErrPDFJobNotFound
}
func (r *recordingJobs) GetArtifactKey(context.Context, string) (string, error) {
	return "", store.ErrPDFJobNotFound
}
func (r *recordingJobs) SetJobHistory(_ context.Context, jobID string, history []byte) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	if r.saved == nil {
		r.saved = map[string][]byte{}
	}
	r.saved[jobID] = history
	return nil
}

func historyHandler(t *testing.T, jobs *recordingJobs, withStore bool) (*grpcsvc.DeliveryHandler, func(string) *unstructured.Unstructured) {
	t.Helper()
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		pdfrender.GVR: "PdfRenderList",
	})
	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	h := grpcsvc.NewDeliveryHandlerFull(&stubPolicyClient{}, mlSvc, pdfrender.NewClient(dyn, "ns"),
		grpcsvc.PDFConfig{FetchURLBase: "http://delivery:8081", OutputBucket: "b"}, jobs, nil)
	if withStore {
		h = h.WithHistoryStore(jobs)
	}
	get := func(name string) *unstructured.Unstructured {
		u, err := dyn.Resource(pdfrender.GVR).Namespace("ns").Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return nil
		}
		return u
	}
	return h, get
}

func TestRequestPDFExportKeepsTheHistoryAndPointsTheFetchAtTheJob(t *testing.T) {
	jobs := &recordingJobs{}
	h, get := historyHandler(t, jobs, true)
	at := time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)

	resp, err := h.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{
		PolicyVersionId: "pv-1", RequesterUserId: "u1", IncludeHistory: true,
		History: []*deliveryv1.VersionHistoryEntry{
			{OccurredAt: timestamppb.New(at), VersionNumber: 2, Label: "Approved", ActorName: "Lee Park"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := jobs.saved[resp.GetJobId()]
	if !ok {
		t.Fatalf("history not saved for %s: %v", resp.GetJobId(), jobs.saved)
	}
	var back []policyhttp.HistoryEntry
	if err := json.Unmarshal(raw, &back); err != nil || len(back) != 1 || back[0].Label != "Approved" || back[0].Actor != "Lee Park" || back[0].Version != 2 || !back[0].At.Equal(at) {
		t.Errorf("saved %s (%v)", raw, err)
	}
	url, _, _ := unstructured.NestedString(get(resp.GetJobId()).Object, "spec", "fetchURL")
	if want := "http://delivery:8081/internal/policies/pv-1/html?job=" + resp.GetJobId(); url != want {
		t.Errorf("fetchURL %q, want %q", url, want)
	}
}

func TestRequestPDFExportWithoutHistoryKeepsTheOldFetchURL(t *testing.T) {
	jobs := &recordingJobs{}
	h, get := historyHandler(t, jobs, true)

	resp, err := h.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{PolicyVersionId: "pv-1", RequesterUserId: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs.saved) != 0 {
		t.Errorf("history saved without being asked: %v", jobs.saved)
	}
	url, _, _ := unstructured.NestedString(get(resp.GetJobId()).Object, "spec", "fetchURL")
	if want := "http://delivery:8081/internal/policies/pv-1/html"; url != want {
		t.Errorf("fetchURL %q, want %q", url, want)
	}
}

func TestRequestPDFExportAsksForHistoryWithNoEventsKeepsAnEmptyList(t *testing.T) {
	jobs := &recordingJobs{}
	h, _ := historyHandler(t, jobs, true)

	resp, err := h.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{PolicyVersionId: "pv-1", RequesterUserId: "u1", IncludeHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(jobs.saved[resp.GetJobId()]); got != "[]" {
		t.Errorf("saved %q, want []", got)
	}
}

func TestRequestPDFExportRefusesHistoryItCannotKeepBeforeAnyJobExists(t *testing.T) {
	jobs := &recordingJobs{}
	h, _ := historyHandler(t, jobs, false)

	_, err := h.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{PolicyVersionId: "pv-1", RequesterUserId: "u1", IncludeHistory: true})
	if status.Code(err) != codes.Unavailable {
		t.Errorf("code %v, want Unavailable (%v)", status.Code(err), err)
	}
	if len(jobs.created) != 0 {
		t.Errorf("a job was created: %v", jobs.created)
	}
}

func TestRequestPDFExportRefusesTooManyHistoryEntries(t *testing.T) {
	jobs := &recordingJobs{}
	h, _ := historyHandler(t, jobs, true)
	entries := make([]*deliveryv1.VersionHistoryEntry, 501)
	for i := range entries {
		entries[i] = &deliveryv1.VersionHistoryEntry{Label: "x"}
	}

	_, err := h.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{PolicyVersionId: "pv-1", RequesterUserId: "u1", IncludeHistory: true, History: entries})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("code %v, want InvalidArgument (%v)", status.Code(err), err)
	}
	if len(jobs.created) != 0 {
		t.Errorf("a job was created: %v", jobs.created)
	}
}

func TestRequestPDFExportFailsWhenTheHistoryCannotBeSaved(t *testing.T) {
	jobs := &recordingJobs{saveErr: errors.New("db down")}
	h, _ := historyHandler(t, jobs, true)

	_, err := h.RequestPDFExport(context.Background(), &deliveryv1.RequestPDFExportRequest{PolicyVersionId: "pv-1", RequesterUserId: "u1", IncludeHistory: true})
	if err == nil {
		t.Fatal("expected an error")
	}
}
