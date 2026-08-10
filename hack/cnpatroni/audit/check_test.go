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

func TestCheckReportsUnclassifiedFindingsByName(t *testing.T) {
	res := result([]Finding{
		{Rule: "proc.promote", Severity: SeverityForbidden, Symbol: "pkg.A", Path: "a.go", Line: 3, Col: 2},
	}, "pkg.A")
	cls := &Classification{Schema: classificationSchema}

	report := Check(res, cls, emptyBaseline(), "M0")

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

	report := Check(res, &Classification{Schema: classificationSchema}, emptyBaseline(), "M0")

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

	report := Check(res, cls, emptyBaseline(), "M0")

	if len(report.Violations) != 0 {
		t.Errorf("an allowed finding produced violations: %v", report.Violations)
	}
}

func TestCheckExpiresAllowListEntries(t *testing.T) {
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

	report := Check(result(nil), cls, emptyBaseline(), "M2")

	if !containsSubstring(report.Violations, "expired") {
		t.Errorf("an allowlist entry past its milestone did not expire: %v", report.Violations)
	}
}

func TestCheckComparesAgainstTheBaseline(t *testing.T) {
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
			report := Check(result(tc.findings, "pkg.A"), classified, baseline, "M0")

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

	report := Check(result(findings, "pkg.Old", "pkg.New"), cls, baseline, "M0")

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

	baseline := BuildBaseline(findings, "abc123", "2026-08-09 23:20:00 UTC")

	if baseline.Total != 2 {
		t.Errorf("total = %d, want 2", baseline.Total)
	}
	if len(baseline.Buckets) != 1 || baseline.Buckets[0].Count != 2 {
		t.Errorf("buckets = %+v, want one bucket of 2", baseline.Buckets)
	}
	if baseline.TotalsByRule["proc.promote"] != 2 {
		t.Errorf("totals_by_rule = %v", baseline.TotalsByRule)
	}
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
			head:     &Baseline{Schema: baselineSchema, Total: 301, TotalsByRule: map[string]int{"proc.promote": 3}},
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

func TestCheckVerifiesGuardWiring(t *testing.T) {
	res := scanResultFixture(t, "mod-hits", loadFixtureRules(t))

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
			report := Check(res, cls, emptyBaseline(), "M0")

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
	cls := &Classification{
		Schema: classificationSchema,
		Entries: []Entry{{
			Path: "gone.go", Symbol: "pkg.Vanished", Class: "disable", Dest: "disabled",
			Rationale: "Classified when the symbol still existed upstream.",
			Owner:     "NikolayS", ReviewedAt: "2026-08-09 23:20:00 UTC",
		}},
	}

	report := Check(result(nil), cls, emptyBaseline(), "M0")

	if !containsSubstring(report.Hygiene, "stale classification") {
		t.Errorf("a classification for a vanished symbol was not reported: %v", report.Hygiene)
	}
	if len(report.Violations) != 0 {
		t.Errorf("a stale classification must be hygiene, not a policy violation: %v", report.Violations)
	}
}

func TestCheckReportsStaleResponsibilityAnchors(t *testing.T) {
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

	report := Check(result(nil), cls, emptyBaseline(), "M0")

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

func emptyBaseline() *Baseline {
	return &Baseline{Schema: baselineSchema, TotalsByRule: map[string]int{}}
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
