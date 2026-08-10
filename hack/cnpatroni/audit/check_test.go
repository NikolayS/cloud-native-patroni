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

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func loadClassificationFromString(t *testing.T, content string) (*Classification, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "classification.yaml")
	writeFile(t, path, content)
	return LoadClassification(path)
}

const minimalEntry = `schema: cnpatroni-authority-classification/v1
defaults:
  owner: NikolayS
entries:
  - path: ctrl/ctrl.go
    symbol: example.com/hits/ctrl.Promote
    class: disable
    dest: disabled
    rationale: Promotion is owned by Patroni and never by this operator.
    reviewed-at: "2026-08-09 23:20:00 UTC"
`

func TestLoadClassificationAcceptsAValidFile(t *testing.T) {
	cls, err := loadClassificationFromString(t, minimalEntry)
	if err != nil {
		t.Fatalf("LoadClassification: %v", err)
	}
	if got := cls.Entries[0].Owner; got != "NikolayS" {
		t.Errorf("owner default not applied, got %q", got)
	}
}

func TestLoadClassificationRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"unknown schema", "schema: nope/v1\n", "schema"},
		{
			"unknown class",
			replaceOnce(minimalEntry, "class: disable", "class: banish"),
			"class",
		},
		{
			"unknown destination",
			replaceOnce(minimalEntry, "dest: disabled", "dest: patroni"),
			"dest",
		},
		{
			"rationale too short",
			replaceOnce(minimalEntry,
				"rationale: Promotion is owned by Patroni and never by this operator.",
				"rationale: too short"),
			"rationale",
		},
		{
			"reviewed-at is a bare date",
			replaceOnce(minimalEntry, `reviewed-at: "2026-08-09 23:20:00 UTC"`, `reviewed-at: "2026-08-09"`),
			"reviewed-at",
		},
		{
			"disable must be destined for disabled",
			replaceOnce(minimalEntry, "dest: disabled", "dest: operator"),
			"contradict",
		},
		{
			"keep must not be destined for disabled",
			replaceOnce(minimalEntry, "class: disable", "class: keep"),
			"contradict",
		},
		{
			"duplicate symbol",
			minimalEntry + strings.TrimPrefix(minimalEntry, "schema: cnpatroni-authority-classification/v1\ndefaults:\n  owner: NikolayS\nentries:\n"),
			"duplicate",
		},
		{
			"missing owner",
			replaceOnce(minimalEntry, "defaults:\n  owner: NikolayS\n", ""),
			"owner",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadClassificationFromString(t, tc.body)
			if err == nil {
				t.Fatal("LoadClassification accepted an invalid file")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

const responsibilityClassification = `schema: cnpatroni-authority-classification/v1
defaults:
  owner: NikolayS
responsibilities:
  - responsibility: Exec and supervise the Postgres server process
    dest: patroni-container
    rationale: Patroni owns the postmaster process for its complete lifetime.
    reviewed-at: "2026-08-09 23:20:00 UTC"
    anchors: [example.com/hits/ctrl.Promote]
`

func TestLoadClassificationValidatesResponsibilities(t *testing.T) {
	cls, err := loadClassificationFromString(t, responsibilityClassification)
	if err != nil {
		t.Fatalf("LoadClassification valid responsibility: %v", err)
	}
	if got := cls.Responsibilities[0].Owner; got != "NikolayS" {
		t.Errorf("responsibility owner default = %q, want NikolayS", got)
	}

	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "missing owner",
			body: replaceOnce(responsibilityClassification,
				"defaults:\n  owner: NikolayS\n", ""),
			wantErr: "no owner",
		},
		{
			name: "missing description",
			body: replaceOnce(responsibilityClassification,
				"responsibility: Exec and supervise the Postgres server process",
				"responsibility: \"\""),
			wantErr: "no description",
		},
		{
			name:    "unknown destination",
			body:    replaceOnce(responsibilityClassification, "dest: patroni-container", "dest: nowhere"),
			wantErr: "destination vocabulary",
		},
		{
			name: "short rationale",
			body: replaceOnce(responsibilityClassification,
				"rationale: Patroni owns the postmaster process for its complete lifetime.",
				"rationale: too short"),
			wantErr: "rationale shorter",
		},
		{
			name: "malformed review timestamp",
			body: replaceOnce(responsibilityClassification,
				`reviewed-at: "2026-08-09 23:20:00 UTC"`, `reviewed-at: "2026-08-09"`),
			wantErr: "reviewed-at",
		},
		{
			name:    "empty anchors",
			body:    replaceOnce(responsibilityClassification, "anchors: [example.com/hits/ctrl.Promote]", "anchors: []"),
			wantErr: "no anchors",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadClassificationFromString(t, tc.body)
			if err == nil {
				t.Fatal("LoadClassification accepted an invalid responsibility")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

const allowClassification = `schema: cnpatroni-authority-classification/v1
defaults:
  owner: NikolayS
allow:
  - rule: sync.standby-names
    package: example.com/hits/pg/...
    file: pg/config.go
    symbol: example.com/hits/pg.Render
    reason: This literal rejects operator-owned configuration rather than writing it.
    reviewed-at: "2026-08-09 23:20:00 UTC"
    until: M1
`

func TestLoadClassificationValidatesAllowEntries(t *testing.T) {
	cls, err := loadClassificationFromString(t, allowClassification)
	if err != nil {
		t.Fatalf("LoadClassification valid allow entry: %v", err)
	}
	if got := cls.Allow[0].Owner; got != "NikolayS" {
		t.Errorf("allow owner default = %q, want NikolayS", got)
	}
	allowed := Finding{
		Rule: "sync.standby-names", Package: "example.com/hits/pg/config",
		Path: "pg/config.go", Symbol: "example.com/hits/pg.Render",
	}
	if !cls.allows(allowed) {
		t.Errorf("validated subtree allow entry did not cover %+v", allowed)
	}
	allowed.Path = "pg/other.go"
	if cls.allows(allowed) {
		t.Errorf("file-scoped allow entry covered the wrong path: %+v", allowed)
	}

	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing rule",
			body:    replaceOnce(allowClassification, "rule: sync.standby-names", "rule: \"\""),
			wantErr: "no rule",
		},
		{
			name:    "missing package",
			body:    replaceOnce(allowClassification, "package: example.com/hits/pg/...", "package: \"\""),
			wantErr: "no package",
		},
		{
			name: "short reason",
			body: replaceOnce(allowClassification,
				"reason: This literal rejects operator-owned configuration rather than writing it.",
				"reason: too short"),
			wantErr: "reason shorter",
		},
		{
			name: "missing owner",
			body: replaceOnce(allowClassification,
				"defaults:\n  owner: NikolayS\n", ""),
			wantErr: "no owner",
		},
		{
			name: "malformed review timestamp",
			body: replaceOnce(allowClassification,
				`reviewed-at: "2026-08-09 23:20:00 UTC"`, `reviewed-at: "yesterday"`),
			wantErr: "reviewed-at",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadClassificationFromString(t, tc.body)
			if err == nil {
				t.Fatal("LoadClassification accepted an invalid allow entry")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckReportsUnclassifiedFindingsByName(t *testing.T) {
	res := result([]Finding{
		{Rule: "proc.promote", Severity: SeverityForbidden, Symbol: "pkg.A", Path: "a.go", Line: 3, Col: 2},
	}, "pkg.A")
	rules := ruleSetForFindings(res)
	cls := &Classification{Schema: classificationSchema}

	report := Check(rules, res, cls, emptyBaseline(rules, res), "M0")

	if len(report.Violations) == 0 {
		t.Fatal("an unclassified forbidden finding produced no violation")
	}
	joined := strings.Join(report.Violations, "\n")
	if !strings.Contains(joined, "pkg.A") {
		t.Errorf("the violation does not name the new hit:\n%s", joined)
	}
	if !strings.Contains(joined, "unclassified") {
		t.Errorf("the violation does not say the hit is unclassified:\n%s", joined)
	}
}

func TestCheckDoesNotRequireClassificationForObserveFindings(t *testing.T) {
	res := result([]Finding{
		{Rule: "role.current-primary-read", Severity: SeverityObserve, Symbol: "pkg.B", Path: "b.go"},
	}, "pkg.B")
	rules := ruleSetForFindings(res)

	report := Check(rules, res, &Classification{Schema: classificationSchema}, emptyBaseline(rules, res), "M0")

	if len(report.Violations) != 0 {
		t.Errorf("an observe finding produced violations: %v", report.Violations)
	}
}

func TestCheckHonoursTheAllowList(t *testing.T) {
	res := result([]Finding{
		{
			Rule: "sync.standby-names", Severity: SeverityForbidden,
			Symbol: "pkg/postgres.fixed", Path: "pkg/postgres/configuration.go", Package: "pkg/postgres",
		},
	}, "pkg/postgres.fixed")
	rules := ruleSetForFindings(res)
	cls := &Classification{
		Schema: classificationSchema,
		Allow: []AllowEntry{{
			Rule:       "sync.standby-names",
			Package:    "pkg/postgres",
			Reason:     "The literal is a denylist key that rejects user configuration.",
			Owner:      "NikolayS",
			ReviewedAt: "2026-08-09 23:20:00 UTC",
		}},
	}

	report := Check(rules, res, cls, emptyBaseline(rules, res), "M0")

	if len(report.Violations) != 0 {
		t.Errorf("an allowed finding produced violations: %v", report.Violations)
	}
}

func TestCheckExpiresAllowListEntries(t *testing.T) {
	res := result(nil)
	rules := ruleSetForFindings(res)
	cls := &Classification{
		Schema: classificationSchema,
		Allow: []AllowEntry{{
			Rule:       "sync.standby-names",
			Package:    "pkg/postgres",
			Reason:     "Temporary exception while the reserved parameter table is rebuilt.",
			Owner:      "NikolayS",
			ReviewedAt: "2026-08-09 23:20:00 UTC",
			Until:      "M1",
		}},
	}

	report := Check(rules, res, cls, emptyBaseline(rules, res), "M2")

	if !containsSubstring(report.Violations, "expired") {
		t.Errorf("an allowlist entry past its milestone did not expire: %v", report.Violations)
	}
}

func TestCheckComparesAgainstTheBaseline(t *testing.T) {
	rules := ruleSet("proc.promote")
	forbidden := func(symbol string, n int) []Finding {
		out := make([]Finding, 0, n)
		for range n {
			out = append(out, Finding{
				Rule: "proc.promote", Severity: SeverityForbidden,
				Symbol: symbol, Path: "a.go", Line: 1,
			})
		}
		return out
	}
	classified := &Classification{
		Schema: classificationSchema,
		Entries: []Entry{{
			Path: "a.go", Symbol: "pkg.A", Class: "disable", Dest: "disabled",
			Rationale: "Disabled because Patroni owns promotion entirely.",
			Owner:     "NikolayS", ReviewedAt: "2026-08-09 23:20:00 UTC",
		}},
	}
	baseline := &Baseline{
		Schema:  baselineSchema,
		Total:   1,
		Buckets: []Bucket{{Rule: "proc.promote", Symbol: "pkg.A", Count: 1}},
	}

	cases := []struct {
		name          string
		findings      []Finding
		wantViolation string
		wantHygiene   string
	}{
		{name: "count unchanged", findings: forbidden("pkg.A", 1)},
		{name: "count grew", findings: forbidden("pkg.A", 2), wantViolation: "grew 1 -> 2"},
		{name: "count shrank", findings: forbidden("pkg.A", 0), wantHygiene: "stale baseline entry"},
		{name: "bucket shrank but survives", findings: forbidden("pkg.A", 1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := result(tc.findings, "pkg.A")
			report := Check(rules, res, classified, baselineWithInputs(baseline, rules, res), "M0")

			if tc.wantViolation == "" {
				if len(report.Violations) != 0 {
					t.Errorf("unexpected violations: %v", report.Violations)
				}
			} else if !containsSubstring(report.Violations, tc.wantViolation) {
				t.Errorf("violations %v do not contain %q", report.Violations, tc.wantViolation)
			}

			if tc.wantHygiene != "" && !containsSubstring(report.Hygiene, tc.wantHygiene) {
				t.Errorf("hygiene %v does not contain %q", report.Hygiene, tc.wantHygiene)
			}
		})
	}
}

func TestCheckFlagsAForbiddenCallInAFileAlreadyOnTheBaseline(t *testing.T) {
	// The attack the baseline has to survive: a merge adds a brand new forbidden
	// call inside a file that is already full of baselined ones. Because buckets
	// are keyed by symbol rather than by file, the new function is a new bucket.
	baseline := &Baseline{
		Schema:  baselineSchema,
		Total:   1,
		Buckets: []Bucket{{Rule: "proc.promote", Symbol: "pkg.Old", Count: 1}},
	}
	cls := &Classification{
		Schema: classificationSchema,
		Entries: []Entry{
			{
				Path: "a.go", Symbol: "pkg.Old", Class: "disable", Dest: "disabled",
				Rationale: "Existing promotion path scheduled for removal in M1.",
				Owner:     "NikolayS", ReviewedAt: "2026-08-09 23:20:00 UTC",
			},
			{
				Path: "a.go", Symbol: "pkg.New", Class: "disable", Dest: "disabled",
				Rationale: "Newly merged promotion path, also owned by Patroni.",
				Owner:     "NikolayS", ReviewedAt: "2026-08-09 23:20:00 UTC",
			},
		},
	}
	findings := []Finding{
		{Rule: "proc.promote", Severity: SeverityForbidden, Symbol: "pkg.Old", Path: "a.go", Line: 10},
		{Rule: "proc.promote", Severity: SeverityForbidden, Symbol: "pkg.New", Path: "a.go", Line: 40},
	}
	rules := ruleSet("proc.promote")
	res := result(findings, "pkg.Old", "pkg.New")

	report := Check(rules, res, cls, baselineWithInputs(baseline, rules, res), "M0")

	if !containsSubstring(report.Violations, "new forbidden call") {
		t.Errorf("a new forbidden call in an already-baselined file was not reported: %v", report.Violations)
	}
	if !containsSubstring(report.Violations, "pkg.New") {
		t.Errorf("the violation does not name the new symbol: %v", report.Violations)
	}
}

func TestBuildBaselineCountsOnlyForbiddenFindings(t *testing.T) {
	findings := []Finding{
		{Rule: "proc.promote", Severity: SeverityForbidden, Symbol: "pkg.A"},
		{Rule: "proc.promote", Severity: SeverityForbidden, Symbol: "pkg.A"},
		{Rule: "guard.call", Severity: SeverityInventory, Symbol: "pkg.B"},
		{Rule: "role.read", Severity: SeverityObserve, Symbol: "pkg.C"},
	}
	res := result(findings)
	rules := ruleSetForFindings(res)

	baseline := BuildBaseline(rules, res, findings, 1, "abc123", "2026-08-09 23:20:00 UTC")

	if baseline.Total != 2 {
		t.Errorf("total = %d, want 2", baseline.Total)
	}
	if len(baseline.Buckets) != 1 || baseline.Buckets[0].Count != 2 {
		t.Errorf("buckets = %+v, want one bucket of 2", baseline.Buckets)
	}
	if baseline.TotalsByRule["proc.promote"] != 2 {
		t.Errorf("totals_by_rule = %v", baseline.TotalsByRule)
	}
	if baseline.AllowedTotal != 1 {
		t.Errorf("allowed_total = %d, want 1", baseline.AllowedTotal)
	}
	if differences := inputDifferences(baseline.Inputs, Fingerprint(rules, res)); len(differences) != 0 {
		t.Errorf("baseline inputs do not match the measurement: %v", differences)
	}
}

func TestLoadBaselineRejectsDishonestCounts(t *testing.T) {
	valid := `schema: cnpatroni-authority-baseline/v2
generated_from: abc123
generated_at: 2026-08-09 23:20:00 UTC
total: 1
allowed_total: 0
totals_by_rule:
  proc.promote: 1
inputs:
  scope:
    roots: [.]
    exclude_paths: []
    exclude_generated: false
    include_tests: false
  rules:
    - id: proc.promote
      severity: forbidden
      matchers: ["call:example.com/pg.Promote"]
  packages: 1
  files: 1
buckets:
  - rule: proc.promote
    symbol: pkg.A
    count: 1
`
	cases := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "total disagrees with buckets",
			body:    replaceOnce(valid, "total: 1", "total: 2"),
			wantErr: "total is 2, but buckets sum to 1",
		},
		{
			name:    "rule total disagrees with buckets",
			body:    replaceOnce(valid, "proc.promote: 1", "proc.promote: 2"),
			wantErr: "totals_by_rule for proc.promote is 2, but buckets sum to 1",
		},
		{
			name: "duplicate bucket",
			body: valid + `  - rule: proc.promote
    symbol: pkg.A
    count: 1
`,
			wantErr: "duplicate bucket",
		},
		{
			name:    "zero bucket count",
			body:    replaceOnce(valid, "count: 1", "count: 0"),
			wantErr: "count must be positive",
		},
		{
			name:    "missing bucket symbol",
			body:    replaceOnce(valid, "symbol: pkg.A", "symbol: \"\""),
			wantErr: "has no symbol",
		},
		{
			name:    "rule total absent from input fingerprint",
			body:    replaceOnce(valid, "    - id: proc.promote\n      severity: forbidden\n      matchers: [\"call:example.com/pg.Promote\"]\n", "    []\n"),
			wantErr: "absent from inputs.rules",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "baseline.yaml")
			writeFile(t, path, tc.body)

			_, err := LoadBaseline(path)
			if err == nil {
				t.Fatal("LoadBaseline accepted a dishonest baseline")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestBaselineWriteRoundTripsExactData(t *testing.T) {
	findings := []Finding{
		{Rule: "role.primary", Severity: SeverityForbidden, Symbol: "pkg.B"},
		{Rule: "proc.promote", Severity: SeverityForbidden, Symbol: "pkg.A"},
	}
	res := result(findings)
	rules := ruleSet("role.primary", "proc.promote")
	baseline := BuildBaseline(rules, res, findings, 0, "abc123", "2026-08-09 23:20:00 UTC")
	path := filepath.Join(t.TempDir(), "baseline.yaml")

	if err := baseline.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.HasPrefix(string(body), "# Generated by hack/cnpatroni/audit;") {
		t.Errorf("baseline header is missing: %q", body)
	}

	loaded, err := LoadBaseline(path)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	if loaded.GeneratedFrom != "abc123" || loaded.GeneratedAt != "2026-08-09 23:20:00 UTC" {
		t.Errorf("provenance = %q at %q", loaded.GeneratedFrom, loaded.GeneratedAt)
	}
	if loaded.Total != 2 || loaded.TotalsByRule["proc.promote"] != 1 ||
		loaded.TotalsByRule["role.primary"] != 1 {
		t.Errorf("loaded totals = total %d, by rule %v", loaded.Total, loaded.TotalsByRule)
	}
	if len(loaded.Buckets) != 2 || loaded.Buckets[0].Rule != "proc.promote" ||
		loaded.Buckets[0].Symbol != "pkg.A" || loaded.Buckets[1].Rule != "role.primary" ||
		loaded.Buckets[1].Symbol != "pkg.B" {
		t.Errorf("loaded buckets = %+v, want deterministic rule and symbol order", loaded.Buckets)
	}
}

func TestBaselineWriteHeaderReferencesOnlyExistingDocs(t *testing.T) {
	baseline := BuildBaseline(nil, "abc123", "2026-08-09 23:20:00 UTC")
	path := filepath.Join(t.TempDir(), "baseline.yaml")
	if err := baseline.Write(path); err != nil {
		t.Fatalf("Write: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var headerLines []string
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "#") {
			break
		}
		headerLines = append(headerLines, line)
	}
	header := strings.Join(headerLines, "\n")
	docPath := regexp.MustCompile(`docs/cnpatroni/[[:alnum:]_./-]+\.md`)
	root := repositoryRoot(t)
	for _, referencedPath := range docPath.FindAllString(header, -1) {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(referencedPath)))
		if err != nil {
			t.Errorf("generated baseline header references documentation path %q that is absent from the working tree: %v",
				referencedPath, err)
			continue
		}
		if !info.Mode().IsRegular() {
			t.Errorf("generated baseline header references documentation path %q, but it is not a file", referencedPath)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	// Go tests run from the package directory, so walk upward to find the
	// repository-relative audit module rather than assuming the caller's cwd.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		modulePath := filepath.Join(dir, "hack", "cnpatroni", "audit", "go.mod")
		if info, statErr := os.Stat(modulePath); statErr == nil && info.Mode().IsRegular() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root not found above %s", dir)
		}
		dir = parent
	}
}

func TestBaselineIOReportsSpecificErrors(t *testing.T) {
	t.Run("missing input", func(t *testing.T) {
		_, err := LoadBaseline(filepath.Join(t.TempDir(), "missing.yaml"))
		if err == nil || !strings.Contains(err.Error(), "reading baseline") {
			t.Fatalf("error = %v, want a baseline read error", err)
		}
	})

	t.Run("malformed yaml", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "baseline.yaml")
		writeFile(t, path, "schema: [\n")
		_, err := LoadBaseline(path)
		if err == nil || !strings.Contains(err.Error(), "parsing baseline yaml") {
			t.Fatalf("error = %v, want a baseline parse error", err)
		}
	})

	t.Run("missing output directory", func(t *testing.T) {
		res := result(nil)
		rules := ruleSetForFindings(res)
		baseline := BuildBaseline(rules, res, nil, 0, "abc123", "2026-08-09 23:20:00 UTC")
		err := baseline.Write(filepath.Join(t.TempDir(), "missing", "baseline.yaml"))
		if err == nil || !strings.Contains(err.Error(), "writing baseline") {
			t.Fatalf("error = %v, want a baseline write error", err)
		}
	})
}

func TestCompareBaselinesFailsOnlyWhenTheBaselineGrows(t *testing.T) {
	base := &Baseline{Schema: baselineSchema, Total: 300, TotalsByRule: map[string]int{"proc.promote": 3}}

	cases := []struct {
		name     string
		head     *Baseline
		wantGrew bool
		wantLine string
	}{
		{
			name:     "shrunk",
			head:     &Baseline{Schema: baselineSchema, Total: 275, TotalsByRule: map[string]int{"proc.promote": 2}},
			wantLine: "300 -> 275 (-25)",
		},
		{
			name:     "unchanged",
			head:     &Baseline{Schema: baselineSchema, Total: 300, TotalsByRule: map[string]int{"proc.promote": 3}},
			wantLine: "300 -> 300 (+0)",
		},
		{
			name:     "total grew",
			head:     &Baseline{Schema: baselineSchema, Total: 301, TotalsByRule: map[string]int{"proc.promote": 4}},
			wantGrew: true,
			wantLine: "300 -> 301",
		},
		{
			name:     "one rule grew while the total fell",
			head:     &Baseline{Schema: baselineSchema, Total: 290, TotalsByRule: map[string]int{"proc.promote": 9}},
			wantGrew: true,
			wantLine: "proc.promote",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grew, lines := CompareBaselines(base, tc.head)

			if grew != tc.wantGrew {
				t.Errorf("grew = %v, want %v (%v)", grew, tc.wantGrew, lines)
			}
			if !containsSubstring(lines, tc.wantLine) {
				t.Errorf("lines %v do not contain %q", lines, tc.wantLine)
			}
		})
	}
}

func TestCompareBaselinesRejectsARenamedBucket(t *testing.T) {
	base := &Baseline{
		Schema:       baselineSchema,
		Total:        1,
		TotalsByRule: map[string]int{"proc.promote": 1},
		Buckets: []Bucket{{
			Rule: "proc.promote", Symbol: "pkg.OldName", Count: 1,
		}},
	}
	head := &Baseline{
		Schema:       baselineSchema,
		Total:        1,
		TotalsByRule: map[string]int{"proc.promote": 1},
		Buckets: []Bucket{{
			Rule: "proc.promote", Symbol: "pkg.NewName", Count: 1,
		}},
	}

	grew, lines := CompareBaselines(base, head)

	if !grew {
		t.Fatalf("renaming a forbidden bucket passed the ratchet: %v", lines)
	}
	if !containsSubstring(lines, "pkg.NewName") {
		t.Errorf("comparison does not name the new bucket: %v", lines)
	}
}

func TestCompareBaselinesRejectsARemovedRule(t *testing.T) {
	base := &Baseline{
		Schema: baselineSchema, Total: 113,
		TotalsByRule: map[string]int{"proc.promote": 1, "proc.startstop": 112},
		Inputs: Inputs{Rules: []RuleFingerprint{
			{ID: "proc.promote", Severity: SeverityForbidden, Matchers: []string{"call:example.com/pg.Promote"}},
			{ID: "proc.startstop", Severity: SeverityForbidden, Matchers: []string{"call:example.com/pg.Start"}},
		}},
	}
	head := &Baseline{
		Schema: baselineSchema, Total: 112,
		TotalsByRule: map[string]int{"proc.startstop": 112},
		Inputs: Inputs{Rules: []RuleFingerprint{
			{ID: "proc.startstop", Severity: SeverityForbidden, Matchers: []string{"call:example.com/pg.Start"}},
		}},
	}

	grew, lines := CompareBaselines(base, head)

	if !grew {
		t.Fatalf("removing the proc.promote rule passed the ratchet: %v", lines)
	}
	if !containsSubstring(lines, "rule proc.promote was removed from the rule set") {
		t.Errorf("comparison does not name the removed rule: %v", lines)
	}
}

func TestCompareBaselinesRejectsANarrowedMatcherSet(t *testing.T) {
	base := &Baseline{
		Schema: baselineSchema, Total: 2, TotalsByRule: map[string]int{"proc.promote": 2},
		Inputs: Inputs{Rules: []RuleFingerprint{{
			ID: "proc.promote", Severity: SeverityForbidden,
			Matchers: []string{"call:example.com/pg.Promote", "literal:promote_trigger_file"},
		}}},
	}
	head := &Baseline{
		Schema: baselineSchema, Total: 1, TotalsByRule: map[string]int{"proc.promote": 1},
		Inputs: Inputs{Rules: []RuleFingerprint{{
			ID: "proc.promote", Severity: SeverityForbidden,
			Matchers: []string{"call:example.com/pg.Promote"},
		}}},
	}

	grew, lines := CompareBaselines(base, head)

	if !grew {
		t.Fatalf("narrowing proc.promote's matcher set passed the ratchet: %v", lines)
	}
	if !containsSubstring(lines, "rule proc.promote no longer matches literal:promote_trigger_file") {
		t.Errorf("comparison does not name the removed matcher: %v", lines)
	}
}

func TestCompareBaselinesRejectsANarrowedCallArgumentFilter(t *testing.T) {
	base := &Baseline{
		Schema: baselineSchema, Total: 4, TotalsByRule: map[string]int{"proc.startstop": 4},
		Inputs: Inputs{Rules: []RuleFingerprint{{
			ID: "proc.startstop", Severity: SeverityForbidden,
			Matchers: []string{"call:example.com/pg.Start"},
		}}},
	}
	head := &Baseline{
		Schema: baselineSchema, Total: 0, TotalsByRule: map[string]int{},
		Inputs: Inputs{Rules: []RuleFingerprint{{
			ID: "proc.startstop", Severity: SeverityForbidden,
			Matchers: []string{"call:example.com/pg.Start#arg=zzz-never"},
		}}},
	}

	grew, lines := CompareBaselines(base, head)

	if !grew {
		t.Fatalf("narrowing proc.startstop to an argument that never matches passed the ratchet: %v", lines)
	}
	if !containsSubstring(lines, "no longer matches call:example.com/pg.Start") {
		t.Errorf("comparison does not name the removed unfiltered call matcher: %v", lines)
	}
}

func TestFingerprintIncludesCallArgumentFiltersInEveryCallMatcher(t *testing.T) {
	res := &ScanResult{}
	withoutFilter := &RuleSet{Rules: []Rule{{
		ID: "proc.startstop", Severity: SeverityForbidden,
		Calls: []string{"example.com/pg.Stop", "example.com/pg.Start"},
	}}}
	withFilter := &RuleSet{Rules: []Rule{{
		ID: "proc.startstop", Severity: SeverityForbidden,
		Calls:           []string{"example.com/pg.Stop", "example.com/pg.Start"},
		CallArgContains: []string{"zzz-never", "postgres", "postgres"},
	}}}

	before := Fingerprint(withoutFilter, res).Rules[0].Matchers
	after := Fingerprint(withFilter, res).Rules[0].Matchers

	if strings.Join(before, "\n") == strings.Join(after, "\n") {
		t.Fatalf("adding call_arg_contains did not change the call matcher identities: %v", after)
	}
	want := []string{
		"call:example.com/pg.Start#arg=postgres,zzz-never",
		"call:example.com/pg.Stop#arg=postgres,zzz-never",
	}
	if strings.Join(after, "\n") != strings.Join(want, "\n") {
		t.Errorf("filtered call matchers = %v, want %v", after, want)
	}
}

func TestCompareBaselinesRejectsScopeNarrowing(t *testing.T) {
	baseScope := ScopeFingerprint{
		Roots: []string{".", "extra"}, ExcludePaths: []string{"vendor/**"},
		ExcludeGenerated: false, IncludeTests: true,
	}
	cases := []struct {
		name      string
		headScope ScopeFingerprint
		want      string
	}{
		{
			name: "exclude path added",
			headScope: ScopeFingerprint{
				Roots: []string{".", "extra"}, ExcludePaths: []string{"generated/**", "vendor/**"},
				ExcludeGenerated: false, IncludeTests: true,
			},
			want: "exclude path generated/** was added",
		},
		{
			name: "root removed",
			headScope: ScopeFingerprint{
				Roots: []string{"."}, ExcludePaths: []string{"vendor/**"},
				ExcludeGenerated: false, IncludeTests: true,
			},
			want: "scan root extra was removed",
		},
		{
			name: "generated files excluded",
			headScope: ScopeFingerprint{
				Roots: []string{".", "extra"}, ExcludePaths: []string{"vendor/**"},
				ExcludeGenerated: true, IncludeTests: true,
			},
			want: "generated files are now excluded",
		},
		{
			name: "test files excluded",
			headScope: ScopeFingerprint{
				Roots: []string{".", "extra"}, ExcludePaths: []string{"vendor/**"},
				ExcludeGenerated: false, IncludeTests: false,
			},
			want: "test files are now excluded",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := &Baseline{Schema: baselineSchema, Inputs: Inputs{Scope: baseScope}}
			head := &Baseline{Schema: baselineSchema, Inputs: Inputs{Scope: tc.headScope}}

			grew, lines := CompareBaselines(base, head)

			if !grew {
				t.Fatalf("scope narrowing passed the ratchet: %v", lines)
			}
			if !containsSubstring(lines, tc.want) {
				t.Errorf("comparison does not name %q: %v", tc.want, lines)
			}
		})
	}
}

func TestCompareBaselinesRejectsMoreAllowListSuppression(t *testing.T) {
	base := &Baseline{Schema: baselineSchema, AllowedTotal: 2}
	head := &Baseline{Schema: baselineSchema, AllowedTotal: 5}

	grew, lines := CompareBaselines(base, head)

	if !grew {
		t.Fatalf("increasing allow-list suppression passed the ratchet: %v", lines)
	}
	if !containsSubstring(lines,
		"3 more findings are suppressed by the allow list; debt removed by exemption is not debt fixed") {
		t.Errorf("comparison does not explain the increased suppression: %v", lines)
	}
}

func TestCompareBaselinesAllowsGrowthAttributableToATightenedRule(t *testing.T) {
	baseRules := []RuleFingerprint{
		{ID: "proc.promote", Severity: SeverityForbidden, Matchers: []string{"call:example.com/pg.Promote"}},
		{ID: "proc.startstop", Severity: SeverityForbidden, Matchers: []string{"call:example.com/pg.Start"}},
	}
	tightenedRules := []RuleFingerprint{
		{
			ID: "proc.promote", Severity: SeverityForbidden,
			Matchers: []string{"call:example.com/pg.Promote", "literal:promote_trigger_file"},
		},
		{ID: "proc.startstop", Severity: SeverityForbidden, Matchers: []string{"call:example.com/pg.Start"}},
	}
	base := &Baseline{
		Schema: baselineSchema, Total: 2,
		TotalsByRule: map[string]int{"proc.promote": 1, "proc.startstop": 1},
		Inputs:       Inputs{Rules: baseRules},
		Buckets: []Bucket{
			{Rule: "proc.promote", Symbol: "pkg.Promote", Count: 1},
			{Rule: "proc.startstop", Symbol: "pkg.Start", Count: 1},
		},
	}
	head := &Baseline{
		Schema: baselineSchema, Total: 3,
		TotalsByRule: map[string]int{"proc.promote": 2, "proc.startstop": 1},
		Inputs:       Inputs{Rules: tightenedRules},
		Buckets: []Bucket{
			{Rule: "proc.promote", Symbol: "pkg.Promote", Count: 2},
			{Rule: "proc.startstop", Symbol: "pkg.Start", Count: 1},
		},
	}

	grew, lines := CompareBaselines(base, head)
	if grew {
		t.Fatalf("growth caused by the tightened proc.promote rule failed the ratchet: %v", lines)
	}
	if !containsSubstring(lines, "attributable growth: proc.promote 1 -> 2") {
		t.Errorf("comparison does not mark the growth as attributable: %v", lines)
	}
	if !containsSubstring(lines, "rules diff") {
		t.Errorf("attribution does not tell the reviewer to read the rules diff: %v", lines)
	}

	unrelatedGrowth := &Baseline{
		Schema: baselineSchema, Total: 3,
		TotalsByRule: map[string]int{"proc.promote": 1, "proc.startstop": 2},
		Inputs:       Inputs{Rules: tightenedRules},
		Buckets: []Bucket{
			{Rule: "proc.promote", Symbol: "pkg.Promote", Count: 1},
			{Rule: "proc.startstop", Symbol: "pkg.Start", Count: 2},
		},
	}
	grew, lines = CompareBaselines(base, unrelatedGrowth)
	if !grew {
		t.Fatalf("growth in an untightened rule passed through another rule's attribution: %v", lines)
	}
}

func TestCheckRejectsABaselineGeneratedUnderDifferentRules(t *testing.T) {
	const symbol = "example.com/controller.handlePromotion"
	res := result([]Finding{{
		Rule: "role.current-primary-read", Severity: SeverityObserve, Symbol: symbol,
	}}, symbol)
	liveRules := &RuleSet{
		Scope: Scope{Roots: []string{"."}},
		Rules: []Rule{{
			ID: "role.current-primary-read", Severity: SeverityObserve, Literals: []string{"currentPrimary"},
		}},
	}
	inputs := Fingerprint(liveRules, res)
	inputs.Rules = append(inputs.Rules, RuleFingerprint{
		ID: "proc.promote", Severity: SeverityForbidden,
		Matchers: []string{"call:example.com/pg.Promote"},
	})
	baseline := &Baseline{
		Schema: baselineSchema, Total: 1, TotalsByRule: map[string]int{"proc.promote": 1}, Inputs: inputs,
		Buckets: []Bucket{{Rule: "proc.promote", Symbol: symbol, Count: 1}},
	}
	cls := &Classification{Schema: classificationSchema, Entries: []Entry{{Symbol: symbol}}}

	report := Check(liveRules, res, cls, baseline, "M0")

	if !containsSubstring(report.Violations, "different rule set or scope") {
		t.Fatalf("a baseline generated under a different rule set produced no violation: %v", report.Violations)
	}
	if !containsSubstring(report.Violations, "proc.promote") ||
		!containsSubstring(report.Violations, "recorded baseline") {
		t.Errorf("coherence violation does not make clear which side lacks proc.promote: %v", report.Violations)
	}
	if !containsSubstring(report.Hygiene, "stale classification: "+symbol) {
		t.Errorf("classification made stale by the removed rule was not reported: %v", report.Hygiene)
	}
}

func TestCheckVerifiesGuardWiring(t *testing.T) {
	rules := loadFixtureRules(t)
	res := scanResultFixture(t, "mod-hits", rules)

	entry := func(symbol, guard string) Entry {
		return Entry{
			Path: "ctrl/guarded.go", Symbol: symbol, Class: "disable", Dest: "disabled",
			Rationale: "Disabled in CloudNativePatroni; the guard replaces the body.",
			Owner:     "NikolayS", ReviewedAt: "2026-08-09 23:20:00 UTC", Guard: guard,
		}
	}

	cases := []struct {
		name   string
		symbol string
		want   string
	}{
		{
			name:   "guard call site agrees with the enclosing symbol",
			symbol: "example.com/hits/ctrl.GuardedOK",
		},
		{
			name:   "guard call site passes another symbol",
			symbol: "example.com/hits/ctrl.GuardedWrongLiteral",
			want:   "must equal the enclosing symbol",
		},
		{
			name:   "guard required but never called",
			symbol: "example.com/hits/ctrl.GuardedMissing",
			want:   "does not call guard.Forbidden",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cls := &Classification{Schema: classificationSchema, Entries: []Entry{entry(tc.symbol, "required")}}
			report := Check(rules, res, cls, emptyBaseline(rules, res), "M0")

			if tc.want == "" {
				for _, message := range []string{"guard: required but", "op literal must equal"} {
					if containsSubstring(report.Violations, message) {
						t.Errorf("correct guard wiring produced %q: %v", message, report.Violations)
					}
				}
				return
			}
			if !containsSubstring(report.Violations, tc.want) {
				t.Errorf("violations %v do not contain %q", report.Violations, tc.want)
			}
		})
	}
}

