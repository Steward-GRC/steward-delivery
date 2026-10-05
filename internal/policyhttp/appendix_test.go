// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package policyhttp

import (
	"strings"
	"testing"
)

func TestAppendHTML_EmptyAppendices(t *testing.T) {
	body := `<article class="policy-document"><p>body</p></article>`
	got := appendHTML(body, nil)
	if got != body {
		t.Errorf("empty appendices: got %q, want body unchanged %q", got, body)
	}
}

func TestAppendHTML_TwoAppendices(t *testing.T) {
	body := `<article class="policy-document"><p>body</p></article>`
	appendices := []AppendixRender{
		{Letter: "A", Title: "First Appendix", BodyHTML: "<p>Alpha content</p>"},
		{Letter: "B", Title: "Second Appendix", BodyHTML: "<p>Beta content</p>"},
	}
	got := appendHTML(body, appendices)

	// Body comes first
	if !strings.HasPrefix(got, body) {
		t.Errorf("body should come first; got:\n%s", got)
	}

	// Section A before section B
	idxA := strings.Index(got, `<section class="policy-appendix">`)
	idxB := strings.LastIndex(got, `<section class="policy-appendix">`)
	if idxA == idxB {
		t.Error("expected two policy-appendix sections")
	}
	if idxA > idxB {
		t.Error("section A should come before section B")
	}

	checks := []struct{ label, want string }{
		{"appendix A heading", `<h2>Appendix A: First Appendix</h2>`},
		{"appendix A body", `<p>Alpha content</p>`},
		{"appendix B heading", `<h2>Appendix B: Second Appendix</h2>`},
		{"appendix B body", `<p>Beta content</p>`},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q in output\nGot:\n%s", c.label, c.want, got)
		}
	}
}

func TestAppendHTML_TitleEscaping(t *testing.T) {
	body := `<article class="policy-document"></article>`
	appendices := []AppendixRender{
		{Letter: "A", Title: "Bad <title> & more", BodyHTML: "<p>safe</p>"},
	}
	got := appendHTML(body, appendices)

	if strings.Contains(got, "<title>") {
		t.Errorf("raw <title> tag should be escaped; got:\n%s", got)
	}
	if !strings.Contains(got, "Bad &lt;title&gt; &amp; more") {
		t.Errorf("expected escaped title in output; got:\n%s", got)
	}
}
