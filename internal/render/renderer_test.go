// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package render_test

import (
	"strings"
	"testing"

	"github.com/Steward-GRC/steward-delivery/internal/render"
)

func TestRenderHTMLSectionStructure(t *testing.T) {
	input := []byte(`{
		"root": { "type": "root", "children": [
			{ "type": "SectionNode", "sectionKey": "purpose", "title": "Purpose", "children": [
				{ "type": "BoilerplateNode", "children": [
					{ "type": "text", "text": "Boilerplate text." }
				]},
				{ "type": "EditableRegionNode", "children": [
					{ "type": "paragraph", "children": [{ "type": "text", "text": "Author text." }] }
				]}
			]}
		]},
		"sensitivity": false
	}`)

	r := render.New()
	got, err := r.RenderHTML(input)
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}

	checks := []struct{ label, want string }{
		{"article wrapper", `<article class="policy-document">`},
		{"section element", `<section class="policy-section" data-section-key="purpose">`},
		{"section title", `<h2 class="section-title">Purpose</h2>`},
		{"boilerplate div", `<div class="boilerplate" aria-readonly="true">`},
		{"editable div", `<div class="editable-region">`},
		{"paragraph", `<p>Author text.</p>`},
	}
	for _, c := range checks {
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q in output\nGot:\n%s", c.label, c.want, got)
		}
	}
}

func TestRenderHTMLBoilerplateNotEditable(t *testing.T) {
	input := []byte(`{
		"root": { "type": "root", "children": [
			{ "type": "SectionNode", "sectionKey": "s1", "title": "S1", "children": [
				{ "type": "BoilerplateNode", "children": [{ "type": "text", "text": "Locked." }]}
			]}
		]},
		"sensitivity": false
	}`)

	r := render.New()
	got, err := r.RenderHTML(input)
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	if strings.Contains(got, `class="editable-region"`) {
		t.Error("boilerplate section must not produce an editable-region element")
	}
	if !strings.Contains(got, `aria-readonly="true"`) {
		t.Error("boilerplate must carry aria-readonly=true")
	}
}

func TestRenderHTMLUnknownNodeTypeErrors(t *testing.T) {
	input := []byte(`{
		"root": { "type": "root", "children": [
			{ "type": "UnknownNode", "children": [] }
		]},
		"sensitivity": false
	}`)

	r := render.New()
	if _, err := r.RenderHTML(input); err == nil {
		t.Error("expected error for unknown node type")
	}
}