func TestCheckReportsStaleClassificationsAsHygiene(t *testing.T) {
	res := result(nil)
	rules := ruleSetForFindings(res)
	cls := &Classification{
		Schema: classificationSchema,
		Entries: []Entry{{
			Path: "gone.go", Symbol: "pkg.Vanished", Class: "disable", Dest: "disabled",
			Rationale: "Classified when the symbol still existed upstream.",
			Owner:     "NikolayS", ReviewedAt: "2026-08-09 23:20:00 UTC",
		}},
	}

	report := Check(rules, res, cls, emptyBaseline(rules, res), "M0")

	if !containsSubstring(report.Hygiene, "stale classification") {
		t.Errorf("a classification for a vanished symbol was not reported: %v", report.Hygiene)
	}
	if len(report.Violations) != 0 {
		t.Errorf("a stale classification must be hygiene, not a policy violation: %v", report.Violations)
	}
}

func TestCheckReportsStaleResponsibilityAnchors(t *testing.T) {
	res := result(nil)
	rules := ruleSetForFindings(res)
	cls := &Classification{
		Schema: classificationSchema,
		Responsibilities: []Responsibility{{
			Responsibility: "Exec and supervise the postmaster",
			Dest:           "disabled",
			Rationale:      "Patroni execs postgres in the same container and cgroup.",
			Owner:          "NikolayS",
			ReviewedAt:     "2026-08-09 23:20:00 UTC",
			Anchors:        []string{"pkg.Gone"},
		}},
	}

	report := Check(rules, res, cls, emptyBaseline(rules, res), "M0")

	if !containsSubstring(report.Hygiene, "stale responsibility anchor") {
		t.Errorf("a vanished anchor was not reported: %v", report.Hygiene)
	}
}

