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
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/baseline"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/boundary"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/gittest"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/gitx"
)

// validateFixture is a repository whose fork base carries the upstream files
// and whose HEAD carries the CloudNativePatroni edits.
type validateFixture struct {
	git      *gittest.Fixture
	repo     *gitx.Repo
	baseline *baseline.Baseline
}

func newValidateFixture(t *testing.T) *validateFixture {
	t.Helper()

	f := gittest.New(t)
	f.Write("pkg/management/postgres/instance.go", "package postgres\n\n// pg_ctl lives here.\n")
	f.Write("pkg/specs/pods.go", "package specs\n")
	f.Write("internal/controller/replicas.go", "package controller\n\n// TargetPrimary\n")
	f.Write("pkg/doomed.go", "package pkg\n")
	f.Write("docs/src/faq.md", "# Frequently asked questions\n")
	forkBase := f.Commit("upstream base")

	f.Write("pkg/specs/pods.go", "package specs\n\n// CloudNativePatroni edit.\n")
	f.Commit("fork edit")

	repo, err := gitx.Open(f.Root)
	if err != nil {
		t.Fatalf("gitx.Open: %v", err)
	}

	b, err := baseline.Parse([]byte(`
schema: cnpatroni.io/upstream-baseline/v1
upstream:
  url: https://github.com/cloudnative-pg/cloudnative-pg
  track: main
fork_base:
  commit: ` + forkBase + `
  describe: fixture
  date: "2026-01-01"
last_integrated:
  commit: ` + forkBase + `
  describe: fixture
  date: "2026-01-01"
compatibility:
  cloudnative_pg_minor: "1.30"
  cloudnative_pg_reference_tag: v1.30.0
  claimed: fixture
  verified_at: "2026-01-01"
  unadopted_upstream_commits: 0
not_adopted: []
`))
	if err != nil {
		t.Fatalf("baseline.Parse: %v", err)
	}

	return &validateFixture{git: f, repo: repo, baseline: b}
}

func (vf *validateFixture) validate(t *testing.T, body string, opts boundary.Options) []boundary.Finding {
	t.Helper()

	m, err := boundary.Parse([]byte(body))
	if err != nil {
		t.Fatalf("boundary.Parse: %v", err)
	}
	opts.Repo = vf.repo
	opts.Baseline = vf.baseline

	findings, err := boundary.Validate(m, opts)
	if err != nil {
		t.Fatalf("boundary.Validate: %v", err)
	}

	return findings
}

func findingCodes(findings []boundary.Finding) []string {
	codes := make([]string, 0, len(findings))
	for _, f := range findings {
		codes = append(codes, f.Code)
	}

	return codes
}

func hasCode(findings []boundary.Finding, code string) bool {
	for _, f := range findings {
		if f.Code == code {
			return true
		}
	}

	return false
}

// manifestBody assembles a manifest around a set of rules, so that each test
// changes exactly one thing.
func manifestBody(provisional bool, rules string, required string) string {
	return `
schema: cnpatroni.io/boundary/v1
generated_from: fixture
provisional: ` + boolString(provisional) + `
default_ownership: upstream-untouched
audit_scan:
  include: ["**/*.go"]
  exclude: ["**/*_test.go"]
audit_terms: ["TargetPrimary", "pg_ctl"]
generated_artifacts: ["api/**"]
required_paths: [` + required + `]
rules:
` + rules
}

func boolString(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

const validRules = `  - id: adapt.process-primitives
    ownership: adapted
    paths: ["pkg/management/postgres/instance.go"]
  - id: adapt.specs
    ownership: adapted
    paths: ["pkg/specs/pods.go"]
  - id: disable.election
    ownership: disabled
    mechanism: unreferenced
    paths: ["internal/controller/replicas.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`

func TestValidateHappyPath(t *testing.T) {
	vf := newValidateFixture(t)
	// The catch-all rule must stay last, so every other rule is declared above it.
	body := manifestBody(false, `  - id: keep.doomed
    ownership: upstream-untouched
    paths: ["pkg/doomed.go"]
`+validRules, `"pkg/specs/pods.go"`)

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if len(findings) != 0 {
		t.Fatalf("expected a clean manifest, got %v", findings)
	}
}

func TestValidateRejectsAnUnknownSchema(t *testing.T) {
	vf := newValidateFixture(t)
	body := strings.Replace(manifestBody(false, validRules, ""),
		"cnpatroni.io/boundary/v1", "cnpatroni.io/boundary/v2", 1)

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V1") {
		t.Fatalf("codes = %v, want V1", findingCodes(findings))
	}
}

