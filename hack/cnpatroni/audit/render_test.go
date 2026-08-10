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

func TestBuildDocDataReportsExactAuthorityMetrics(t *testing.T) {
	rules := &RuleSet{Rules: []Rule{
		{ID: "z.observe", Severity: SeverityObserve, Spec: "8.12"},
		{ID: "a.forbidden", Severity: SeverityForbidden, Spec: "7.4"},
	}}
	res := &ScanResult{
		Packages: 3,
		Files:    4,
		Findings: []Finding{
			{Rule: "a.forbidden", Severity: SeverityForbidden, Symbol: "module/pkg/a.A", Path: "pkg/a/a.go", Package: "pkg/a"},
			{Rule: "a.forbidden", Severity: SeverityForbidden, Symbol: "module/pkg/a.B", Path: "pkg/a/a.go", Package: "pkg/a"},
			{Rule: "a.forbidden", Severity: SeverityForbidden, Symbol: "module/pkg/b.C", Path: "pkg/b/b.go", Package: "pkg/b"},
			{Rule: "z.observe", Severity: SeverityObserve, Symbol: "module/pkg/c.D", Path: "pkg/c/c.go", Package: "pkg/c"},
		},
	}
	cls := &Classification{
		Entries: []Entry{
			{Path: "pkg/a/a.go", Symbol: "module/pkg/a.A", Class: "move", Dest: "patroni-container", Rationale: "Move role authority into Patroni."},
			{Path: "pkg/a/a.go", Symbol: "module/pkg/a.B", Class: "disable", Dest: "disabled", Rationale: "Disable the competing authority path."},
			{Path: "pkg/b/b.go", Symbol: "module/pkg/b.C", Class: "keep", Dest: "operator", Rationale: "Keep observation in the operator."},
		},
		Responsibilities: []Responsibility{{Responsibility: "Run Postgres", Dest: "patroni-container"}},
	}
	baseline := &Baseline{Total: 3}

	data := buildDocData(rules, res, cls, baseline, "policy/rules.yaml")

	if data.Findings != 4 || data.Packages != 3 || data.Files != 4 {
		t.Errorf("scan totals = %d findings, %d packages, %d files", data.Findings, data.Packages, data.Files)
	}
	if data.ForbiddenHits != 3 || data.ForbiddenSymbols != 3 || data.ForbiddenFiles != 2 ||
		data.ForbiddenPackages != 2 {
		t.Errorf("forbidden metrics = hits %d, symbols %d, files %d, packages %d",
			data.ForbiddenHits, data.ForbiddenSymbols, data.ForbiddenFiles, data.ForbiddenPackages)
	}
	if data.HighestRulePkg != "pkg/a" || data.HighestRulePkgPct != 66 || data.TopThreePkgPct != 100 {
		t.Errorf("concentration = %q %d%%, top three %d%%",
			data.HighestRulePkg, data.HighestRulePkgPct, data.TopThreePkgPct)
	}
	if len(data.ByRule) != 2 || data.ByRule[0].ID != "a.forbidden" || data.ByRule[0].Hits != 3 ||
		data.ByRule[0].Symbols != 3 || data.ByRule[0].Files != 2 || data.ByRule[1].ID != "z.observe" ||
		data.ByRule[1].Hits != 1 {
		t.Errorf("rule rows = %+v, want exact sorted counts", data.ByRule)
	}
	if data.RelocatedEntries != 1 || data.RelocatedFiles != 1 || data.DisabledEntries != 1 ||
		data.UntouchedEntries != 1 || data.AdaptedEntries != 0 || data.ClassifiedFiles != 2 ||
		data.ClassifiedPackages != 2 {
		t.Errorf("classification metrics = %+v", data)
	}
	if len(data.ByPackage) != 2 || data.ByPackage[0].Package != "pkg/a" ||
		len(data.ByPackage[0].Entries) != 2 || data.ByPackage[1].Package != "pkg/b" {
		t.Errorf("package sections = %+v", data.ByPackage)
	}
}