// result builds a ScanResult whose declared-symbol set is exactly symbols, so a
// test can distinguish "the symbol is gone" from "the symbol has no finding".
func result(findings []Finding, symbols ...string) *ScanResult {
	res := &ScanResult{Findings: findings, Symbols: map[string]bool{}, GuardOps: map[string][]string{}}
	for _, s := range symbols {
		res.Symbols[s] = true
	}
	return res
}

// baselineWithInputs stamps the fingerprint of the rule set and scan a test
// baseline is meant to have been generated from, so the coherence check sees a
// matching pair and the test stays about what it was about.
func baselineWithInputs(b *Baseline, rules *RuleSet, res *ScanResult) *Baseline {
	copy := *b
	copy.Inputs = Fingerprint(rules, res)
	return &copy
}

func emptyBaseline(rules *RuleSet, res *ScanResult) *Baseline {
	return baselineWithInputs(&Baseline{Schema: baselineSchema, TotalsByRule: map[string]int{}}, rules, res)
}

func ruleSet(ids ...string) *RuleSet {
	rules := make([]Rule, 0, len(ids))
	for _, id := range ids {
		rules = append(rules, Rule{ID: id, Severity: SeverityForbidden, Literals: []string{"test:" + id}})
	}
	return &RuleSet{Scope: Scope{Roots: []string{"."}}, Rules: rules}
}

func ruleSetForFindings(res *ScanResult) *RuleSet {
	seen := map[string]bool{}
	rules := &RuleSet{Scope: Scope{Roots: []string{"."}}}
	for _, finding := range res.Findings {
		if seen[finding.Rule] {
			continue
		}
		seen[finding.Rule] = true
		rules.Rules = append(rules.Rules, Rule{
			ID: finding.Rule, Severity: finding.Severity, Literals: []string{"test:" + finding.Rule},
		})
	}
	return rules
}

func containsSubstring(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func replaceOnce(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}
