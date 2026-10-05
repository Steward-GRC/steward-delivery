// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc

import (
	"context"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
)

// knownDeferred allowlists RPCs that are intentionally left unimplemented —
// falling through to the embedded Unimplemented*Server stub — because the
// feature backing them has not shipped yet. Every entry here must have a
// concrete reason; it is a deliberate, documented exception to the coverage
// guard below, never a silent gap.
//
// Key format: "<case-name>.<MethodName>" (case-name matches the table below).
var knownDeferred = map[string]string{}

// grpcCoverageCase pairs a generated *Server interface with a zero-value
// handler. Reflecting over the interface's methods and invoking each one
// against the zero-value handler proves every RPC has a real, explicit
// implementation that shadows the embedded Unimplemented*Server stub: a
// stub compiles fine and returns codes.Unimplemented at runtime, so `go
// build` alone can never catch a forgotten handler method.
type grpcCoverageCase struct {
	name          string
	interfaceType reflect.Type
	handler       any
}

func TestGRPCServersImplementEveryRPC(t *testing.T) {
	cases := []grpcCoverageCase{
		{
			name:          "DeliveryService",
			interfaceType: reflect.TypeFor[deliveryv1.DeliveryServiceServer](),
			handler:       &DeliveryHandler{},
		},
	}

	invoked := 0

	for _, tc := range cases {
		handlerVal := reflect.ValueOf(tc.handler)

		for method := range tc.interfaceType.Methods() {
			// Skip anything that isn't a plain unary RPC shape: this drops
			// streaming methods (different signature entirely) and the
			// generated mustEmbedUnimplemented*Server() marker method
			// (zero args, zero returns).
			if !isUnaryRPCShape(method.Type) {
				continue
			}

			key := tc.name + "." + method.Name
			reqType := method.Type.In(1).Elem()
			args := []reflect.Value{
				reflect.ValueOf(context.Background()),
				reflect.New(reqType),
			}

			invoked++
			t.Run(key, func(t *testing.T) {
				checkImplemented(t, handlerVal, method.Name, args, key)
			})
		}
	}

	// Sanity check: if this drops to zero, the reflection walk above is
	// broken (e.g. a contracts upgrade renamed/removed the interfaces) and
	// the guard is silently exercising nothing.
	if invoked == 0 {
		t.Fatal("no gRPC RPCs were exercised — coverage guard is not wired up")
	}
	t.Logf("exercised %d gRPC RPC(s) across %d service(s)", invoked, len(cases))
}

// isUnaryRPCShape reports whether m looks like a generated unary RPC method:
// func(context.Context, *Req) (*Resp, error). Interface-derived
// reflect.Type method signatures have no receiver argument, so a unary RPC
// has exactly two inputs and two outputs.
func isUnaryRPCShape(m reflect.Type) bool {
	if m.NumIn() != 2 || m.NumOut() != 2 {
		return false
	}

	ctxType := reflect.TypeFor[context.Context]()
	errType := reflect.TypeFor[error]()

	if !m.In(0).Implements(ctxType) {
		return false
	}
	if m.In(1).Kind() != reflect.Pointer {
		return false
	}
	if m.Out(0).Kind() != reflect.Pointer {
		return false
	}
	return m.Out(1) == errType
}

// checkImplemented invokes methodName on handlerVal with args. A panic is
// treated as proof of a real implementation: the handlers under test are
// constructed with zero-value (nil) dependencies, so any method that
// actually does work will panic on a nil dependency rather than return
// cleanly. The only failure case is a method that returns codes.Unimplemented
// — i.e. one that fell through to the embedded Unimplemented*Server stub —
// and isn't explicitly allowlisted in knownDeferred.
func checkImplemented(t *testing.T, handlerVal reflect.Value, methodName string, args []reflect.Value, key string) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Logf("%s panicked against a zero-value handler (treated as implemented): %v", key, r)
		}
	}()

	method := handlerVal.MethodByName(methodName)
	if !method.IsValid() {
		t.Fatalf("%s: handler value does not expose method %s", key, methodName)
	}

	out := method.Call(args)
	if len(out) != 2 {
		return
	}

	err, _ := out[1].Interface().(error)
	if err == nil {
		return
	}

	if status.Code(err) != codes.Unimplemented {
		return
	}

	if reason, ok := knownDeferred[key]; ok {
		t.Logf("%s: known-deferred RPC, not implemented (%s)", key, reason)
		return
	}

	t.Errorf("%s: RPC falls through to the embedded Unimplemented*Server stub (returns codes.Unimplemented) — implement it, or if it is intentionally deferred, add it to knownDeferred with a reason", key)
}
