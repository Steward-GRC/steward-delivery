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

func TestRenderHTMLEditorNodes(t *testing.T) {
	input := []byte(`{
		"root": { "type": "root", "children": [
			{ "type": "heading", "tag": "h1", "format": "", "children": [{ "type": "text", "text": "Title", "format": 0 }] },
			{ "type": "paragraph", "format": "", "children": [
				{ "type": "text", "text": "bold", "format": 1 },
				{ "type": "text", "text": "both", "format": 3 },
				{ "type": "linebreak" },
				{ "type": "link", "url": "https://example.org/a?b=1&c=2", "children": [{ "type": "text", "text": "site" }] },
				{ "type": "link", "url": "javascript:alert(1)", "children": [{ "type": "text", "text": "bad" }] }
			]},
			{ "type": "list", "listType": "number", "tag": "ol", "children": [
				{ "type": "listitem", "children": [{ "type": "text", "text": "one" }] }
			]},
			{ "type": "list", "listType": "bullet", "tag": "ul", "children": [
				{ "type": "listitem", "children": [{ "type": "text", "text": "dot" }] }
			]},
			{ "type": "quote", "children": [{ "type": "text", "text": "said" }] }
		]},
		"sensitivity": false
	}`)
	got, err := render.New().RenderHTML(input)
	if err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	for _, want := range []string{
		`<h1>Title</h1>`,
		`<strong>bold</strong>`,
		`<strong><em>both</em></strong>`,
		`<br>`,
		`<a href="https://example.org/a?b=1&amp;c=2">site</a>`,
		`bad`,
		`<ol><li>one</li></ol>`,
		`<ul><li>dot</li></ul>`,
		`<blockquote>said</blockquote>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("want %q in output\nGot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "javascript:") {
		t.Errorf("script link survived:\n%s", got)
	}
}

func TestRenderHTMLUnknownNodeStillFails(t *testing.T) {
	_, err := render.New().RenderHTML([]byte(`{"root":{"type":"root","children":[{"type":"mystery"}]}}`))
	if err == nil {
		t.Fatal("want an error for an unknown node type")
	}
}
