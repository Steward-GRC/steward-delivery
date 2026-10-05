// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package magiclink issues, resolves and revokes magic links (read-only share
// links to one policy version): it mints the tokens, picks the expiry by
// sensitivity and emits the audit and activity events. Events never carry a
// token, only its Fingerprint.
package magiclink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/store"
)

// The default lifetimes: 30 days, and 48 hours for a sensitive link.
const (
	defaultExpiry   = 30 * 24 * time.Hour
	sensitiveExpiry = 48 * time.Hour
)

// Repo stores the links; store.MagicLinkRepo implements it.
type Repo interface {
	Create(ctx context.Context, ml store.MagicLink) error
	Resolve(ctx context.Context, token string) (store.MagicLink, error)
	Revoke(ctx context.Context, token, revokedByUserID string) error
}

// AuditEmitter records the audit and activity events. Emission is best
// effort: an emitter error never fails the operation.
type AuditEmitter interface {
	Emit(ctx context.Context, action, subject, actorUserID string, attrs map[string]string) error
}

// Service is the magic-link service.
type Service struct {
	repo         Repo
	emitter      AuditEmitter
	ttl, ttlSens time.Duration
}

// Option configures a Service.
type Option func(*Service)

// WithTTLs sets the lifetime of a link and of a sensitive link.
func WithTTLs(standard, sensitive time.Duration) Option {
	return func(s *Service) { s.ttl, s.ttlSens = standard, sensitive }
}

// NewService returns a Service on repo and emitter.
func NewService(repo Repo, emitter AuditEmitter, opts ...Option) *Service {
	s := &Service{repo: repo, emitter: emitter, ttl: defaultExpiry, ttlSens: sensitiveExpiry}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Fingerprint names a token in events and logs without revealing it: the
// first 16 hex characters of its SHA-256.
func Fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// CreateRequest is Create's input.
type CreateRequest struct {
	PolicyVersionID string
	CreatedByUserID string
	Sensitive       bool
}

// CreateResponse is the output of Service.Create.
type CreateResponse struct {
	Token     string
	ExpiresAt time.Time
}

// Create mints and stores a link and emits magic_link.created. A sensitive
// link gets the shorter lifetime.
func (s *Service) Create(ctx context.Context, req CreateRequest) (CreateResponse, error) {
	token, err := generateToken()
	if err != nil {
		return CreateResponse{}, fmt.Errorf("generate token: %w", err)
	}

	expiry := s.ttl
	if req.Sensitive {
		expiry = s.ttlSens
	}
	expiresAt := time.Now().Add(expiry).UTC()

	ml := store.MagicLink{
		Token:           token,
		PolicyVersionID: req.PolicyVersionID,
		CreatedByUserID: req.CreatedByUserID,
		Sensitive:       req.Sensitive,
		ExpiresAt:       expiresAt,
	}
	if err := s.repo.Create(ctx, ml); err != nil {
		return CreateResponse{}, fmt.Errorf("persist token: %w", err)
	}

	_ = s.emitter.Emit(ctx, "magic_link.created", "policy_version:"+req.PolicyVersionID, req.CreatedByUserID, map[string]string{
		"link":      Fingerprint(token),
		"sensitive": fmt.Sprintf("%v", req.Sensitive),
	})

	return CreateResponse{Token: token, ExpiresAt: expiresAt}, nil
}

// ResolveRequest is the input for Service.Resolve.
type ResolveRequest struct{ Token, ViewerEmail string }

// ResolveResponse is the output of Service.Resolve.
type ResolveResponse struct {
	PolicyVersionID   string
	Sensitive         bool
	WatermarkRequired bool
}

// Resolve returns the version a live link opens and emits
// magic_link.accessed. A sensitive link always requires the watermark.
func (s *Service) Resolve(ctx context.Context, req ResolveRequest) (ResolveResponse, error) {
	ml, err := s.repo.Resolve(ctx, req.Token)
	if err != nil {
		return ResolveResponse{}, err
	}

	_ = s.emitter.Emit(ctx, "magic_link.accessed", "policy_version:"+ml.PolicyVersionID, "", map[string]string{
		"link":         Fingerprint(req.Token),
		"viewer_email": req.ViewerEmail,
		"sensitive":    fmt.Sprintf("%v", ml.Sensitive),
	})

	return ResolveResponse{
		PolicyVersionID:   ml.PolicyVersionID,
		Sensitive:         ml.Sensitive,
		WatermarkRequired: ml.Sensitive,
	}, nil
}

// RevokeRequest is the input for Service.Revoke.
type RevokeRequest struct{ Token, RevokedByUserID string }

// Revoke ends a link and emits magic_link.revoked. Revoking twice succeeds.
func (s *Service) Revoke(ctx context.Context, req RevokeRequest) error {
	if err := s.repo.Revoke(ctx, req.Token, req.RevokedByUserID); err != nil {
		return err
	}
	_ = s.emitter.Emit(ctx, "magic_link.revoked", "magic_link:"+Fingerprint(req.Token), req.RevokedByUserID, nil)
	return nil
}

// generateToken returns 32 random bytes as 64 hex characters, URL-safe
// without padding.
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
