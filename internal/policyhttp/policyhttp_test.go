// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package policyhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stubPolicy struct {
	content []byte
	err     error
}

func (s stubPolicy) GetContent(_ context.Context, _ string) ([]byte, error) {
	return s.content, s.err
}

// Minimal valid Lexical JSON that render.Renderer happily produces HTML from.
// A single empty paragraph node is enough.
const minimalLexical = `{"root":{"type":"root","format":"","indent":0,"version":1,"children":[{"type":"paragraph","format":"","indent":0,"version":1,"children":[]}]}}`

func TestServeHTML_OK(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil)
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/internal/policies/pv-42/html", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type: got %q, want text/html...", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "<") {
		t.Errorf("body looks empty / unrendered: %q", body)
	}
}

func TestServeHTML_MissingVersionID(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil)
	mux := http.NewServeMux()
	h.Mount(mux)

	// Trailing slash with empty path segment doesn't match the route. Use a
	// direct invocation of the handler with an empty PathValue to exercise
	// the validation branch.
	req := httptest.NewRequest(http.MethodGet, "/internal/policies//html", nil)
	req.SetPathValue("versionId", "")
	rec := httptest.NewRecorder()
	h.serveHTML(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
}

func TestServeHTML_NotFound(t *testing.T) {
	h := New(stubPolicy{err: ErrNotFound}, nil)
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/internal/policies/missing/html", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
}

func TestServeHTML_UpstreamError(t *testing.T) {
	h := New(stubPolicy{err: errors.New("boom")}, nil)
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/internal/policies/pv-42/html", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", rec.Code)
	}
}

type stubUpdated struct {
	at  time.Time
	err error
}

func (s stubUpdated) GetUpdatedAt(_ context.Context, _ string) (time.Time, error) {
	return s.at, s.err
}

func serve(t *testing.T, h *Handler) (int, string) {
	t.Helper()
	mux := http.NewServeMux()
	h.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal/policies/pv-42/html", nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body)
}

func TestServeHTML_LastUpdatedLineComesFirst(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).
		WithUpdated(stubUpdated{at: time.Date(2026, 10, 2, 14, 2, 0, 0, time.UTC)})
	code, body := serve(t, h)
	if code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", code)
	}
	const want = `<p class="policy-updated">Last updated 2 Oct 2026</p>`
	if !strings.HasPrefix(body, want) {
		t.Errorf("want the Last updated line first, got:\n%s", body)
	}
}

func TestServeHTML_NoDateNoLine(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithUpdated(stubUpdated{})
	_, body := serve(t, h)
	if strings.Contains(body, "Last updated") {
		t.Errorf("a version with no date should print no line, got:\n%s", body)
	}
}

func TestServeHTML_UpdatedLookupFailureIsBadGateway(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).
		WithUpdated(stubUpdated{err: errors.New("core down")})
	if code, _ := serve(t, h); code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", code)
	}
}
