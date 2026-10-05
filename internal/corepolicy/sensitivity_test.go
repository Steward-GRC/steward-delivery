// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package corepolicy_test

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	corev1 "github.com/Steward-GRC/steward-delivery/gen/go/thirdparty/core/v1"
	"github.com/Steward-GRC/steward-delivery/internal/corepolicy"
	"github.com/Steward-GRC/steward-delivery/internal/policyhttp"
)

type fakeSensitivityCore struct {
	sensitivity corev1.Sensitivity
	gotPolicyID string
}

func (f *fakeSensitivityCore) GetPolicyVersion(_ context.Context, in *corev1.GetPolicyVersionRequest, _ ...grpc.CallOption) (*corev1.GetPolicyVersionResponse, error) {
	return &corev1.GetPolicyVersionResponse{Version: &corev1.PolicyVersion{Id: in.GetId(), PolicyId: "pol-" + in.GetId()}}, nil
}

func (f *fakeSensitivityCore) GetPolicy(_ context.Context, in *corev1.GetPolicyRequest, _ ...grpc.CallOption) (*corev1.GetPolicyResponse, error) {
	f.gotPolicyID = in.GetId()
	return &corev1.GetPolicyResponse{Policy: &corev1.Policy{Id: in.GetId(), Sensitivity: f.sensitivity}}, nil
}

func TestSensitiveReadsThePolicyOfTheVersion(t *testing.T) {
	core := &fakeSensitivityCore{sensitivity: corev1.Sensitivity_SENSITIVITY_SENSITIVE}
	got, err := corepolicy.NewSensitivity(core).Sensitive(context.Background(), "pv-1")
	if err != nil {
		t.Fatalf("Sensitive: %v", err)
	}
	if !got {
		t.Error("a SENSITIVE policy must report sensitive")
	}
	if core.gotPolicyID != "pol-pv-1" {
		t.Errorf("GetPolicy id: got %q", core.gotPolicyID)
	}
}

func TestStandardPolicyIsNotSensitive(t *testing.T) {
	core := &fakeSensitivityCore{sensitivity: corev1.Sensitivity_SENSITIVITY_STANDARD}
	got, err := corepolicy.NewSensitivity(core).Sensitive(context.Background(), "pv-1")
	if err != nil || got {
		t.Fatalf("got %v, %v; want false, nil", got, err)
	}
}

func TestGetContentCoreNotFoundIsNotFound(t *testing.T) {
	core := &fakeCore{getErr: status.Error(codes.NotFound, "no such version")}
	_, err := corepolicy.New(core).GetContent(context.Background(), "missing")
	if !errors.Is(err, policyhttp.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
