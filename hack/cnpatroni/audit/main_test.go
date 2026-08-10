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
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const promoteOnlyRules = `schema: cnpatroni-authority-rules/v1
module: example.com/hits
scope:
  roots: [.]
  exclude_generated: true
  include_tests: false
rules:
  - id: proc.promote
    severity: forbidden
    spec: "7.4 bullet 1"
    message: promotion belongs to Patroni
    calls:
      - .../pg.(*Instance).PromoteAndWait
`

const emptyAuthorityBaseline = `schema: cnpatroni-authority-baseline/v2
generated_from: abc123
generated_at: 2026-08-09 23:20:00 UTC
total: 0
allowed_total: 0
totals_by_rule: {}
inputs:
  scope:
    roots: [.]
    exclude_paths: []
    exclude_generated: true
    include_tests: false
  rules:
    - id: proc.promote
      severity: forbidden
      matchers:
        - call:example.com/hits/pg.(*Instance).PromoteAndWait
  packages: 5
  files: 6
buckets: []
`

const recordedPromoteBaseline = `schema: cnpatroni-authority-baseline/v2
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
    exclude_generated: true
    include_tests: false
  rules:
    - id: proc.promote
      severity: forbidden
      matchers:
        - call:example.com/hits/pg.(*Instance).PromoteAndWait
  packages: 5
  files: 6
buckets:
  - rule: proc.promote
    symbol: example.com/hits/ctrl.Promote
    count: 1
`

const stalePromoteBaseline = `schema: cnpatroni-authority-baseline/v2
generated_from: abc123
generated_at: 2026-08-09 23:20:00 UTC
total: 2
allowed_total: 0
totals_by_rule:
  proc.promote: 2
inputs:
  scope:
    roots: [.]
    exclude_paths: []
    exclude_generated: true
    include_tests: false
  rules:
    - id: proc.promote
      severity: forbidden
      matchers:
        - call:example.com/hits/pg.(*Instance).PromoteAndWait
  packages: 5
  files: 6
buckets:
  - rule: proc.promote
    symbol: example.com/hits/ctrl.Promote
    count: 1
  - rule: proc.promote
    symbol: pkg.Vanished
    count: 1
`

const grownPromoteBaseline = `schema: cnpatroni-authority-baseline/v2
generated_from: abc123
generated_at: 2026-08-09 23:20:00 UTC
total: 2
allowed_total: 0
totals_by_rule:
  proc.promote: 2
inputs:
  scope:
    roots: [.]
    exclude_paths: []
    exclude_generated: true
    include_tests: false
  rules:
    - id: proc.promote
      severity: forbidden
      matchers:
        - call:example.com/hits/pg.(*Instance).PromoteAndWait
  packages: 5
  files: 6
buckets:
  - rule: proc.promote
    symbol: example.com/hits/ctrl.Promote
    count: 2
`

const renamedOldBaseline = `schema: cnpatroni-authority-baseline/v2
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
    exclude_generated: true
    include_tests: false
  rules:
    - id: proc.promote
      severity: forbidden
      matchers:
        - call:example.com/hits/pg.(*Instance).PromoteAndWait
  packages: 5
  files: 6
buckets:
  - rule: proc.promote
    symbol: pkg.OldName
    count: 1
`

const renamedNewBaseline = `schema: cnpatroni-authority-baseline/v2
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
    exclude_generated: true
    include_tests: false
  rules:
    - id: proc.promote
      severity: forbidden
      matchers:
        - call:example.com/hits/pg.(*Instance).PromoteAndWait
  packages: 5
  files: 6
buckets:
  - rule: proc.promote
    symbol: pkg.NewName
    count: 1
`

