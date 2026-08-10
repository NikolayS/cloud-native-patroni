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

package report_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarkdownReportsAnEmptyRange(t *testing.T) {
	s := newScenario(t)
	md := s.build(t).Markdown()

	for _, want := range []string{
		"# Upstream integration report",
		"**Verdict:** absorb",
		"## Boundary files touched\n\nNone.",
		"| Concentration ratio | 0.000 |",
		"| Gates required | none |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q:\n%s", want, md)
		}
	}
}

func TestMarkdownNamesTheBoundaryFileAndItsGates(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("fix: promote faster", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n// pg_ctl\n// x\n",
	})

	md := s.build(t).Markdown()

	for _, want := range []string{
		"needs review",
		"`pkg/management/postgres/instance.go`",
		"adapt.process-primitives",
		"authority-audit, chaos",
		"high-availability signal",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q:\n%s", want, md)
		}
	}
}

func TestMarkdownHeadingsAreSentenceCase(t *testing.T) {
	s := newScenario(t)
	md := s.build(t).Markdown()

	for _, line := range strings.Split(md, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimLeft(line, "# ")
		words := strings.Fields(heading)
		for _, word := range words[1:] {
			// Proper nouns keep their capitals in a sentence-case heading.
			if strings.HasPrefix(word, "CloudNativePatroni") || strings.HasPrefix(word, "CloudNativePG") {
				continue
			}
			if word != "" && strings.ToUpper(word[:1]) == word[:1] && strings.ToLower(word) != word {
				t.Errorf("heading %q is not sentence case (%q)", heading, word)
			}
		}
	}
}

func TestJSONIsIndentedAndNewlineTerminated(t *testing.T) {
	s := newScenario(t)
	body, err := s.build(t).JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}

	if !strings.HasSuffix(string(body), "}\n") {
		t.Error("the JSON report must end with a newline")
	}
	if !strings.Contains(string(body), "\n  \"schema\": \"cnpatroni.io/upstream-report/v1\"") {
		t.Errorf("the JSON report is not indented as expected:\n%s", body)
	}

	var round map[string]any
	if err := json.Unmarshal(body, &round); err != nil {
		t.Fatalf("the JSON report does not parse: %v", err)
	}
}
