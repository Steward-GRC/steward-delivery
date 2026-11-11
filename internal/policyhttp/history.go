// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package policyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/store"
)

// HistoryEntry is one line of the version-history page. The gateway sends the
// entries with an export request, with names and wording already resolved.
type HistoryEntry struct {
	At      time.Time `json:"at"`
	Version int32     `json:"version"`
	Label   string    `json:"label"`
	Actor   string    `json:"actor"`
}

// HistoryClient returns the history kept with an export job. requested is
// false when the job did not ask for a history page. A job that is unknown, or
// belongs to another version, wraps ErrNotFound.
type HistoryClient interface {
	JobHistory(ctx context.Context, jobID, versionID string) (entries []HistoryEntry, requested bool, err error)
}

// MarshalHistory is the form a job's history is stored in. A nil list is
// stored as an empty one, so "asked for, nothing recorded" differs from "not
// asked for" (no stored value).
func MarshalHistory(entries []HistoryEntry) ([]byte, error) {
	if entries == nil {
		entries = []HistoryEntry{}
	}
	return json.Marshal(entries)
}

// JobStore reads the version id and stored history of an export job;
// store.PDFJobRepo implements it. No stored history means the export did not ask for the page.
type JobStore interface {
	GetJobHistory(ctx context.Context, jobID string) (versionID string, history []byte, err error)
}

// JobHistory is the HistoryClient that reads from a JobStore.
type JobHistory struct{ jobs JobStore }

// NewJobHistory wraps jobs.
func NewJobHistory(jobs JobStore) *JobHistory { return &JobHistory{jobs: jobs} }

// JobHistory implements HistoryClient.
func (j *JobHistory) JobHistory(ctx context.Context, jobID, versionID string) ([]HistoryEntry, bool, error) {
	jobVersion, raw, err := j.jobs.GetJobHistory(ctx, jobID)
	if errors.Is(err, store.ErrPDFJobNotFound) {
		return nil, false, fmt.Errorf("pdf job %q: %w", jobID, ErrNotFound)
	}
	if err != nil {
		return nil, false, fmt.Errorf("read history of pdf job %q: %w", jobID, err)
	}
	if jobVersion != versionID {
		return nil, false, fmt.Errorf("pdf job %q is not for version %q: %w", jobID, versionID, ErrNotFound)
	}
	if raw == nil {
		return nil, false, nil
	}
	var entries []HistoryEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, false, fmt.Errorf("decode history of pdf job %q: %w", jobID, err)
	}
	return entries, true, nil
}

// historyHTML is the page after the policy: a table of when, which version,
// what and who, in the order given, or a notice when nothing is recorded.
// Every value is escaped.
func historyHTML(entries []HistoryEntry) string {
	var b strings.Builder
	b.WriteString(`<section class="policy-history" style="break-before: page">`)
	b.WriteString(`<h2>Version history</h2>`)
	if len(entries) == 0 {
		b.WriteString(`<p>No history is recorded for this version.</p></section>`)
		return b.String()
	}
	b.WriteString(`<table><thead><tr><th>Date</th><th>Version</th><th>Event</th><th>By</th></tr></thead><tbody>`)
	for _, e := range entries {
		b.WriteString(`<tr><td>`)
		b.WriteString(html.EscapeString(e.At.UTC().Format("2 Jan 2006 15:04 UTC")))
		b.WriteString(`</td><td>`)
		if e.Version > 0 {
			b.WriteString(strconv.Itoa(int(e.Version)))
		}
		b.WriteString(`</td><td>`)
		b.WriteString(html.EscapeString(e.Label))
		b.WriteString(`</td><td>`)
		b.WriteString(html.EscapeString(e.Actor))
		b.WriteString(`</td></tr>`)
	}
	b.WriteString(`</tbody></table></section>`)
	return b.String()
}
