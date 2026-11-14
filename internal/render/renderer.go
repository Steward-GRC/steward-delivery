// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

// Renderer converts serialized Lexical JSON (policy content) to HTML
// conforming to the rendering contract defined in contract.go.
type Renderer struct{}

// New returns a stateless Renderer. It is safe for concurrent use.
func New() *Renderer { return &Renderer{} }

// lexicalDoc is the top-level JSON structure from PolicyVersion.content.
type lexicalDoc struct {
	Root        lexicalNode `json:"root"`
	Sensitivity bool        `json:"sensitivity"`
}

type lexicalNode struct {
	Type       string `json:"type"`
	SectionKey string `json:"sectionKey,omitempty"`
	Title      string `json:"title,omitempty"`
	Text       string `json:"text,omitempty"`
	Tag        string `json:"tag,omitempty"`
	ListType   string `json:"listType,omitempty"`
	URL        string `json:"url,omitempty"`
	// Format is numeric (a bit mask) on text nodes and a string (alignment)
	// on element nodes, so it is kept raw and read only where it applies.
	Format   json.RawMessage `json:"format,omitempty"`
	Children []lexicalNode   `json:"children,omitempty"`
}

const indentUnit = "  "

// RenderHTML converts Lexical JSON to a contract-compliant HTML string.
// Output is pretty-printed with two-space indentation; the golden-file
// conformance suite normalizes whitespace before comparing, so the indent
// style is part of the canonical baseline but tolerant of equivalent
// re-formatting. Unknown node types return an error so missing contract
// entries surface in tests rather than silently dropping content.
func (r *Renderer) RenderHTML(lexicalJSON []byte) (string, error) {
	var doc lexicalDoc
	if err := json.Unmarshal(lexicalJSON, &doc); err != nil {
		return "", fmt.Errorf("parse lexical json: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(`<article class="policy-document">`)
	for _, child := range doc.Root.Children {
		buf.WriteByte('\n')
		buf.WriteString(indentUnit)
		if err := renderNode(&buf, child, 1); err != nil {
			return "", err
		}
	}
	if len(doc.Root.Children) > 0 {
		buf.WriteByte('\n')
	}
	buf.WriteString(`</article>`)
	return buf.String(), nil
}

func renderNode(buf *bytes.Buffer, node lexicalNode, depth int) error {
	switch node.Type {
	case "SectionNode":
		fmt.Fprintf(buf, `<section class="policy-section" data-section-key="%s">`, html.EscapeString(node.SectionKey))
		childIndent := strings.Repeat(indentUnit, depth+1)
		buf.WriteByte('\n')
		buf.WriteString(childIndent)
		fmt.Fprintf(buf, `<h2 class="section-title">%s</h2>`, html.EscapeString(node.Title))
		for _, child := range node.Children {
			buf.WriteByte('\n')
			buf.WriteString(childIndent)
			if err := renderNode(buf, child, depth+1); err != nil {
				return err
			}
		}
		buf.WriteByte('\n')
		buf.WriteString(strings.Repeat(indentUnit, depth))
		buf.WriteString(`</section>`)

	case "BoilerplateNode":
		buf.WriteString(`<div class="boilerplate" aria-readonly="true">`)
		// BoilerplateNode children in the contract are inline text; the golden
		// fixtures wrap the text in <p> so the visual baseline matches the
		// EditableRegion paragraph layout. When the children are already block
		// elements (e.g. paragraph), render them directly.
		if hasOnlyTextChildren(node.Children) {
			buf.WriteString(`<p>`)
			for _, child := range node.Children {
				if err := renderNode(buf, child, depth+1); err != nil {
					return err
				}
			}
			buf.WriteString(`</p>`)
		} else {
			for _, child := range node.Children {
				if err := renderNode(buf, child, depth+1); err != nil {
					return err
				}
			}
		}
		buf.WriteString(`</div>`)

	case "EditableRegionNode":
		buf.WriteString(`<div class="editable-region">`)
		for _, child := range node.Children {
			if err := renderNode(buf, child, depth+1); err != nil {
				return err
			}
		}
		buf.WriteString(`</div>`)

	case "paragraph":
		buf.WriteString(`<p>`)
		for _, child := range node.Children {
			if err := renderNode(buf, child, depth+1); err != nil {
				return err
			}
		}
		buf.WriteString(`</p>`)

	case "heading":
		tag := headingTag(node.Tag)
		fmt.Fprintf(buf, `<%s>`, tag)
		if err := renderChildren(buf, node.Children, depth+1); err != nil {
			return err
		}
		fmt.Fprintf(buf, `</%s>`, tag)

	case "list":
		tag := "ul"
		if node.ListType == "number" || node.Tag == "ol" {
			tag = "ol"
		}
		fmt.Fprintf(buf, `<%s>`, tag)
		if err := renderChildren(buf, node.Children, depth+1); err != nil {
			return err
		}
		fmt.Fprintf(buf, `</%s>`, tag)

	case "listitem":
		buf.WriteString(`<li>`)
		if err := renderChildren(buf, node.Children, depth+1); err != nil {
			return err
		}
		buf.WriteString(`</li>`)

	case "quote":
		buf.WriteString(`<blockquote>`)
		if err := renderChildren(buf, node.Children, depth+1); err != nil {
			return err
		}
		buf.WriteString(`</blockquote>`)

	case "linebreak":
		buf.WriteString(`<br>`)

	case "link", "autolink":
		safe := safeURL(node.URL)
		if safe != "" {
			fmt.Fprintf(buf, `<a href="%s">`, html.EscapeString(safe))
		}
		if err := renderChildren(buf, node.Children, depth+1); err != nil {
			return err
		}
		if safe != "" {
			buf.WriteString(`</a>`)
		}

	case "text":
		open, closeTags := textFormatTags(node.Format)
		buf.WriteString(open)
		buf.WriteString(escapeText(node.Text))
		buf.WriteString(closeTags)

	default:
		return fmt.Errorf("unknown node type %q: add it to the rendering contract", node.Type)
	}
	return nil
}

// escapeText escapes text content for safe HTML embedding. It first runs the
// standard escape (which handles <, >, &, ", '), then promotes every non-ASCII
// rune to its numeric character reference (&#xHHHH;) so that the rendered HTML
// is pure ASCII. This matches the canonical golden-file output and keeps
// downstream byte-level equality checks stable across encodings.
func escapeText(s string) string {
	escaped := html.EscapeString(s)
	var b strings.Builder
	b.Grow(len(escaped))
	for _, r := range escaped {
		if r < 0x80 {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, "&#x%X;", r)
	}
	return b.String()
}

func hasOnlyTextChildren(children []lexicalNode) bool {
	if len(children) == 0 {
		return false
	}
	for _, c := range children {
		if c.Type != "text" {
			return false
		}
	}
	return true
}

func renderChildren(buf *bytes.Buffer, children []lexicalNode, depth int) error {
	for _, child := range children {
		if err := renderNode(buf, child, depth); err != nil {
			return err
		}
	}
	return nil
}

// headingTag returns h1 to h6 for a Lexical heading tag, and h2 for anything
// else so a malformed tag cannot inject markup.
func headingTag(tag string) string {
	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return tag
	}
	return "h2"
}

// safeURL returns the URL when its scheme is http, https or mailto, and an
// empty string otherwise, so exported documents never carry script links.
func safeURL(raw string) string {
	u := strings.TrimSpace(raw)
	l := strings.ToLower(u)
	if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "mailto:") {
		return u
	}
	return ""
}

// Lexical text format bits.
const (
	fmtBold          = 1
	fmtItalic        = 2
	fmtStrikethrough = 4
	fmtUnderline     = 8
	fmtCode          = 16
)

// textFormatTags returns the opening and closing tags for a text node's
// format bit mask. A missing or non-numeric format means plain text.
func textFormatTags(raw json.RawMessage) (string, string) {
	var f int
	if len(raw) == 0 || json.Unmarshal(raw, &f) != nil || f == 0 {
		return "", ""
	}
	var open, closing strings.Builder
	add := func(bit int, tag string) {
		if f&bit != 0 {
			open.WriteString("<" + tag + ">")
			s := "</" + tag + ">"
			closing.WriteString(s)
		}
	}
	add(fmtBold, "strong")
	add(fmtItalic, "em")
	add(fmtStrikethrough, "s")
	add(fmtUnderline, "u")
	add(fmtCode, "code")
	return open.String(), reverseTags(closing.String())
}

// reverseTags reorders closing tags so they nest correctly.
func reverseTags(s string) string {
	parts := strings.SplitAfter(s, ">")
	var out strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		out.WriteString(parts[i])
	}
	return out.String()
}