func TestCheckCommandTracksForbiddenCallState(t *testing.T) {
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.yaml")
	classification := filepath.Join(dir, "classification.yaml")
	baseline := filepath.Join(dir, "baseline.yaml")
	writeFile(t, rules, promoteOnlyRules)
	writeFile(t, classification, minimalEntry)
	writeFile(t, baseline, emptyAuthorityBaseline)

	args := []string{
		"check",
		"--root", filepath.Join("testdata", "mod-hits"),
		"--rules", rules,
		"--classification", classification,
		"--baseline", baseline,
	}
	code, stdout, stderr := captureRun(t, args)
	if code != exitViolation {
		t.Fatalf("new forbidden call exit code = %d, want %d", code, exitViolation)
	}
	if !strings.Contains(stderr, "new forbidden call: proc.promote in example.com/hits/ctrl.Promote") {
		t.Errorf("violation output does not name the new call: %q", stderr)
	}
	if !strings.Contains(stdout, "1 findings, 1 classified symbols, 1 violations, 0 hygiene notes") {
		t.Errorf("violation summary = %q", stdout)
	}

	writeFile(t, baseline, recordedPromoteBaseline)
	code, stdout, stderr = captureRun(t, args)
	if code != exitClean {
		t.Fatalf("recorded forbidden call exit code = %d, want %d", code, exitClean)
	}
	if stderr != "" {
		t.Errorf("recorded debt wrote stderr: %q", stderr)
	}
	if !strings.Contains(stdout, "1 findings, 1 classified symbols, 0 violations, 0 hygiene notes") {
		t.Errorf("clean summary = %q", stdout)
	}

	writeFile(t, baseline, stalePromoteBaseline)
	code, stdout, stderr = captureRun(t, args)
	if code != exitHygiene {
		t.Fatalf("stale baseline exit code = %d, want %d", code, exitHygiene)
	}
	wantHygiene := "stale baseline entry: proc.promote in pkg.Vanished; " +
		"run `go run . baseline --write`\n"
	if stderr != wantHygiene {
		t.Errorf("stale baseline stderr = %q, want %q", stderr, wantHygiene)
	}
	if stdout != "authority audit: 1 findings, 1 classified symbols, 0 violations, 1 hygiene notes\n" {
		t.Errorf("stale baseline stdout = %q", stdout)
	}

	warnArgs := append(append([]string{}, args...), "--warn-only-hygiene")
	code, stdout, stderr = captureRun(t, warnArgs)
	if code != exitClean {
		t.Fatalf("warn-only stale baseline exit code = %d, want %d", code, exitClean)
	}
	if stderr != wantHygiene {
		t.Errorf("warn-only stale baseline stderr = %q, want %q", stderr, wantHygiene)
	}
	if stdout != "authority audit: 1 findings, 1 classified symbols, 0 violations, 1 hygiene notes\n" {
		t.Errorf("warn-only stale baseline stdout = %q", stdout)
	}

	writeFile(t, baseline, "schema: [\n")
	code, _, stderr = captureRun(t, args)
	if code != exitToolError {
		t.Fatalf("malformed baseline exit code = %d, want %d", code, exitToolError)
	}
	if !strings.Contains(stderr, "parsing baseline yaml") ||
		!strings.Contains(stderr, "not a policy failure") {
		t.Errorf("malformed baseline stderr = %q", stderr)
	}
}

func TestBaselineCompareCommandMapsGrowthOntoExitCodes(t *testing.T) {
	refusal := "the authority baseline grew; a change that adds forbidden calls needs an explicit" +
		" human decision, not a regenerated baseline. Input weakening has no in-band approval;" +
		" narrow the audit only by changing the audit tool itself, and review the rules diff\n"
	cases := []struct {
		name           string
		base           string
		head           string
		missingCompare bool
		wantCode       int
		wantStdout     string
		wantStderr     string
	}{
		{
			name:       "unchanged",
			base:       recordedPromoteBaseline,
			head:       recordedPromoteBaseline,
			wantCode:   exitClean,
			wantStdout: "authority baseline: 1 -> 1 (+0)\n",
		},
		{
			name:     "total fell",
			base:     stalePromoteBaseline,
			head:     recordedPromoteBaseline,
			wantCode: exitClean,
			wantStdout: "authority baseline: 2 -> 1 (-1)\n" +
				fmt.Sprintf("  %-28s %d -> %d (%+d)\n", "proc.promote", 2, 1, -1),
		},
		{
			name:     "total grew",
			base:     recordedPromoteBaseline,
			head:     grownPromoteBaseline,
			wantCode: exitViolation,
			wantStdout: "authority baseline: 1 -> 2 (+1)\n" +
				fmt.Sprintf("  %-28s %d -> %d (%+d)\n", "proc.promote", 1, 2, 1) +
				"  new or enlarged bucket: proc.promote in example.com/hits/ctrl.Promote 1 -> 2\n",
			wantStderr: refusal,
		},
		{
			name:     "bucket renamed at a constant total",
			base:     renamedOldBaseline,
			head:     renamedNewBaseline,
			wantCode: exitViolation,
			wantStdout: "authority baseline: 1 -> 1 (+0)\n" +
				"  new or enlarged bucket: proc.promote in pkg.NewName 0 -> 1\n",
			wantStderr: refusal,
		},
		{
			name:           "compare file missing",
			head:           recordedPromoteBaseline,
			missingCompare: true,
			wantCode:       exitToolError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			basePath := filepath.Join(dir, "base.yaml")
			headPath := filepath.Join(dir, "head.yaml")
			summaryPath := filepath.Join(dir, "summary.md")
			if !tc.missingCompare {
				writeFile(t, basePath, tc.base)
			}
			writeFile(t, headPath, tc.head)
			writeFile(t, summaryPath, "")
			t.Setenv("GITHUB_STEP_SUMMARY", summaryPath)

			wantStderr := tc.wantStderr
			if tc.missingCompare {
				_, readErr := os.ReadFile(basePath)
				wantStderr = fmt.Sprintf("audit: reading baseline: %v\n", readErr) +
					"this is a tool or configuration error, not a policy failure\n"
			}
			code, stdout, stderr := captureRun(t, []string{
				"baseline", "--baseline", headPath, "--compare", basePath,
			})
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}
			if stdout != tc.wantStdout {
				t.Errorf("stdout = %q, want %q", stdout, tc.wantStdout)
			}
			if stderr != wantStderr {
				t.Errorf("stderr = %q, want %q", stderr, wantStderr)
			}

			wantSummary := ""
			if tc.wantStdout != "" {
				wantSummary = strings.SplitN(tc.wantStdout, "\n", 2)[0] + "\n"
			}
			summary, err := os.ReadFile(summaryPath)
			if err != nil {
				t.Fatalf("read step summary: %v", err)
			}
			if string(summary) != wantSummary {
				t.Errorf("step summary = %q, want %q", summary, wantSummary)
			}
		})
	}
}