func TestConcentrationCoversZeroOneAndManyPackages(t *testing.T) {
	if top, pct, three := concentration(&ScanResult{}); top != "" || pct != 0 || three != 0 {
		t.Errorf("empty concentration = %q %d %d, want empty and zero", top, pct, three)
	}

	one := &ScanResult{Findings: []Finding{{Severity: SeverityForbidden, Package: "pkg/only"}}}
	if top, pct, three := concentration(one); top != "pkg/only" || pct != 100 || three != 100 {
		t.Errorf("one-package concentration = %q %d %d, want pkg/only 100 100", top, pct, three)
	}

	many := &ScanResult{Findings: []Finding{
		{Severity: SeverityForbidden, Package: "pkg/a"},
		{Severity: SeverityForbidden, Package: "pkg/a"},
		{Severity: SeverityForbidden, Package: "pkg/a"},
		{Severity: SeverityForbidden, Package: "pkg/b"},
		{Severity: SeverityForbidden, Package: "pkg/b"},
		{Severity: SeverityForbidden, Package: "pkg/c"},
		{Severity: SeverityForbidden, Package: "pkg/d"},
		{Severity: SeverityObserve, Package: "pkg/ignored"},
	}}
	if top, pct, three := concentration(many); top != "pkg/a" || pct != 42 || three != 85 {
		t.Errorf("many-package concentration = %q %d %d, want pkg/a 42 85", top, pct, three)
	}
}

func TestDocumentRenderingAndWriteErrors(t *testing.T) {
	data := &docData{
		Rules: "policy/rules.yaml",
		Baseline: &Baseline{
			GeneratedFrom: "head-123",
			GeneratedAt:   "2026-08-10 12:00:00 UTC",
		},
		ResponsibilityRows: []Responsibility{{
			Responsibility: "Run Postgres", Dest: "patroni-container",
			Rationale: "Patroni supervises the server process.",
		}},
	}
	dir := t.TempDir()
	narrative := filepath.Join(dir, "narrative.md")
	writeFile(t, narrative, "The scanner narrative is rendered here.\n")

	audit, err := renderAudit(narrative, data)
	if err != nil {
		t.Fatalf("renderAudit: %v", err)
	}
	if !strings.Contains(audit, "The scanner narrative is rendered here.") ||
		!strings.Contains(audit, "policy/rules.yaml") {
		t.Errorf("audit output is missing its narrative or rules path:\n%s", audit)
	}
	wantAbsent := "Taken from commit: fork base not recorded. " +
		"Last regenerated from `head-123` at 2026-08-10 12:00:00 UTC."
	if !strings.Contains(audit, wantAbsent) {
		t.Errorf("audit output does not distinguish an absent fork base:\n%s", audit)
	}

	data.Baseline.ForkBase = "fork-base-123"
	auditWithForkBase, err := renderAudit(narrative, data)
	if err != nil {
		t.Fatalf("renderAudit with fork base: %v", err)
	}
	wantPresent := "Taken from commit `fork-base-123`. " +
		"Last regenerated from `head-123` at 2026-08-10 12:00:00 UTC."
	if !strings.Contains(auditWithForkBase, wantPresent) {
		t.Errorf("audit output does not label both commits:\n%s", auditWithForkBase)
	}
	responsibilities, err := renderResponsibilityMap(data)
	if err != nil {
		t.Fatalf("renderResponsibilityMap: %v", err)
	}
	if !strings.Contains(responsibilities, "Run Postgres") ||
		!strings.Contains(responsibilities, "Patroni container") {
		t.Errorf("responsibility map is missing exact responsibility data:\n%s", responsibilities)
	}

	target := filepath.Join(dir, "nested", "audit.md")
	if err := writeDoc(target, audit); err != nil {
		t.Fatalf("writeDoc: %v", err)
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(written) != audit {
		t.Error("writeDoc changed the rendered document")
	}

	if _, err := renderAudit(filepath.Join(dir, "missing.md"), data); err == nil ||
		!strings.Contains(err.Error(), "reading narrative") {
		t.Errorf("missing narrative error = %v", err)
	}
	badNarrative := filepath.Join(dir, "bad.md")
	writeFile(t, badNarrative, "{{")
	if _, err := renderAudit(badNarrative, data); err == nil ||
		!strings.Contains(err.Error(), "parsing the narrative fragment") {
		t.Errorf("malformed narrative error = %v", err)
	}
	if err := writeDoc(dir, "cannot replace a directory"); err == nil ||
		!strings.Contains(err.Error(), "writing") {
		t.Errorf("directory target error = %v", err)
	}
}