func TestValidateRequiresTheCatchAllToBeLast(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
  - id: adapt.specs
    ownership: adapted
    paths: ["pkg/specs/pods.go"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V2") {
		t.Fatalf("codes = %v, want V2", findingCodes(findings))
	}
}

func TestValidateRejectsDuplicateLiteralPaths(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: one
    ownership: adapted
    paths: ["pkg/specs/pods.go"]
  - id: two
    ownership: disabled
    mechanism: guarded
    paths: ["pkg/specs/pods.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V3") {
		t.Fatalf("codes = %v, want V3", findingCodes(findings))
	}
}

func TestValidateRejectsIllegalGlobs(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: bad
    ownership: adapted
    paths: ["a**b", "/pkg/x.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	count := 0
	for _, f := range findings {
		if f.Code == "V4" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("got %d V4 findings, want 2: %v", count, findings)
	}
}

func TestValidateRejectsUncompilableAuditScanPatterns(t *testing.T) {
	tests := []struct {
		name        string
		old         string
		replacement string
		list        string
	}{
		{
			name:        "include",
			old:         `include: ["**/*.go"]`,
			replacement: `include: ["**/*.go["]`,
			list:        "audit_scan.include",
		},
		{
			name:        "exclude",
			old:         `exclude: ["**/*_test.go"]`,
			replacement: `exclude: ["**/*_test.go["]`,
			list:        "audit_scan.exclude",
		},
		{
			name:        "generated_artifacts",
			old:         `generated_artifacts: ["api/**"]`,
			replacement: `generated_artifacts: ["api/**["]`,
			list:        "generated_artifacts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vf := newValidateFixture(t)
			body := strings.Replace(manifestBody(false, validRules, ""), tt.old, tt.replacement, 1)

			findings := vf.validate(t, body, boundary.Options{})
			found := false
			for _, f := range findings {
				if f.Code == "V4" && f.Severity == boundary.SeverityError &&
					strings.Contains(f.Message, tt.list) {
					found = true
				}
			}
			if !found {
				t.Fatalf("findings = %v, want a V4 error naming %s", findings, tt.list)
			}
			if code := boundary.ExitCode(findings); code != 4 {
				t.Errorf("exit code = %d, want 4", code)
			}
		})
	}
}

func TestValidateRejectsAnEmptyAuditTermList(t *testing.T) {
	vf := newValidateFixture(t)
	body := strings.Replace(manifestBody(false, validRules, ""),
		`audit_terms: ["TargetPrimary", "pg_ctl"]`, "audit_terms: []", 1)

	findings := vf.validate(t, body, boundary.Options{})
	for _, f := range findings {
		if f.Code == "V14" && f.Severity == boundary.SeverityError &&
			strings.Contains(f.Message, "vocabulary check reports nothing") {
			return
		}
	}
	t.Fatalf("an empty audit_terms list produced no V14 error: %v", findings)
}

