// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package policyhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type stubHistory struct {
	entries   []HistoryEntry
	requested bool
	err       error
	gotJob    string
	gotParams [2]string
}

func (s *stubHistory) JobHistory(_ context.Context, jobID, versionID string) ([]HistoryEntry, bool, error) {
	s.gotParams = [2]string{jobID, versionID}
	s.gotJob = jobID
	return s.entries, s.requested, s.err
}

func serveHistory(t *testing.T, h *Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestHistoryPageFollowsTheBodyInTheGivenOrder(t *testing.T) {
	hist := &stubHistory{requested: true, entries: []HistoryEntry{
		{At: time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC), Version: 2, Label: "Submitted for review", Actor: "Dana Reyes"},
		{At: time.Date(2026, 3, 4, 16, 5, 0, 0, time.UTC), Version: 2, Label: "Approved", Actor: "Lee Park"},
		{At: time.Date(2026, 3, 5, 8, 0, 0, 0, time.UTC), Version: 2, Label: "Published", Actor: ""},
	}}
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithHistory(hist)

	rec := serveHistory(t, h, "/internal/policies/pv-42/html?job=pdf-pv-42-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if hist.gotParams != [2]string{"pdf-pv-42-1", "pv-42"} {
		t.Errorf("history asked for %v", hist.gotParams)
	}
	section := strings.Index(body, `<section class="policy-history"`)
	if section < 0 {
		t.Fatalf("no history section in %s", body)
	}
	if strings.Index(body, `</article>`) > section {
		t.Error("history section should come after the policy body")
	}
	for _, want := range []string{"Version history", "2 Mar 2026", "Submitted for review", "Dana Reyes", "Lee Park", "Published"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %s", want, body)
		}
	}
	if strings.Index(body, "Submitted for review") > strings.Index(body, "Approved") ||
		strings.Index(body, "Approved") > strings.Index(body, "Published") {
		t.Error("entries are not in the order given")
	}
}

func TestHistoryPageEscapesNamesAndLabels(t *testing.T) {
	hist := &stubHistory{requested: true, entries: []HistoryEntry{
		{At: time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC), Version: 1, Label: `<b>x</b>`, Actor: `<script>alert(1)</script>`},
	}}
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithHistory(hist)

	body := serveHistory(t, h, "/internal/policies/pv-42/html?job=j1").Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "<b>x</b>") {
		t.Errorf("unescaped markup in %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("expected escaped actor in %s", body)
	}
}

func TestHistoryPageSaysSoWhenNoEventIsRecorded(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithHistory(&stubHistory{requested: true})

	body := serveHistory(t, h, "/internal/policies/pv-42/html?job=j1").Body.String()
	if !strings.Contains(body, `<section class="policy-history"`) || !strings.Contains(body, "No history is recorded for this version.") {
		t.Errorf("expected the empty notice in %s", body)
	}
}

func TestNoHistoryPageWhenTheJobDidNotAskForIt(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithHistory(&stubHistory{requested: false})

	body := serveHistory(t, h, "/internal/policies/pv-42/html?job=j1").Body.String()
	if strings.Contains(body, "policy-history") {
		t.Errorf("unexpected history in %s", body)
	}
}

func TestNoHistoryLookupWithoutAJobParameter(t *testing.T) {
	hist := &stubHistory{requested: true, entries: []HistoryEntry{{Label: "Approved"}}}
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithHistory(hist)

	body := serveHistory(t, h, "/internal/policies/pv-42/html").Body.String()
	if hist.gotJob != "" || strings.Contains(body, "policy-history") {
		t.Errorf("history looked up or printed without a job: %q %s", hist.gotJob, body)
	}
}

func TestHistoryOfAnotherVersionsJobIsNotFound(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithHistory(&stubHistory{err: ErrNotFound})

	if rec := serveHistory(t, h, "/internal/policies/pv-42/html?job=other"); rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestHistoryStoreFailureIsBadGateway(t *testing.T) {
	h := New(stubPolicy{content: []byte(minimalLexical)}, nil).WithHistory(&stubHistory{err: context.DeadlineExceeded})

	if rec := serveHistory(t, h, "/internal/policies/pv-42/html?job=j1"); rec.Code != http.StatusBadGateway {
		t.Errorf("status %d, want 502", rec.Code)
	}
}