func TestHeadCommitReportsUnknownWhenGitIsAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if got := headCommit(t.TempDir()); got != "unknown" {
		t.Errorf("headCommit without git = %q, want unknown", got)
	}
}

func TestScanCommandStreamsOutputLargerThanThePipeBuffer(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/big\n\ngo 1.26.5\n")
	writeFile(t, filepath.Join(dir, "gen.go"),
		"package big\n\nvar signals = []string{\n"+
			strings.Repeat("\t\"standby.signal\",\n", 2000)+"}\n")
	rules := filepath.Join(dir, "rules.yaml")
	writeFile(t, rules, `schema: cnpatroni-authority-rules/v1
module: example.com/big
scope:
  roots: [.]
  exclude_generated: true
  include_tests: false
rules:
  - id: recovery.standby-signal
    severity: forbidden
    spec: "7.4 bullet 3"
    message: writing standby.signal belongs to Patroni
    literals:
      - standby.signal
`)

	code, stdout, stderr := captureRun(t, []string{
		"scan", "--root", dir, "--rules", rules, "--format", "text",
	})
	if code != exitClean {
		t.Errorf("exit code = %d, want %d", code, exitClean)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	// This exceeds twice the 64 KiB maximum pipe buffer on macOS and Linux.
	if len(stdout) <= 131072 {
		t.Errorf("stdout length = %d, want more than 131072", len(stdout))
	}
	const trailer = "\n2000 findings in 1 packages, 1 files\n"
	if !strings.HasSuffix(stdout, trailer) {
		t.Errorf("stdout does not end with %q", trailer)
	}
	matchingLines := 0
	for _, line := range strings.Split(stdout, "\n") {
		if strings.Contains(line, "[recovery.standby-signal]") {
			matchingLines++
		}
	}
	if matchingLines != 2000 {
		t.Errorf("matching output lines = %d, want 2000", matchingLines)
	}
}

func captureRun(t *testing.T, args []string) (code int, stdout, stderr string) {
	t.Helper()

	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	var stdoutBody, stderrBody bytes.Buffer
	var stdoutReadErr, stderrReadErr error
	var readers sync.WaitGroup
	readers.Add(2)
	go func() {
		defer readers.Done()
		_, stdoutReadErr = io.Copy(&stdoutBody, stdoutReader)
	}()
	go func() {
		defer readers.Done()
		_, stderrReadErr = io.Copy(&stderrBody, stderrReader)
	}()

	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	code = run(args)
	os.Stdout, os.Stderr = oldStdout, oldStderr
	stdoutCloseErr := stdoutWriter.Close()
	stderrCloseErr := stderrWriter.Close()
	readers.Wait()
	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}
	if stdoutCloseErr != nil {
		t.Fatalf("close stdout writer: %v", stdoutCloseErr)
	}
	if stderrCloseErr != nil {
		t.Fatalf("close stderr writer: %v", stderrCloseErr)
	}
	if stdoutReadErr != nil {
		t.Fatalf("read stdout: %v", stdoutReadErr)
	}
	if stderrReadErr != nil {
		t.Fatalf("read stderr: %v", stderrReadErr)
	}

	return code, stdoutBody.String(), stderrBody.String()
}