func TestAuditTermScanStaysOnWhenAnIncludePatternIsBroken(t *testing.T) {
	vf := newValidateFixture(t)
	body := strings.Replace(manifestBody(false, `  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, ""), `include: ["**/*.go"]`, `include: ["**/*.go["]`, 1)

	findings := vf.validate(t, body, boundary.Options{})
	want := map[string]bool{
		"pkg/management/postgres/instance.go": false,
		"internal/controller/replicas.go":     false,
	}
	for _, f := range findings {
		if f.Code == "V9" {
			if _, ok := want[f.Path]; ok {
				want[f.Path] = true
			}
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("no V9 finding for %s: one uncompilable include pattern silenced the check: %v", path, findings)
		}
	}
}

// A manifest entry for a path this fork no longer carries must be reported,
// rather than silently classifying nothing.
func TestValidateReportsAManifestEntryForAnAbsentPath(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: adapt.ghost
    ownership: adapted
    paths: ["pkg/management/postgres/ghost.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V5") {
		t.Fatalf("codes = %v, want V5", findingCodes(findings))
	}
}

func TestValidateAllowsPlannedOwnedPathsToBeAbsent(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: owned.planned
    ownership: cnpatroni-owned
    state: planned
    paths: ["internal/patroni/**"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{})
	if len(findings) != 0 {
		t.Fatalf("planned paths may be absent, got %v", findings)
	}
}

func TestValidateRejectsPlannedOnTheWrongClass(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: adapt.planned
    ownership: adapted
    state: planned
    paths: ["pkg/specs/pods.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V10") {
		t.Fatalf("codes = %v, want V10", findingCodes(findings))
	}
}

func TestValidateChecksMovedAsideRules(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: disable.moved
    ownership: disabled
    mechanism: moved-aside
    moved_to: .github/workflows-upstream/
    paths: ["pkg/doomed.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V6") {
		t.Fatalf("codes = %v, want V6 because the file was never moved", findingCodes(findings))
	}
}

func TestValidateRejectsADeletedClassPathThatStillExists(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: delete.doomed
    ownership: deleted
    paths: ["pkg/doomed.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V7") {
		t.Fatalf("codes = %v, want V7", findingCodes(findings))
	}
}

func TestValidateAcceptsADeletedClassPathThatWasRemoved(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Remove("pkg/doomed.go")
	vf.git.Commit("remove the doomed file")

	body := manifestBody(false, `  - id: delete.doomed
    ownership: deleted
    paths: ["pkg/doomed.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if len(findings) != 0 {
		t.Fatalf("a deleted path removed from the worktree is legal, got %v", findings)
	}
}

func TestValidateRejectsADeletedClassPathAbsentAtForkBase(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: delete.never-existed
    ownership: deleted
    paths: ["pkg/never-existed.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V7") {
		t.Fatalf("codes = %v, want V7", findingCodes(findings))
	}
}

func TestValidateRequiresDeclaredRequiredPaths(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, `"pkg/specs/pods.go"`)

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V8") {
		t.Fatalf("codes = %v, want V8", findingCodes(findings))
	}
}

// A file carrying HA vocabulary that falls through to the catch-all is the
// central failure this manifest exists to prevent.
func TestValidateReportsAMissingManifestEntry(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V9") {
		t.Fatalf("codes = %v, want V9", findingCodes(findings))
	}
	for _, f := range findings {
		if f.Code == "V9" && f.Severity != boundary.SeverityUndeclared {
			t.Errorf("V9 severity = %v, want undeclared", f.Severity)
		}
	}
}

