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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixtureRules(t *testing.T) *RuleSet {
	t.Helper()

	rules, err := LoadRules(filepath.Join("testdata", "rules-fixture.yaml"))
	if err != nil {
		t.Fatalf("LoadRules: %v", err)
	}
	return rules
}

func scanResultFixture(t *testing.T, module string, rules *RuleSet) *ScanResult {
	t.Helper()

	res, err := Scan(filepath.Join("testdata", module), rules)
	if err != nil {
		t.Fatalf("Scan(%s): %v", module, err)
	}
	return res
}

func scanFixture(t *testing.T, module string, rules *RuleSet) []Finding {
	t.Helper()

	return scanResultFixture(t, module, rules).Findings
}

func findingKeys(findings []Finding) []string {
	keys := make([]string, 0, len(findings))
	for _, f := range findings {
		keys = append(keys, fmt.Sprintf("%s %s %s:%d", f.Rule, f.Symbol, f.Path, f.Line))
	}
	return keys
}

func TestScanMatchesExactlyTheExpectedHits(t *testing.T) {
	findings := scanFixture(t, "mod-hits", loadFixtureRules(t))

	// Ordered by path, line, column and rule, which is what makes two runs on the
	// same tree byte-identical. Every fixture file carries the repository's
	// licence header, so the interesting lines start in the thirties.
	want := []string{
		"recovery.standby-signal example.com/hits/ctrl.init ctrl/ctrl.go:32",
		"proc.promote example.com/hits/ctrl.Promote ctrl/ctrl.go:35",
		"proc.startstop example.com/hits/ctrl.StopBoth ctrl/ctrl.go:40",
		"role.target-primary example.com/hits/ctrl.SetPrimary ctrl/ctrl.go:46",
		"role.current-primary-read example.com/hits/ctrl.ReadPrimary ctrl/ctrl.go:51",
		"proc.exec-pgctl example.com/hits/ctrl.ExecPgCtl ctrl/exec.go:30",
		"proc.exec-pgctl example.com/hits/ctrl.ExecPgCtlContext ctrl/exec.go:33",
		"guard.call example.com/hits/ctrl.GuardedOK ctrl/guarded.go:25",
		"guard.call example.com/hits/ctrl.GuardedWrongLiteral ctrl/guarded.go:28",
	}

	got := findingKeys(findings)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("scan findings mismatch\n got:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestScanFiltersCallsByConstantArguments(t *testing.T) {
	findings := scanFixture(t, "mod-hits", loadFixtureRules(t))
	matched := map[string]bool{}
	for _, finding := range findings {
		if finding.Rule == "proc.exec-pgctl" {
			matched[finding.Symbol] = true
		}
	}

	want := map[string]bool{
		"example.com/hits/ctrl.ExecPgCtl":        true,
		"example.com/hits/ctrl.ExecPgCtlContext": true,
	}
	if len(matched) != len(want) {
		t.Fatalf("pg_ctl call symbols = %v, want exactly %v", matched, want)
	}
	for symbol := range want {
		if !matched[symbol] {
			t.Errorf("constant pg_ctl call %s was not reported", symbol)
		}
	}
	for _, symbol := range []string{
		"example.com/hits/ctrl.ExecDynamic",
		"example.com/hits/ctrl.ExecUnrelated",
	} {
		if matched[symbol] {
			t.Errorf("non-matching call %s was reported", symbol)
		}
	}
}

func TestScanIsTypeAwareAcrossIdenticalMethodNames(t *testing.T) {
	findings := scanFixture(t, "mod-hits", loadFixtureRules(t))

	shutdowns := 0
	for _, f := range findings {
		if f.Rule == "proc.startstop" {
			shutdowns++
		}
	}
	// ctrl.StopBoth calls Shutdown on the guarded type and on *http.Server.
	if shutdowns != 1 {
		t.Errorf("proc.startstop matched %d times, want 1 (http.Server.Shutdown must not match)", shutdowns)
	}
}

func TestScanIgnoresComments(t *testing.T) {
	findings := scanFixture(t, "mod-hits", loadFixtureRules(t))

	for _, f := range findings {
		if strings.HasSuffix(f.Symbol, ".Commented") {
			t.Errorf("a comment produced a finding: %+v", f)
		}
	}
}

func TestScanHonoursTestAndGeneratedExclusions(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RuleSet)
		symbol  string
		present bool
	}{
		{
			name:    "test files excluded by default",
			mutate:  func(*RuleSet) {},
			symbol:  "example.com/hits/ctrl.TestPromoteFromATest",
			present: false,
		},
		{
			name:    "test files included when configured",
			mutate:  func(rs *RuleSet) { rs.Scope.IncludeTests = true },
			symbol:  "example.com/hits/ctrl.TestPromoteFromATest",
			present: true,
		},
		{
			// The fixture carries the licence header above its generated marker,
			// exactly as this repository's own generated files do.
			name:    "generated files excluded by default, marker below the licence header",
			mutate:  func(*RuleSet) {},
			symbol:  "example.com/hits/gen.Generated",
			present: false,
		},
		{
			name:    "generated files included when configured",
			mutate:  func(rs *RuleSet) { rs.Scope.ExcludeGenerated = false },
			symbol:  "example.com/hits/gen.Generated",
			present: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := loadFixtureRules(t)
			tc.mutate(rules)

			found := false
			for _, f := range scanFixture(t, "mod-hits", rules) {
				if f.Symbol == tc.symbol {
					found = true
				}
			}
			if found != tc.present {
				t.Errorf("symbol %s present=%v, want %v", tc.symbol, found, tc.present)
			}
		})
	}
}

