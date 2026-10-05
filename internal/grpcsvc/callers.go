// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc

import (
	"context"

	log "github.com/Bugs5382/go-log"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/audit"
	"github.com/Steward-GRC/steward-delivery/internal/workloadauth"
)

// CallerGateway is the only gRPC caller, from the service account
// steward-gateway. The PDF renderer fetches HTML over the internal HTTP port,
// not gRPC.
const CallerGateway = "gateway"

// CallerPolicy is delivery's per-method allow-list: the gateway, on behalf of
// the signed-in user, on every method. Anything else is refused.
func CallerPolicy() workloadauth.Policy {
	p := workloadauth.Policy{}
	sd := deliveryv1.DeliveryService_ServiceDesc
	for _, md := range sd.Methods {
		p["/"+sd.ServiceName+"/"+md.MethodName] = map[string]workloadauth.Access{CallerGateway: workloadauth.OnBehalf}
	}
	return p
}

type eventEmitter interface {
	Emit(ctx context.Context, ev audit.Event) error
}

// AuditDenial records a call the workload-auth interceptor refused, as
// rpc.denied in the audit tier. The actor is the authenticated caller (or
// "unauthenticated"), never a user the call claimed.
func AuditDenial(emitter eventEmitter, lg log.Logger) workloadauth.DenyHook {
	return func(ctx context.Context, d workloadauth.Denial) {
		caller := d.Caller.Name
		if caller == "" {
			caller = "unauthenticated"
		}
		err := emitter.Emit(ctx, audit.Event{
			Tier: audit.TierAudit, Action: "rpc.denied", ActorUserID: "service:" + caller, Subject: d.Method,
			Attributes: map[string]string{
				"method": d.Method, "caller": d.Caller.Name, "service_account": d.Caller.ServiceAccount,
				"code": d.Code.String(), "reason": d.Reason,
			},
		})
		if err != nil {
			lg.Ctx(ctx).Error(err, "audit of a refused call failed", log.F("method", d.Method), log.F("caller", caller))
		}
	}
}
