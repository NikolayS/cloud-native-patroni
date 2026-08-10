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

	"github.com/postgres-ai/cnpatroni-upstream/internal/boundary"
)

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"exact", "pkg/specs/pods.go", "pkg/specs/pods.go", true},
		{"exact_mismatch", "pkg/specs/pods.go", "pkg/specs/jobs.go", false},
		{"star_matches_within_segment", "pkg/*.go", "pkg/a.go", true},
		{"star_does_not_cross_slash", "pkg/*.go", "pkg/x/a.go", false},
		{"star_matches_empty", "pkg/a*.go", "pkg/a.go", true},
		{"question_one_character", "pkg/?.go", "pkg/a.go", true},
		{"question_not_two", "pkg/?.go", "pkg/ab.go", false},
		{"question_does_not_cross_slash", "pkg/?.go", "pkg//.go", false},
		{"doublestar_one_segment", "pkg/**", "pkg/a.go", true},
		{"doublestar_many_segments", "pkg/**", "pkg/x/y/a.go", true},
		{"doublestar_zero_segments", "a/**/b", "a/b", true},
		{"doublestar_middle", "a/**/b", "a/x/y/b", true},
		{"doublestar_does_not_escape_prefix", "pkg/**", "internal/a.go", false},
		{"doublestar_alone_matches_everything", "**", "a/b/c.go", true},
		{"trailing_slash_shorthand", "pkg/patroni/", "pkg/patroni/x/y.go", true},
		{"trailing_slash_excludes_prefix_sibling", "pkg/patroni/", "pkg/patronix.go", false},
		{"prefix_without_doublestar_is_not_a_directory", "pkg/specs", "pkg/specs/pods.go", false},
		{"workflow_star", ".github/workflows/cnpatroni-*.yml", ".github/workflows/cnpatroni-x.yml", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := boundary.CompileGlob(tc.pattern)
			if err != nil {
				t.Fatalf("CompileGlob(%q): %v", tc.pattern, err)
			}
			if got := g.Match(tc.path); got != tc.want {
				t.Fatalf("CompileGlob(%q).Match(%q) = %v, want %v",
					tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}

func TestCompileGlobRejectsIllegalPatterns(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
	}{
		{"empty", ""},
		{"leading_slash", "/pkg/x.go"},
		{"leading_dot_slash", "./pkg/x.go"},
		{"doublestar_not_a_whole_segment", "a**b"},
		{"doublestar_glued_to_prefix", "pkg**/x.go"},
		{"triple_star", "a/***/b"},
		{"character_class", "pkg/[ab].go"},
		{"brace_expansion", "pkg/{a,b}.go"},
		{"backslash_separator", "pkg\\specs\\pods.go"},
		{"empty_segment", "pkg//x.go"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := boundary.CompileGlob(tc.pattern); err == nil {
				t.Fatalf("CompileGlob(%q) must fail", tc.pattern)
			}
		})
	}
}

func TestGlobLiteralPrefix(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
	}{
		{"pkg/specs/pods.go", "pkg/specs/pods.go"},
		{"pkg/management/**", "pkg/management/"},
		{"pkg/management/postgres/webserver/probes/**", "pkg/management/postgres/webserver/probes/"},
		{"**", ""},
		{".github/workflows/cnpatroni-*.yml", ".github/workflows/cnpatroni-"},
	}

	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			g, err := boundary.CompileGlob(tc.pattern)
			if err != nil {
				t.Fatalf("CompileGlob: %v", err)
			}
			if got := g.LiteralPrefix(); got != tc.want {
				t.Fatalf("LiteralPrefix() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGlobIsLiteral(t *testing.T) {
	literal, err := boundary.CompileGlob("pkg/specs/pods.go")
	if err != nil {
		t.Fatalf("CompileGlob: %v", err)
	}
	if !literal.IsLiteral() {
		t.Error("a pattern with no metacharacters must report IsLiteral")
	}

	globbed, err := boundary.CompileGlob("pkg/**")
	if err != nil {
		t.Fatalf("CompileGlob: %v", err)
	}
	if globbed.IsLiteral() {
		t.Error("a pattern containing ** must not report IsLiteral")
	}
}
