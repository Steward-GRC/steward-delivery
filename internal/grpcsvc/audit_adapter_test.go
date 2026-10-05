// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc

import (
	"context"
	"testing"

	"github.com/Steward-GRC/steward-delivery/internal/audit"
)

// capturePublisher records published audit messages so the adapter's
// tier-routing and attribute mapping can be asserted without RabbitMQ.
type capturePublisher struct {
	routingKey string
	event      audit.Event
}

func (c *capturePublisher) Publish(_ context.Context, routingKey string, body []byte) error {
	c.routingKey = routingKey
	ev, err := audit.Decode(body)
	c.event = ev
	return err
}

func TestAuditAdapterRoutesCreatedToAuditTier(t *testing.T) {
	cap := &capturePublisher{}
	a := newAuditAdapter(audit.New(cap))

	if err := a.Emit(context.Background(), "magic_link.created", "policy_version:pv-1", "user-1", map[string]string{
		"link":      "3f2a9c41aa00bb11",
		"sensitive": "true",
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if cap.routingKey != "audit.audit" {
		t.Errorf("routingKey: got %q want audit.audit", cap.routingKey)
	}
	if cap.event.Action != "magic_link.created" {
		t.Errorf("action: got %q", cap.event.Action)
	}
	if cap.event.ActorUserID != "user-1" {
		t.Errorf("actor: got %q", cap.event.ActorUserID)
	}
	if cap.event.Attributes["sensitive"] != "true" {
		t.Error("expected the sensitive attribute carried")
	}
	if cap.event.Attributes["link"] != "3f2a9c41aa00bb11" {
		t.Errorf("attrs not carried: %+v", cap.event.Attributes)
	}
}

func TestAuditAdapterRoutesAccessedToActivityTier(t *testing.T) {
	cap := &capturePublisher{}
	a := newAuditAdapter(audit.New(cap))

	if err := a.Emit(context.Background(), "magic_link.accessed", "policy_version:pv-1", "", map[string]string{
		"sensitive": "false",
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if cap.routingKey != "audit.activity" {
		t.Errorf("routingKey: got %q want audit.activity", cap.routingKey)
	}
	if cap.event.Tier != audit.TierActivity {
		t.Errorf("tier: got %q", cap.event.Tier)
	}
}

func TestAuditAdapterNilEmitterIsNoop(t *testing.T) {
	a := newAuditAdapter(nil)
	if err := a.Emit(context.Background(), "magic_link.revoked", "magic_link:x", "user-1", nil); err != nil {
		t.Fatalf("nil-emitter Emit should be no-op, got %v", err)
	}
}
