// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package grpcsvc

import (
	"context"

	"github.com/Steward-GRC/steward-delivery/internal/audit"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
)

// auditAdapter publishes the magic-link service's events as steward-audit
// AuditEvents: created and revoked on the audit tier, accessed (a viewer
// opening a link, not an admin change) on the activity tier. A nil emitter
// makes Emit a no-op.
type auditAdapter struct {
	emitter *audit.Emitter
}

func newAuditAdapter(emitter *audit.Emitter) *auditAdapter {
	return &auditAdapter{emitter: emitter}
}

// Emit publishes one event; the caller ignores its error.
func (a *auditAdapter) Emit(ctx context.Context, action, subject, actorUserID string, attrs map[string]string) error {
	if a == nil || a.emitter == nil {
		return nil
	}
	tier := audit.TierAudit
	if action == "magic_link.accessed" {
		tier = audit.TierActivity
	}
	return a.emitter.Emit(ctx, audit.Event{
		Tier:        tier,
		Action:      action,
		Subject:     subject,
		ActorUserID: actorUserID,
		Attributes:  attrs,
	})
}

// NewAuditEmitter returns the magic-link service's emitter on e.
func NewAuditEmitter(e *audit.Emitter) magiclink.AuditEmitter { return newAuditAdapter(e) }
