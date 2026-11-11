// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package policyhttp serves the rendered policy HTML over plain HTTP for the
// steward-pdf-renderer job to fetch, at GET /internal/policies/{versionId}/html.
// The server puts it behind the workload-token check (server.HTTPAuth), which
// admits only steward-pdf-renderer, on its own port; a NetworkPolicy that
// admits the renderer's pods is defence in depth.
package policyhttp

import (
	"context"
	"errors"
	"html"
	"net/http"
	"strings"
	"time"

	log "github.com/Bugs5382/go-log"

	"github.com/Steward-GRC/steward-delivery/internal/render"
)

// PolicyClient fetches a version's content (grpcsvc.PolicyClient's GetContent).
type PolicyClient interface {
	GetContent(ctx context.Context, policyVersionID string) ([]byte, error)
}

// AppendixClient lists a version's appendices.
type AppendixClient interface {
	ListAppendices(ctx context.Context, policyVersionID string) ([]AppendixMeta, error)
}

// AppendixMeta carries the fields needed to render one appendix section.
type AppendixMeta struct {
	Title       string
	ContentJSON []byte
	OrderIndex  int32
}

// AppendixRender is one appendix, already rendered.
type AppendixRender struct {
	Letter   string
	Title    string
	BodyHTML string
}

// appendHTML adds each appendix after the body as a
// <section class="policy-appendix"> with an escaped <h2> title.
func appendHTML(bodyHTML string, appendices []AppendixRender) string {
	if len(appendices) == 0 {
		return bodyHTML
	}
	var b strings.Builder
	b.WriteString(bodyHTML)
	for _, a := range appendices {
		b.WriteString(`<section class="policy-appendix">`)
		b.WriteString(`<h2>Appendix `)
		b.WriteString(a.Letter)
		b.WriteString(`: `)
		b.WriteString(html.EscapeString(a.Title))
		b.WriteString(`</h2>`)
		b.WriteString(a.BodyHTML)
		b.WriteString(`</section>`)
	}
	return b.String()
}

// UpdatedClient says when a version last changed, for the "Last updated" line.
type UpdatedClient interface {
	GetUpdatedAt(ctx context.Context, policyVersionID string) (time.Time, error)
}

// updatedHTML is the "Last updated" line above the body; no line when the date
// is unknown.
func updatedHTML(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return `<p class="policy-updated">Last updated ` + at.UTC().Format("2 Jan 2006") + `</p>`
}

// Handler resolves PolicyVersionID to rendered HTML.
type Handler struct {
	updated  UpdatedClient
	policy   PolicyClient
	appendix AppendixClient
	history  HistoryClient
	renderer *render.Renderer
	logger   log.Logger
}

// New returns a Handler with no appendices. A nil renderer means the default.
func New(policy PolicyClient, renderer *render.Renderer) *Handler {
	if renderer == nil {
		renderer = render.New()
	}
	return &Handler{policy: policy, renderer: renderer, logger: log.Nop()}
}

// NewWithAppendix returns a Handler that renders the appendices after the body.
func NewWithAppendix(policy PolicyClient, appendix AppendixClient, renderer *render.Renderer) *Handler {
	if renderer == nil {
		renderer = render.New()
	}
	return &Handler{policy: policy, appendix: appendix, renderer: renderer, logger: log.Nop()}
}

// WithUpdated adds a "Last updated" line above the document.
func (h *Handler) WithUpdated(c UpdatedClient) *Handler {
	h.updated = c
	return h
}

// WithHistory adds the version-history page to a render whose fetch URL names
// a job that asked for it (?job=<id>).
func (h *Handler) WithHistory(c HistoryClient) *Handler {
	h.history = c
	return h
}

// WithLogger logs upstream and render failures to l.
func (h *Handler) WithLogger(l log.Logger) *Handler {
	h.logger = l
	return h
}

// Mount registers the handler on mux at the path every render's fetch URL
// points at.
func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /internal/policies/{versionId}/html", h.serveHTML)
}

func (h *Handler) serveHTML(w http.ResponseWriter, r *http.Request) {
	versionID := strings.TrimSpace(r.PathValue("versionId"))
	if versionID == "" {
		http.Error(w, "versionId required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	raw, err := h.policy.GetContent(ctx, versionID)
	if err != nil {
		// A 404 lets the renderer fail the job instead of retrying.
		if isNotFound(err) {
			http.Error(w, "policy version not found", http.StatusNotFound)
			return
		}
		h.logger.Ctx(ctx).Error(err, "fetch policy content", log.F("version_id", versionID))
		http.Error(w, "fetch policy failed", http.StatusBadGateway)
		return
	}

	bodyHTML, err := h.renderer.RenderHTML(raw)
	if err != nil {
		h.logger.Ctx(ctx).Error(err, "render html", log.F("version_id", versionID))
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}

	var rendered []AppendixRender
	if h.appendix != nil {
		metas, err := h.appendix.ListAppendices(ctx, versionID)
		if err != nil {
			h.logger.Ctx(ctx).Error(err, "fetch appendices", log.F("version_id", versionID))
			http.Error(w, "fetch appendices failed", http.StatusBadGateway)
			return
		}
		rendered = make([]AppendixRender, 0, len(metas))
		for _, m := range metas {
			aHTML, err := h.renderer.RenderHTML(m.ContentJSON)
			if err != nil {
				h.logger.Ctx(ctx).Error(err, "render appendix", log.F("version_id", versionID))
				http.Error(w, "render failed", http.StatusInternalServerError)
				return
			}
			rendered = append(rendered, AppendixRender{
				Letter:   string(rune('A' + m.OrderIndex)),
				Title:    m.Title,
				BodyHTML: aHTML,
			})
		}
	}

	out := appendHTML(bodyHTML, rendered)
	if jobID := strings.TrimSpace(r.URL.Query().Get("job")); jobID != "" && h.history != nil {
		entries, requested, err := h.history.JobHistory(ctx, jobID, versionID)
		if isNotFound(err) {
			http.Error(w, "job not found for this version", http.StatusNotFound)
			return
		}
		if err != nil {
			h.logger.Ctx(ctx).Error(err, "fetch history", log.F("version_id", versionID), log.F("job_id", jobID))
			http.Error(w, "fetch history failed", http.StatusBadGateway)
			return
		}
		h.logger.Ctx(ctx).Debug("history page", log.F("version_id", versionID), log.F("job_id", jobID),
			log.F("requested", requested), log.F("entries", len(entries)))
		if requested {
			out += historyHTML(entries)
		}
	}
	if h.updated != nil {
		at, err := h.updated.GetUpdatedAt(ctx, versionID)
		if err != nil {
			h.logger.Ctx(ctx).Error(err, "fetch updated date", log.F("version_id", versionID))
			http.Error(w, "fetch policy failed", http.StatusBadGateway)
			return
		}
		out = updatedHTML(at) + out
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(out)) // #nosec G705 -- render.RenderHTML escapes every text value and emits only contract tags; appendix titles are escaped in appendHTML
}

// ErrNotFound is the sentinel a policy client wraps for an unknown version.
var ErrNotFound = errors.New("policy version not found")

func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
