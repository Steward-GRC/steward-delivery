// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package corepolicy is delivery's client of steward-core: a version's
// Lexical content, the section diff between two versions, a version's
// appendices and its policy's sensitivity. The handlers depend on their own
// narrow interfaces; this package adapts core's generated clients to them.
package corepolicy

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	corev1 "github.com/Steward-GRC/steward-delivery/gen/go/thirdparty/core/v1"
	"github.com/Steward-GRC/steward-delivery/internal/policyhttp"
)

// AppendixService is the part of core's AppendixService this package calls.
type AppendixService interface {
	ListAppendices(ctx context.Context, in *corev1.ListAppendicesRequest, opts ...grpc.CallOption) (*corev1.ListAppendicesResponse, error)
}

// AppendixClient adapts core's AppendixService to policyhttp.AppendixClient.
type AppendixClient struct {
	svc AppendixService
}

// NewAppendixClient wraps svc.
func NewAppendixClient(svc AppendixService) *AppendixClient {
	return &AppendixClient{svc: svc}
}

// ListAppendices returns a version's appendices in core's order.
func (c *AppendixClient) ListAppendices(ctx context.Context, policyVersionID string) ([]policyhttp.AppendixMeta, error) {
	resp, err := c.svc.ListAppendices(ctx, &corev1.ListAppendicesRequest{PolicyVersionId: policyVersionID})
	if err != nil {
		return nil, fmt.Errorf("core ListAppendices %q: %w", policyVersionID, err)
	}
	appendices := resp.GetAppendices()
	out := make([]policyhttp.AppendixMeta, 0, len(appendices))
	for _, a := range appendices {
		out = append(out, policyhttp.AppendixMeta{
			Title:       a.GetTitle(),
			ContentJSON: []byte(a.GetContentJson()),
			OrderIndex:  a.GetOrderIndex(),
		})
	}
	return out, nil
}

// PolicyService is the part of core's PolicyService the content client calls.
type PolicyService interface {
	GetPolicyVersion(ctx context.Context, in *corev1.GetPolicyVersionRequest, opts ...grpc.CallOption) (*corev1.GetPolicyVersionResponse, error)
	DiffVersions(ctx context.Context, in *corev1.DiffVersionsRequest, opts ...grpc.CallOption) (*corev1.DiffVersionsResponse, error)
}

// Client adapts core's PolicyService to the GetContent and GetDiff the
// handlers use.
type Client struct {
	core PolicyService
}

// New wraps core.
func New(core PolicyService) *Client {
	return &Client{core: core}
}

// GetContent returns a version's content_json as core stored it. An unknown
// version wraps policyhttp.ErrNotFound, so the HTML endpoint answers 404 and
// the renderer fails the job instead of retrying.
func (c *Client) GetContent(ctx context.Context, policyVersionID string) ([]byte, error) {
	resp, err := c.core.GetPolicyVersion(ctx, &corev1.GetPolicyVersionRequest{Id: policyVersionID})
	if status.Code(err) == codes.NotFound {
		return nil, fmt.Errorf("policy version %q: %w", policyVersionID, policyhttp.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("core GetPolicyVersion %q: %w", policyVersionID, err)
	}
	v := resp.GetVersion()
	if v == nil {
		return nil, fmt.Errorf("policy version %q: %w", policyVersionID, policyhttp.ErrNotFound)
	}
	return []byte(v.GetContentJson()), nil
}

// GetDiff maps core's DiffVersions onto delivery's SectionDiff. Core's
// section title has no field here: the viewer takes titles from the rendered
// content.
func (c *Client) GetDiff(ctx context.Context, fromID, toID string) ([]*deliveryv1.SectionDiff, error) {
	resp, err := c.core.DiffVersions(ctx, &corev1.DiffVersionsRequest{
		FromVersionId: fromID,
		ToVersionId:   toID,
	})
	if err != nil {
		return nil, fmt.Errorf("core DiffVersions %q->%q: %w", fromID, toID, err)
	}
	coreDiffs := resp.GetDiffs()
	out := make([]*deliveryv1.SectionDiff, 0, len(coreDiffs))
	for _, d := range coreDiffs {
		out = append(out, &deliveryv1.SectionDiff{
			SectionKey:  d.GetSectionKey(),
			ChangeType:  d.GetChangeType(),
			DiffHtml:    d.GetWordDiffHtml(),
			Boilerplate: d.GetIsBoilerplate(),
		})
	}
	return out, nil
}

// SensitivityService is the part of core's PolicyService the sensitivity
// lookup calls.
type SensitivityService interface {
	GetPolicyVersion(ctx context.Context, in *corev1.GetPolicyVersionRequest, opts ...grpc.CallOption) (*corev1.GetPolicyVersionResponse, error)
	GetPolicy(ctx context.Context, in *corev1.GetPolicyRequest, opts ...grpc.CallOption) (*corev1.GetPolicyResponse, error)
}

// Sensitivity reads whether a version's policy is classified sensitive.
type Sensitivity struct {
	core SensitivityService
}

// NewSensitivity wraps core.
func NewSensitivity(core SensitivityService) *Sensitivity { return &Sensitivity{core: core} }

// Sensitive reports whether the policy the version belongs to is sensitive.
func (s *Sensitivity) Sensitive(ctx context.Context, policyVersionID string) (bool, error) {
	vr, err := s.core.GetPolicyVersion(ctx, &corev1.GetPolicyVersionRequest{Id: policyVersionID})
	if err != nil {
		return false, fmt.Errorf("core GetPolicyVersion %q: %w", policyVersionID, err)
	}
	pr, err := s.core.GetPolicy(ctx, &corev1.GetPolicyRequest{Id: vr.GetVersion().GetPolicyId()})
	if err != nil {
		return false, fmt.Errorf("core GetPolicy for version %q: %w", policyVersionID, err)
	}
	return pr.GetPolicy().GetSensitivity() == corev1.Sensitivity_SENSITIVITY_SENSITIVE, nil
}