func TestValidateDowngradesMissingEntriesWhileProvisional(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(true, `  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	for _, f := range findings {
		if f.Code == "V9" && f.Severity != boundary.SeverityWarning {
			t.Fatalf("V9 severity = %v while provisional, want warning", f.Severity)
		}
	}

	strictFindings := vf.validate(t, body, boundary.Options{Strict: true})
	for _, f := range strictFindings {
		if f.Code == "V9" && f.Severity != boundary.SeverityUndeclared {
			t.Fatalf("V9 severity = %v with --strict, want undeclared", f.Severity)
		}
	}
}

func TestValidateAcceptsReviewedAuditFalsePositives(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(false, `  - id: reviewed
    ownership: upstream-untouched
    audit_reviewed: true
    paths: ["pkg/management/postgres/instance.go", "internal/controller/replicas.go"]
  - id: adapt.specs
    ownership: adapted
    paths: ["pkg/specs/pods.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	if hasCode(findings, "V9") {
		t.Fatalf("reviewed false positives must not raise V9: %v", findings)
	}
}

func TestValidateRejectsABaselineThatIsNotAnAncestor(t *testing.T) {
	vf := newValidateFixture(t)
	other := gittest.New(t)
	other.Write("unrelated.go", "package unrelated\n")
	unrelated := other.Commit("unrelated")

	vf.baseline.LastIntegrated.Commit = unrelated

	body := manifestBody(false, validRules, "")
	m, err := boundary.Parse([]byte(body))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	findings, err := boundary.Validate(m, boundary.Options{Repo: vf.repo, Baseline: vf.baseline})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !hasCode(findings, "V11") {
		t.Fatalf("codes = %v, want V11", findingCodes(findings))
	}
}

// Drift is the other half of the CI gate: a file this fork has edited but
// never declared.
func TestValidateReportsUndeclaredForkEdits(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(true, `  - id: adapt.process-primitives
    ownership: adapted
    paths: ["pkg/management/postgres/instance.go"]
  - id: disable.election
    ownership: disabled
    mechanism: unreferenced
    paths: ["internal/controller/replicas.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if !hasCode(findings, "D1") {
		t.Fatalf("codes = %v, want D1 naming pkg/specs/pods.go", findingCodes(findings))
	}
	for _, f := range findings {
		if f.Code == "D1" && f.Path != "pkg/specs/pods.go" {
			t.Errorf("D1 names %q, want pkg/specs/pods.go", f.Path)
		}
	}
}

// A file written by this fork has no upstream source to adapt. Naming it in an
// adapted rule must not hide it from the fork-owned hygiene checks.
func TestValidateReportsNewForkFilesDeclaredAdapted(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Write("internal/cnpatroni/new.go", "package cnpatroni\n")
	vf.git.Commit("add fork-owned code")

	body := manifestBody(true, `  - id: adapt.new
    ownership: adapted
    paths: ["internal/cnpatroni/new.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	for _, f := range findings {
		if f.Code != "D3" {
			continue
		}
		if f.Path != "internal/cnpatroni/new.go" {
			t.Errorf("D3 names %q, want internal/cnpatroni/new.go", f.Path)
		}
		if f.RuleID != "adapt.new" {
			t.Errorf("D3 rule id = %q, want adapt.new", f.RuleID)
		}
		if f.Severity != boundary.SeverityUndeclared {
			t.Errorf("D3 severity = %v, want undeclared", f.Severity)
		}
		if !strings.Contains(f.Message, "did not exist at the fork base") {
			t.Errorf("D3 message %q should explain why the adapted claim is false", f.Message)
		}

		return
	}
	t.Fatalf("findings = %v, want D3 naming internal/cnpatroni/new.go", findings)
}

func TestValidateResolvesAdaptedGlobsToNewForkFiles(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Write("internal/cnpatroni/new.go", "package cnpatroni\n")
	vf.git.Commit("add fork-owned code")

	body := manifestBody(true, `  - id: adapt.new-tree
    ownership: adapted
    paths: ["internal/cnpatroni/**"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	for _, f := range findings {
		if f.Code == "D3" && f.RuleID == "adapt.new-tree" && f.Path == "internal/cnpatroni/new.go" {
			return
		}
	}
	t.Fatalf("findings = %v, want D3 for the concrete path matched by the adapted glob", findings)
}

func TestValidateAcceptsNewForkFilesDeclaredOwned(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Write("internal/cnpatroni/new.go", "package cnpatroni\n")
	vf.git.Commit("add fork-owned code")

	body := manifestBody(true, `  - id: own.new
    ownership: cnpatroni-owned
    paths: ["internal/cnpatroni/**"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if hasCode(findings, "D3") {
		t.Fatalf("a new cnpatroni-owned path must not raise D3: %v", findings)
	}
}

// upstream-untouched is proved, not asserted: the gate accepts a path whose
// bytes were already in the fork base tree, wherever they sat in it. Parking a
// verbatim copy at a new path is the case this fork actually has.
func TestValidateAcceptsVerbatimContentParkedAtANewPath(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Write("parked/doomed.go", "package pkg\n")
	vf.git.Remove("pkg/doomed.go")
	vf.git.Commit("park the inherited file")

	body := manifestBody(true, `  - id: park.doomed
    ownership: upstream-untouched
    paths: ["parked/doomed.go"]
  - id: delete.doomed
    ownership: deleted
    paths: ["pkg/doomed.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if len(findings) != 0 {
		t.Fatalf("a verbatim copy parked at a new path is not drift, got %v", findings)
	}
}

// The laundering case. One byte of difference and the same declaration must
// fail, because the content is no longer upstream's.
func TestValidateReportsEditedContentParkedAtANewPath(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Write("parked/doomed.go", "package pkg\n\n// CloudNativePatroni edit.\n")
	vf.git.Remove("pkg/doomed.go")
	vf.git.Commit("park the inherited file with an edit")

	body := manifestBody(true, `  - id: park.doomed
    ownership: upstream-untouched
    paths: ["parked/doomed.go"]
  - id: delete.doomed
    ownership: deleted
    paths: ["pkg/doomed.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if !hasCode(findings, "D2") {
		t.Fatalf("codes = %v, want D2 naming parked/doomed.go", findingCodes(findings))
	}
	for _, f := range findings {
		if f.Code == "D2" && !strings.Contains(f.Message, "not the bytes") {
			t.Errorf("D2 message %q should say the content is not upstream's", f.Message)
		}
	}
}

// Content that was never in the fork base at all cannot be upstream-untouched,
// however new the path is.
func TestValidateReportsNewContentDeclaredUpstreamUntouched(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Write("parked/invented.go", "package parked\n\n// Written by this fork.\n")
	vf.git.Commit("add a file that never existed upstream")

	body := manifestBody(true, `  - id: park.invented
    ownership: upstream-untouched
    paths: ["parked/invented.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if !hasCode(findings, "D2") {
		t.Fatalf("codes = %v, want D2 naming parked/invented.go", findingCodes(findings))
	}
}

// Removing an inherited file is a change no upstream-untouched rule explains
// either; the honest declaration is deleted.
func TestValidateReportsRemovalDeclaredUpstreamUntouched(t *testing.T) {
	vf := newValidateFixture(t)
	vf.git.Remove("pkg/doomed.go")
	vf.git.Commit("remove an inherited file")

	body := manifestBody(true, `  - id: keep.doomed
    ownership: upstream-untouched
    state: present
    paths: ["pkg/doomed.go"]
`+validRules, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if !hasCode(findings, "D2") {
		t.Fatalf("codes = %v, want D2 naming pkg/doomed.go", findingCodes(findings))
	}
	for _, f := range findings {
		if f.Code == "D2" && !strings.Contains(f.Message, "removed") {
			t.Errorf("D2 message %q should say the path was removed", f.Message)
		}
	}
}

// Declaring a fork-edited path under a specific upstream-untouched rule must
// not launder it past the gate. The rule promises the file is upstream's, so a
// change to it is drift however precisely the rule names it.
func TestValidateReportsForkEditsDeclaredUpstreamUntouched(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(true, `  - id: keep.specs
    ownership: upstream-untouched
    paths: ["pkg/specs/pods.go"]
  - id: adapt.process-primitives
    ownership: adapted
    paths: ["pkg/management/postgres/instance.go"]
  - id: disable.election
    ownership: disabled
    mechanism: unreferenced
    paths: ["internal/controller/replicas.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if !hasCode(findings, "D2") {
		t.Fatalf("codes = %v, want D2 naming pkg/specs/pods.go", findingCodes(findings))
	}
	for _, f := range findings {
		if f.Code != "D2" {
			continue
		}
		if f.Path != "pkg/specs/pods.go" {
			t.Errorf("D2 names %q, want pkg/specs/pods.go", f.Path)
		}
		if f.RuleID != "keep.specs" {
			t.Errorf("D2 rule id = %q, want keep.specs", f.RuleID)
		}
		if f.Severity != boundary.SeverityUndeclared {
			t.Errorf("D2 severity = %v, want undeclared", f.Severity)
		}
	}
}

// The remedy for a misdeclared path is not the remedy for an undeclared one, so
// the printed text has to name both cases.
func TestRemediationNamesMisdeclaredPaths(t *testing.T) {
	remedy := boundary.RemediationFor("boundary.yaml", []boundary.Finding{
		{Code: "D1", Path: "pkg/new.go"},
		{Code: "D2", RuleID: "keep.specs", Path: "pkg/specs/pods.go"},
		{Code: "D3", RuleID: "adapt.new", Path: "internal/cnpatroni/new.go"},
	})
	for _, want := range []string{
		"pkg/new.go",
		"pkg/specs/pods.go",
		"internal/cnpatroni/new.go",
		"reclassify",
		"cnpatroni-owned",
	} {
		if !strings.Contains(remedy, want) {
			t.Errorf("remedy %q does not mention %q", remedy, want)
		}
	}
}

func TestRemediationReclassifiesFalseAdaptations(t *testing.T) {
	remedy := boundary.RemediationFor("boundary.yaml", []boundary.Finding{
		{Code: "D3", RuleID: "adapt.new", Path: "internal/cnpatroni/new.go"},
	})
	for _, want := range []string{"internal/cnpatroni/new.go", "adapt.new", "Reclassify", "cnpatroni-owned"} {
		if !strings.Contains(remedy, want) {
			t.Errorf("remedy %q does not mention %q", remedy, want)
		}
	}
	if strings.Contains(remedy, "Declare each") {
		t.Errorf("remedy %q tells the reader to declare a path that is already declared", remedy)
	}
}

// A deleted path is expected to differ from the fork base, but only while it is
// genuinely gone. One that is still in the worktree is drift as well as V7.
func TestValidateReportsChangedDeletedPathsThatStillExist(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(true, `  - id: delete.specs
    ownership: deleted
    paths: ["pkg/specs/pods.go"]
  - id: adapt.process-primitives
    ownership: adapted
    paths: ["pkg/management/postgres/instance.go"]
  - id: disable.election
    ownership: disabled
    mechanism: unreferenced
    paths: ["internal/controller/replicas.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if !hasCode(findings, "D2") || !hasCode(findings, "V7") {
		t.Fatalf("codes = %v, want both V7 and D2", findingCodes(findings))
	}
}

func TestValidateSkipsDriftWhenNotRequested(t *testing.T) {
	vf := newValidateFixture(t)
	body := manifestBody(true, `  - id: adapt.process-primitives
    ownership: adapted
    paths: ["pkg/management/postgres/instance.go"]
  - id: disable.election
    ownership: disabled
    mechanism: unreferenced
    paths: ["internal/controller/replicas.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")

	findings := vf.validate(t, body, boundary.Options{})
	if hasCode(findings, "D1") {
		t.Fatalf("drift must not run unless asked: %v", findings)
	}
}

// A shallow clone cannot answer the drift question, and must say so instead of
// reporting a clean result.
func TestValidateRefusesToRunDriftOnAShallowClone(t *testing.T) {
	origin := gittest.New(t)
	origin.Write("pkg/specs/pods.go", "package specs\n")
	origin.Commit("one")
	origin.Write("pkg/specs/pods.go", "package specs\n\n// two\n")
	origin.Commit("two")

	shallowRoot := filepath.Join(t.TempDir(), "shallow")
	gittest.RunGit(t, "", "clone", "--depth", "1", "--no-local",
		"file://"+origin.Root, shallowRoot)

	repo, err := gitx.Open(shallowRoot)
	if err != nil {
		t.Fatalf("gitx.Open: %v", err)
	}

	b, err := baseline.Parse([]byte(`
schema: cnpatroni.io/upstream-baseline/v1
upstream:
  url: https://github.com/cloudnative-pg/cloudnative-pg
  track: main
fork_base:
  commit: ` + strings.Repeat("0", 40) + `
  describe: fixture
  date: "2026-01-01"
last_integrated:
  commit: ` + strings.Repeat("0", 40) + `
  describe: fixture
  date: "2026-01-01"
compatibility:
  cloudnative_pg_minor: "1.30"
  cloudnative_pg_reference_tag: v1.30.0
  claimed: fixture
  verified_at: "2026-01-01"
  unadopted_upstream_commits: 0
not_adopted: []
`))
	if err != nil {
		t.Fatalf("baseline.Parse: %v", err)
	}

	m, err := boundary.Parse([]byte(manifestBody(true, `  - id: adapt.specs
    ownership: adapted
    paths: ["pkg/specs/pods.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`, "")))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	_, err = boundary.Validate(m, boundary.Options{Repo: repo, Baseline: b, CheckDrift: true})
	var envErr *gitx.EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("error %v is not an EnvironmentError", err)
	}
	if !strings.Contains(envErr.Remedy, "setup") {
		t.Errorf("remedy %q should point at the setup command", envErr.Remedy)
	}
}

func TestValidateRejectsAnUnknownClassificationState(t *testing.T) {
	vf := newValidateFixture(t)
	body := strings.Replace(manifestBody(false, validRules, ""),
		"provisional: false", "classification_state: someday\nprovisional: false", 1)

	findings := vf.validate(t, body, boundary.Options{})
	if !hasCode(findings, "V13") {
		t.Fatalf("codes = %v, want V13", findingCodes(findings))
	}
}

func TestValidateAcceptsTheTargetClassificationState(t *testing.T) {
	vf := newValidateFixture(t)
	body := strings.Replace(manifestBody(false, `  - id: keep.doomed
    ownership: upstream-untouched
    paths: ["pkg/doomed.go"]
`+validRules, `"pkg/specs/pods.go"`),
		"provisional: false", "classification_state: target\nprovisional: false", 1)

	findings := vf.validate(t, body, boundary.Options{CheckDrift: true})
	if len(findings) != 0 {
		t.Fatalf("expected a clean manifest, got %v", findings)
	}
}