func TestScanExcludesPathsByGlob(t *testing.T) {
	rules := loadFixtureRules(t)
	rules.Scope.ExcludePaths = append(rules.Scope.ExcludePaths, "ctrl/**")

	for _, f := range scanFixture(t, "mod-hits", rules) {
		if strings.HasPrefix(f.Path, "ctrl/") {
			t.Errorf("excluded path produced a finding: %s", f.Path)
		}
	}
}

func TestScanOfACleanModuleFindsNothing(t *testing.T) {
	if findings := scanFixture(t, "mod-clean", loadFixtureRules(t)); len(findings) != 0 {
		t.Errorf("clean module produced %d findings: %v", len(findings), findingKeys(findings))
	}
}

func TestScanRepoMergesRootsAndPrefixesPaths(t *testing.T) {
	rules := loadFixtureRules(t)
	rules.Scope.Roots = []string{"mod-hits", "mod-clean"}

	res, err := ScanRepo("testdata", rules)
	if err != nil {
		t.Fatalf("ScanRepo: %v", err)
	}

	want := []string{
		"recovery.standby-signal example.com/hits/ctrl.init mod-hits/ctrl/ctrl.go:32",
		"proc.promote example.com/hits/ctrl.Promote mod-hits/ctrl/ctrl.go:35",
		"proc.startstop example.com/hits/ctrl.StopBoth mod-hits/ctrl/ctrl.go:40",
		"role.target-primary example.com/hits/ctrl.SetPrimary mod-hits/ctrl/ctrl.go:46",
		"role.current-primary-read example.com/hits/ctrl.ReadPrimary mod-hits/ctrl/ctrl.go:51",
		"proc.exec-pgctl example.com/hits/ctrl.ExecPgCtl mod-hits/ctrl/exec.go:30",
		"proc.exec-pgctl example.com/hits/ctrl.ExecPgCtlContext mod-hits/ctrl/exec.go:33",
		"guard.call example.com/hits/ctrl.GuardedOK mod-hits/ctrl/guarded.go:25",
		"guard.call example.com/hits/ctrl.GuardedWrongLiteral mod-hits/ctrl/guarded.go:28",
	}
	if got := findingKeys(res.Findings); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("merged findings mismatch\n got:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if res.Packages != 6 {
		t.Errorf("packages = %d, want 6 across both roots", res.Packages)
	}
	if res.Files != 7 {
		t.Errorf("files = %d, want 7 across both roots after exclusions", res.Files)
	}
	if got := res.GuardOps["example.com/hits/ctrl.GuardedOK"]; len(got) != 1 ||
		got[0] != "example.com/hits/ctrl.GuardedOK" {
		t.Errorf("guard ops = %v, want the exact enclosing symbol", got)
	}
}

