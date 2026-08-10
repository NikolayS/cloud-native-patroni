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
	"io"
	"os"
	"path/filepath"
	"strings"
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

const emptyAuthorityBaseline = `schema: cnpatroni-authority-baseline/v1
generated_from: abc123
generated_at: 2026-08-09 23:20:00 UTC
total: 0
totals_by_rule: {}
buckets: []
`

const recordedPromoteBaseline = `schema: cnpatroni-authority-baseline/v1
generated_from: abc123
generated_at: 2026-08-09 23:20:00 UTC
total: 1
totals_by_rule:
  proc.promote: 1
buckets:
  - rule: proc.promote
    symbol: example.com/hits/ctrl.Promote
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

func TestHeadCommitReportsUnknownWhenGitIsAbsent(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if got := headCommit(t.TempDir()); got != "unknown" {
		t.Errorf("headCommit without git = %q, want unknown", got)
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

	oldStdout, oldStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	code = run(args)
	os.Stdout, os.Stderr = oldStdout, oldStderr
	if err := stdoutWriter.Close(); err != nil {
		t.Fatalf("close stdout writer: %v", err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatalf("close stderr writer: %v", err)
	}

	stdoutBody, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderrBody, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatalf("close stdout reader: %v", err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatalf("close stderr reader: %v", err)
	}

	return code, string(stdoutBody), string(stderrBody)
}
