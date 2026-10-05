// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package pdfrender

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The renderer's CRD is the other half of this contract (docs/pdf-render.md).
func TestTheResourceIsInTheProjectAPIGroup(t *testing.T) {
	if GVR.Group != "renders.steward-grc.com" || GVR.Version != "v1alpha1" || GVR.Resource != "pdfrenders" {
		t.Fatalf("GVR = %v", GVR)
	}
	dyn, _ := newFakeDyn(t)
	c := NewClient(dyn, "steward")
	if err := c.Create(context.Background(), "job-1", Spec{PolicyVersionID: "pv-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := dyn.Resource(GVR).Namespace("steward").Get(context.Background(), "job-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetKind() != Kind || got.GetAPIVersion() != "renders.steward-grc.com/v1alpha1" {
		t.Errorf("kind %q apiVersion %q", got.GetKind(), got.GetAPIVersion())
	}
	if got.GetLabels()["app.kubernetes.io/part-of"] != "steward" {
		t.Errorf("part-of label: %v", got.GetLabels())
	}
	if err := c.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}
