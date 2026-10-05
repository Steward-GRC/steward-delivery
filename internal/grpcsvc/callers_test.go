// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc

import (
	"context"
	"testing"

	log "github.com/Bugs5382/go-log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/audit"
	"github.com/Steward-GRC/steward-delivery/internal/workloadauth"
)

func deliveryMethods() []string {
	sd := deliveryv1.DeliveryService_ServiceDesc
	out := make([]string, 0, len(sd.Methods))
	for _, md := range sd.Methods {
		out = append(out, "/"+sd.ServiceName+"/"+md.MethodName)
	}
	return out
}

func TestCallerPolicyGatewayOnEveryMethod(t *testing.T) {
	p := CallerPolicy()
	require.Len(t, p, len(deliveryMethods()), "the policy lists only methods delivery serves")
	for _, m := range deliveryMethods() {
		require.Equal(t, map[string]workloadauth.Access{CallerGateway: workloadauth.OnBehalf}, p[m], m)
	}
}

func TestCallerPolicyRefusesEveryOtherCaller(t *testing.T) {
	p := CallerPolicy()
	for _, caller := range []string{"pdf-renderer", "core", "workflow", "reporting"} {
		for _, m := range deliveryMethods() {
			_, ok := p.Lookup(m, caller)
			require.False(t, ok, "%s on %s", caller, m)
		}
	}
}

type recordingEmitter struct{ evs []audit.Event }

func (r *recordingEmitter) Emit(_ context.Context, ev audit.Event) error {
	r.evs = append(r.evs, ev)
	return nil
}

func TestAuditDenialRecordsTheCallerNotAClaimedUser(t *testing.T) {
	rec := &recordingEmitter{}
	hook := AuditDenial(rec, log.Nop())
	m := deliveryv1.DeliveryService_GetDiff_FullMethodName
	hook(context.Background(), workloadauth.Denial{
		Method: m, Code: codes.PermissionDenied, Reason: workloadauth.ReasonMethodNotAllowed,
		Caller: workloadauth.Caller{Name: "reporting", ServiceAccount: "steward/steward-reporting"},
	})
	hook(context.Background(), workloadauth.Denial{Method: m, Code: codes.Unauthenticated, Reason: workloadauth.ReasonNoToken})
	require.Len(t, rec.evs, 2)
	require.Equal(t, audit.TierAudit, rec.evs[0].Tier)
	require.Equal(t, "rpc.denied", rec.evs[0].Action)
	require.Equal(t, "service:reporting", rec.evs[0].ActorUserID)
	require.Equal(t, m, rec.evs[0].Subject)
	require.Equal(t, map[string]string{
		"method": m, "caller": "reporting", "service_account": "steward/steward-reporting",
		"code": "PermissionDenied", "reason": workloadauth.ReasonMethodNotAllowed,
	}, rec.evs[0].Attributes)
	require.Equal(t, "service:unauthenticated", rec.evs[1].ActorUserID)
}
