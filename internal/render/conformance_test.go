// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package render_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Steward-GRC/steward-delivery/internal/render"
)

// TestGoldenFileConformance runs the server renderer against every fixture in
// testdata/policies and compares the output, whitespace-normalised, to the
// matching file in testdata/expected. Add a fixture pair to cover a new node
// type or edge case. Sensitive fixtures (sensitivity:true) need an expected
// file too: the renderer output must still match the golden HTML.
// FIXTURES_DIR points the suite at another fixture set.
func TestGoldenFileConformance(t *testing.T) {
	fixturesDir := os.Getenv("FIXTURES_DIR")
	if fixturesDir == "" {
		fixturesDir = "testdata"
	}

	policiesDir := filepath.Join(fixturesDir, "policies")
	expectedDir := filepath.Join(fixturesDir, "expected")

	entries, err := os.ReadDir(policiesDir)
	if err != nil {
		t.Fatalf("cannot read fixtures/policies at %s: %v", policiesDir, err)
	}
	if len(entries) == 0 {
		t.Fatalf("no fixtures in %s", policiesDir)
	}

	r := render.New()

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		t.Run(name, func(t *testing.T) {
			rawJSON, err := os.ReadFile(filepath.Join(policiesDir, e.Name()))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			// Parse sensitivity flag so the test log can note it.
			var meta struct {
				Sensitivity bool `json:"sensitivity"`
			}
			_ = json.Unmarshal(rawJSON, &meta)

			got, err := r.RenderHTML(rawJSON)
			if err != nil {
				t.Fatalf("RenderHTML (sensitive=%v): %v", meta.Sensitivity, err)
			}

			expectedPath := filepath.Join(expectedDir, name+".html")
			wantBytes, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("no expected file at %s — create it with the canonical renderer output (sensitive=%v): %v",
					expectedPath, meta.Sensitivity, err)
			}

			normalize := func(s string) string {
				return strings.Join(strings.Fields(s), " ")
			}
			if normalize(got) != normalize(string(wantBytes)) {
				t.Errorf("render output mismatch for %s (sensitive=%v)\nGOT:\n%s\nWANT:\n%s",
					name, meta.Sensitivity, got, string(wantBytes))
			}
		})
	}
}
