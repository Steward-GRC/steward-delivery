// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc_test

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/grpcsvc"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/pdfrender"
)

// TestPDFExportCreatesPdfRender verifies that RequestPDFExport creates a
// PdfRender CRD with the right spec (FetchURL pointing back at delivery's
// internal HTTP, deterministic OutputKey, sensitivity derived from policy
// content). Uses the fake dynamic client — no live API server needed.
func TestPDFExportCreatesPdfRender(t *testing.T) {
	scheme := runtime.NewScheme()
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		pdfrender.GVR: "PdfRenderList",
	})
	client := pdfrender.NewClient(dyn, "policy-test")
	cfg := grpcsvc.PDFConfig{
		FetchURLBase: "http://delivery.example.org:8081",
		OutputBucket: "policy-artifacts",
	}

	mlSvc := magiclink.NewService(newFakeMLRepo(), &noopEmitter{})
	handler := grpcsvc.NewDeliveryHandlerWithPDF(&sensitiveStubPolicyClient{}, mlSvc, client, cfg)

	ctx := context.Background()
	resp, err := handler.RequestPDFExport(ctx, &deliveryv1.RequestPDFExportRequest{
		PolicyVersionId: "pv-mq-001",
		RequesterUserId: "user-001",
	})
	if err != nil {
		t.Fatalf("RequestPDFExport: %v", err)
	}
	if resp.JobId == "" {
		t.Fatal("expected non-empty job ID")
	}

	// Verify the PdfRender resource was created in the right namespace
	// with the expected spec fields.
	got, err := dyn.Resource(pdfrender.GVR).Namespace("policy-test").Get(ctx, resp.JobId, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get PdfRender %s: %v", resp.JobId, err)
	}
	if pv, _, _ := unstructured.NestedString(got.Object, "spec", "policyVersionId"); pv != "pv-mq-001" {
		t.Errorf("spec.policyVersionId: got %q want pv-mq-001", pv)
	}
	if url, _, _ := unstructured.NestedString(got.Object, "spec", "fetchURL"); url != "http://delivery.example.org:8081/internal/policies/pv-mq-001/html" {
		t.Errorf("spec.fetchURL: got %q", url)
	}
	if bucket, _, _ := unstructured.NestedString(got.Object, "spec", "outputBucket"); bucket != "policy-artifacts" {
		t.Errorf("spec.outputBucket: got %q", bucket)
	}
	wantKey := "artifacts/pv-mq-001/" + resp.JobId + ".pdf"
	if key, _, _ := unstructured.NestedString(got.Object, "spec", "outputKey"); key != wantKey {
		t.Errorf("spec.outputKey: got %q want %q", key, wantKey)
	}
	// sensitiveStubPolicyClient returns sensitivity:true so we expect
	// the watermark-bearing variant.
	if sen, _, _ := unstructured.NestedString(got.Object, "spec", "sensitivity"); sen != "sensitive" {
		t.Errorf("spec.sensitivity: got %q want sensitive", sen)
	}
	if rb, _, _ := unstructured.NestedString(got.Object, "spec", "requestedBy"); rb != "user-001" {
		t.Errorf("spec.requestedBy: got %q want user-001", rb)
	}
}

// sensitiveStubPolicyClient returns a doc whose top-level "sensitivity"
// flag is true, so RequestPDFExport stamps spec.sensitivity=sensitive.
type sensitiveStubPolicyClient struct{}

func (sensitiveStubPolicyClient) GetContent(_ context.Context, _ string) ([]byte, error) {
	return []byte(`{"root":{"type":"root","children":[]},"sensitivity":true}`), nil
}
func (sensitiveStubPolicyClient) GetDiff(_ context.Context, _, _ string) ([]*deliveryv1.SectionDiff, error) {
	return nil, nil
}
