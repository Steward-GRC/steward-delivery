// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package render implements the server-side HTML renderer that converts
// serialized Lexical JSON policy documents into contract-conformant HTML.
//
// The rendering contract defined here is the authoritative node-to-visual
// mapping; every renderer (server, web client, native clients) must produce
// HTML that matches this contract for the same input. The golden-file
// conformance suite in conformance_test.go enforces it against the fixtures
// in testdata.
package render

// NodeSpec is the visual contract for a single Lexical node type.
// It mirrors the steward.delivery.v1.VisualSpec message.
type NodeSpec struct {
	// HTMLElement is the tag name the renderer emits (e.g. "section", "div", "p").
	HTMLElement string
	// CSSClasses lists the classes attached to the emitted element.
	CSSClasses []string
	// ReadOnly indicates the node's content must not be presented as editable.
	// BoilerplateNode is the canonical read-only node.
	ReadOnly bool
	// Role is an ARIA role hint for renderers when one is required.
	Role string
	// AriaReadonly, when non-empty, is emitted as the aria-readonly attribute.
	AriaReadonly string
}

// Contract is the canonical node-to-visual mapping. Renderers must produce
// HTML matching this spec; the golden-file suite enforces it across platforms.
// Add an entry here when introducing a new node type, then update fixtures.
var Contract = map[string]NodeSpec{
	"SectionNode": {
		HTMLElement: "section",
		CSSClasses:  []string{"policy-section"},
	},
	"BoilerplateNode": {
		HTMLElement:  "div",
		CSSClasses:   []string{"boilerplate"},
		ReadOnly:     true,
		AriaReadonly: "true",
	},
	"EditableRegionNode": {
		HTMLElement: "div",
		CSSClasses:  []string{"editable-region"},
	},
	"paragraph": {
		HTMLElement: "p",
	},
}
