// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package pdfrender

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	runtimeschema "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func newFakeDyn(t *testing.T) (*fake.FakeDynamicClient, runtimeschema.GroupVersionResource) {
	t.Helper()
	scheme := runtime.NewScheme()
	// The fake dynamic client wants to know the list-kind for the
	// resource so its tracker can return the right object on Watch/List.
	listKinds := map[runtimeschema.GroupVersionResource]string{
		GVR: "PdfRenderList",
	}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)
	return client, GVR
}

func TestClient_Create(t *testing.T) {
	dyn, gvr := newFakeDyn(t)
	c := NewClient(dyn, "policy-test")

	spec := Spec{
		PolicyVersionID: "pv-42",
		FetchURL:        "http://delivery.example.org/internal/policies/pv-42/html",
		OutputBucket:    "policy-artifacts",
		OutputKey:       "artifacts/pv-42/job-1.pdf",
		Sensitivity:     "sensitive",
		RequestedBy:     "user-1",
	}
	if err := c.Create(context.Background(), "job-1", spec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := dyn.Resource(gvr).Namespace("policy-test").Get(context.Background(), "job-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get back: %v", err)
	}
	pv, _, _ := unstructured.NestedString(got.Object, "spec", "policyVersionId")
	if pv != "pv-42" {
		t.Errorf("spec.policyVersionId: got %q want pv-42", pv)
	}
	sen, _, _ := unstructured.NestedString(got.Object, "spec", "sensitivity")
	if sen != "sensitive" {
		t.Errorf("spec.sensitivity: got %q want sensitive", sen)
	}
	if got.GetLabels()["app.kubernetes.io/managed-by"] != "delivery" {
		t.Errorf("missing managed-by label")
	}
}

func TestReadStatus(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"phase":     "Succeeded",
			"outputURL": "s3://policy-artifacts/artifacts/pv-42/job-1.pdf",
			"error":     "",
		},
	}}
	s := ReadStatus(obj)
	if s.Phase != PhaseSucceeded {
		t.Errorf("phase: got %q want Succeeded", s.Phase)
	}
	if s.OutputURL != "s3://policy-artifacts/artifacts/pv-42/job-1.pdf" {
		t.Errorf("outputURL: got %q", s.OutputURL)
	}
}

type stubProjector struct {
	processing []string
	done       map[string]string
	failed     map[string]string
}

func (s *stubProjector) MarkProcessing(_ context.Context, jobID string) error {
	s.processing = append(s.processing, jobID)
	return nil
}
func (s *stubProjector) MarkDone(_ context.Context, jobID, key string) error {
	if s.done == nil {
		s.done = map[string]string{}
	}
	s.done[jobID] = key
	return nil
}
func (s *stubProjector) MarkFailed(_ context.Context, jobID, msg string) error {
	if s.failed == nil {
		s.failed = map[string]string{}
	}
	s.failed[jobID] = msg
	return nil
}

func TestInformer_handle_phaseMapping(t *testing.T) {
	proj := &stubProjector{}
	i := &Informer{project: proj, onError: func(error) {}}

	// Running → MarkProcessing
	i.handle(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "j-run"},
		"spec":     map[string]any{"outputKey": "k"},
		"status":   map[string]any{"phase": "Running"},
	}})
	if len(proj.processing) != 1 || proj.processing[0] != "j-run" {
		t.Errorf("processing: %v", proj.processing)
	}

	// Succeeded → MarkDone with spec.outputKey
	i.handle(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "j-ok"},
		"spec":     map[string]any{"outputKey": "artifacts/pv/ok.pdf"},
		"status":   map[string]any{"phase": "Succeeded", "outputURL": "s3://b/artifacts/pv/ok.pdf"},
	}})
	if proj.done["j-ok"] != "artifacts/pv/ok.pdf" {
		t.Errorf("done: %v", proj.done)
	}

	// Failed → MarkFailed with status.error
	i.handle(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "j-bad"},
		"spec":     map[string]any{},
		"status":   map[string]any{"phase": "Failed", "error": "Chromium crashed"},
	}})
	if proj.failed["j-bad"] != "Chromium crashed" {
		t.Errorf("failed: %v", proj.failed)
	}

	// Empty phase → no-op
	procBefore := len(proj.processing)
	doneBefore := len(proj.done)
	i.handle(&unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "j-fresh"},
		"spec":     map[string]any{},
	}})
	if len(proj.processing) != procBefore || len(proj.done) != doneBefore {
		t.Errorf("empty phase should be no-op")
	}
}
