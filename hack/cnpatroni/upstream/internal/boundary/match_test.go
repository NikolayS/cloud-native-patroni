/*
Copyright © contributors to CloudNativePG, established as
CloudNativePG a Series of LF Projects, LLC.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

SPDX-License-Identifier: Apache-2.0
*/

package boundary_test

import (
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/boundary"
)

func manifestFrom(t *testing.T, body string) *boundary.Manifest {
	t.Helper()

	m, err := boundary.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	return m
}

const matchManifest = `
schema: cnpatroni.io/boundary/v1
generated_from: test
provisional: true
default_ownership: upstream-untouched
audit_scan:
  include: ["**/*.go"]
  exclude: ["**/*_test.go"]
audit_terms: ["TargetPrimary"]
generated_artifacts: ["api/**"]
required_paths: []
rules:
  - id: broad
    ownership: adapted
    paths: ["pkg/management/**"]
  - id: narrow
    ownership: disabled
    mechanism: guarded
    paths: ["pkg/management/postgres/webserver/probes/**"]
  - id: exact
    ownership: adapted
    paths: ["pkg/specs/pods.go"]
  - id: wide
    ownership: disabled
    mechanism: guarded
    paths: ["pkg/**"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`

func TestMatchPrecedence(t *testing.T) {
	m := manifestFrom(t, matchManifest)

	cases := []struct {
		name string
		path string
		want string
	}{
		{"exact_beats_glob", "pkg/specs/pods.go", "exact"},
		{"longest_literal_prefix_wins", "pkg/management/postgres/webserver/probes/liveness.go", "narrow"},
		{"less_specific_glob_still_matches", "pkg/management/postgres/instance.go", "broad"},
		{"catchall_always_loses", "pkg/other/thing.go", "wide"},
		{"catchall_matches_when_nothing_else_does", "docs/src/faq.md", "absorb"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := m.Match(tc.path)
			if rule == nil {
				t.Fatalf("Match(%q) returned no rule", tc.path)
			}
			if rule.ID != tc.want {
				t.Fatalf("Match(%q) = %q, want %q", tc.path, rule.ID, tc.want)
			}
		})
	}
}

func TestLaterDeclarationBreaksTie(t *testing.T) {
	m := manifestFrom(t, `
schema: cnpatroni.io/boundary/v1
generated_from: test
provisional: true
default_ownership: upstream-untouched
audit_scan: {include: ["**/*.go"], exclude: []}
audit_terms: []
generated_artifacts: []
required_paths: []
rules:
  - id: first
    ownership: adapted
    paths: ["pkg/a*/x.go"]
  - id: second
    ownership: disabled
    mechanism: guarded
    paths: ["pkg/a?/x.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`)

	rule := m.Match("pkg/ab/x.go")
	if rule == nil || rule.ID != "second" {
		t.Fatalf("Match returned %v, want the later rule %q", rule, "second")
	}
}

func TestIsCatchAll(t *testing.T) {
	m := manifestFrom(t, matchManifest)

	if !m.Match("docs/src/faq.md").IsCatchAll() {
		t.Error("the ** rule must report IsCatchAll")
	}
	if m.Match("pkg/specs/pods.go").IsCatchAll() {
		t.Error("an explicit rule must not report IsCatchAll")
	}
}

func TestOwnershipNeedsReview(t *testing.T) {
	cases := map[boundary.Ownership]bool{
		boundary.OwnershipUpstreamUntouched: false,
		boundary.OwnershipAdapted:           true,
		boundary.OwnershipDisabled:          true,
		boundary.OwnershipDeleted:           true,
		boundary.OwnershipCNPatroniOwned:    false,
	}

	for ownership, want := range cases {
		if got := ownership.NeedsReview(); got != want {
			t.Errorf("%s.NeedsReview() = %v, want %v", ownership, got, want)
		}
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := boundary.Parse([]byte(`
schema: cnpatroni.io/boundary/v1
generated_from: test
provisional: true
default_ownership: upstream-untouched
audit_scan: {include: [], exclude: []}
audit_terms: []
generated_artifacts: []
required_paths: []
typo_field: true
rules:
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`))
	if err == nil {
		t.Fatal("Parse must reject an unknown top-level field")
	}
}