func TestScanRepoReportsAMissingRootAsAToolError(t *testing.T) {
	rules := loadFixtureRules(t)
	rules.Scope.Roots = []string{"does-not-exist"}

	_, err := ScanRepo("testdata", rules)
	if err == nil {
		t.Fatal("ScanRepo accepted a missing scan root")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error %q does not name the missing root", err)
	}
}

func TestScanReportsUncompilableCodeAsAToolError(t *testing.T) {
	moduleDir := t.TempDir()
	for _, fixture := range []struct {
		stored       string
		materialised string
	}{
		{stored: "go.mod", materialised: "go.mod"},
		{stored: filepath.Join("broken", "broken.go.txt"), materialised: filepath.Join("broken", "broken.go")},
	} {
		content, err := os.ReadFile(filepath.Join("testdata", "mod-broken", fixture.stored))
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture.stored, err)
		}
		target := filepath.Join(moduleDir, fixture.materialised)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("create fixture directory for %s: %v", fixture.materialised, err)
		}
		if err := os.WriteFile(target, content, 0o600); err != nil {
			t.Fatalf("materialise fixture %s: %v", fixture.materialised, err)
		}
	}

	_, err := Scan(moduleDir, loadFixtureRules(t))
	if err == nil {
		t.Fatal("Scan of a module that does not compile returned no error")
	}
	if !strings.Contains(err.Error(), "missing condition in if statement") {
		t.Errorf("error does not contain the expected parser diagnostic: %v", err)
	}
}

func TestScanIsDeterministic(t *testing.T) {
	rules := loadFixtureRules(t)

	first, err := json.Marshal(scanFixture(t, "mod-hits", rules))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	second, err := json.Marshal(scanFixture(t, "mod-hits", rules))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("two scans of the same tree produced different output")
	}
}

func TestFindingTextFormatIsTheGNUErrorFormat(t *testing.T) {
	findings := scanFixture(t, "mod-hits", loadFixtureRules(t))
	if len(findings) == 0 {
		t.Fatal("no findings to format")
	}

	line := findings[0].Text()
	if !strings.HasPrefix(line, findings[0].Path+":") {
		t.Errorf("text format does not start with path: %q", line)
	}
	if !strings.Contains(line, "["+findings[0].Rule+"]") {
		t.Errorf("text format does not name the rule: %q", line)
	}
}

func TestFindingGitHubFormatCarriesFileAndLine(t *testing.T) {
	findings := scanFixture(t, "mod-hits", loadFixtureRules(t))
	if len(findings) == 0 {
		t.Fatal("no findings to format")
	}

	line := findings[0].GitHub()
	for _, want := range []string{"::error file=", ",line=", ",col=", "title="} {
		if !strings.Contains(line, want) {
			t.Errorf("GitHub format %q does not contain %q", line, want)
		}
	}
}

func TestLoadRulesRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "unknown schema",
			yaml:    "schema: cnpatroni-authority-rules/v99\nrules: []\n",
			wantErr: "schema",
		},
		{
			name:    "unknown severity",
			yaml:    "schema: cnpatroni-authority-rules/v1\nrules:\n  - id: r\n    severity: maybe\n",
			wantErr: "severity",
		},
		{
			name:    "duplicate rule id",
			yaml:    "schema: cnpatroni-authority-rules/v1\nrules:\n  - id: r\n    severity: forbidden\n    literals: [a]\n  - id: r\n    severity: forbidden\n    literals: [b]\n",
			wantErr: "duplicate",
		},
		{
			name:    "rule with no matcher",
			yaml:    "schema: cnpatroni-authority-rules/v1\nrules:\n  - id: r\n    severity: forbidden\n",
			wantErr: "no matcher",
		},
		{
			name:    "malformed yaml",
			yaml:    "schema: [\n",
			wantErr: "yaml",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules.yaml")
			writeFile(t, path, tc.yaml)

			_, err := LoadRules(path)
			if err == nil {
				t.Fatal("LoadRules accepted invalid configuration")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}
