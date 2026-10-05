// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package pdfrender is delivery's side of the PdfRender resource: it creates
// one per export request and projects the renderer's status back into the
// pdf_jobs row through an informer.
//
// steward-pdf-renderer owns the CRD; docs/pdf-render.md is the contract.
// The resource is handled as unstructured through the dynamic client: the
// surface is a few spec and status fields, and it keeps controller-runtime
// out of this module.
package pdfrender

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// GVR is the renders.steward-grc.com/v1alpha1 pdfrenders resource.
var GVR = schema.GroupVersionResource{
	Group:    "renders.steward-grc.com",
	Version:  "v1alpha1",
	Resource: "pdfrenders",
}

// Kind is the resource's kind.
const Kind = "PdfRender"

// Spec is what delivery asks the renderer for. The field names on the wire
// are in docs/pdf-render.md.
type Spec struct {
	PolicyVersionID string
	FetchURL        string
	OutputBucket    string
	OutputKey       string
	Sensitivity     string // "standard" or "sensitive"
	RequestedBy     string
	Trace           string
}

// Phase is status.phase. Empty means the renderer hasn't seen the resource
// yet, which is not the same as Pending.
type Phase string

// The phases the renderer reports.
const (
	PhasePending   Phase = "Pending"
	PhaseRunning   Phase = "Running"
	PhaseSucceeded Phase = "Succeeded"
	PhaseFailed    Phase = "Failed"
)

// Status is the part of the status delivery reads.
type Status struct {
	Phase     Phase
	OutputURL string
	Error     string
}

// Client creates PdfRender resources in one namespace.
type Client struct {
	dyn       dynamic.Interface
	namespace string
}

// NewClient returns a Client for namespace.
func NewClient(dyn dynamic.Interface, namespace string) *Client {
	return &Client{dyn: dyn, namespace: namespace}
}

// Create creates the resource named name, the job id, so the informer can
// match its status to the pdf_jobs row.
func (c *Client) Create(ctx context.Context, name string, spec Spec) error {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{Group: GVR.Group, Version: GVR.Version, Kind: Kind})
	obj.SetName(name)
	obj.SetNamespace(c.namespace)
	// Delivery owns these resources even though the renderer reconciles them.
	obj.SetLabels(map[string]string{
		"app.kubernetes.io/managed-by": "delivery",
		"app.kubernetes.io/part-of":    "steward",
	})
	specMap := map[string]any{
		"policyVersionId": spec.PolicyVersionID,
		"fetchURL":        spec.FetchURL,
		"outputBucket":    spec.OutputBucket,
		"outputKey":       spec.OutputKey,
	}
	if spec.Sensitivity != "" {
		specMap["sensitivity"] = spec.Sensitivity
	}
	if spec.RequestedBy != "" {
		specMap["requestedBy"] = spec.RequestedBy
	}
	if spec.Trace != "" {
		specMap["trace"] = spec.Trace
	}
	if err := unstructured.SetNestedMap(obj.Object, specMap, "spec"); err != nil {
		return fmt.Errorf("pdfrender: set spec: %w", err)
	}
	if _, err := c.dyn.Resource(GVR).Namespace(c.namespace).Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("pdfrender: create %s/%s: %w", c.namespace, name, err)
	}
	return nil
}

// Ping lists at most one resource: it fails while the API server, the CRD or
// the service account's access to it is missing.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.dyn.Resource(GVR).Namespace(c.namespace).List(ctx, metav1.ListOptions{Limit: 1})
	return err
}

// ReadStatus reads the status; a new resource has none yet.
func ReadStatus(obj *unstructured.Unstructured) Status {
	if obj == nil {
		return Status{}
	}
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	url, _, _ := unstructured.NestedString(obj.Object, "status", "outputURL")
	errMsg, _, _ := unstructured.NestedString(obj.Object, "status", "error")
	return Status{Phase: Phase(phase), OutputURL: url, Error: errMsg}
}
