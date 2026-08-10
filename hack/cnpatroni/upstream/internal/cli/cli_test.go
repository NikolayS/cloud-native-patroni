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

package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/postgres-ai/cnpatroni-upstream/internal/cli"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gittest"
)

const cliManifest = `schema: cnpatroni.io/boundary/v1
generated_from: fixture
provisional: false
default_ownership: upstream-untouched
audit_scan:
  include: ["**/*.go"]
  exclude: ["**/*_test.go"]
audit_terms: ["TargetPrimary"]
generated_artifacts: []
required_paths: []
rules:
  - id: disable.election
    ownership: disabled
    mechanism: unreferenced
    gate: ["authority-audit"]
    paths: ["internal/controller/replicas.go"]
  - id: owned.tooling
    ownership: cnpatroni-owned
    paths: ["hack/cnpatroni/upstream/**"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`

type env struct {
	git  *gittest.Fixture
	root string
}

// newEnv builds a fork whose HEAD carries a manifest, a baseline and one
// upstream branch to compare against.
func newEnv(t *testing.T, manifestBody string) *env {
	t.Helper()

	f := gittest.New(t)
	f.Write("internal/controller/replicas.go", "package controller\n\n// TargetPrimary\n")
	f.Write("docs/src/faq.md", "# Frequently asked questions\n")
	f.Write("hack/cnpatroni/upstream/boundary.yaml", manifestBody)
	base := f.Commit("fork base")
	f.Branch("upstream")

	f.Write("hack/cnpatroni/upstream/upstream-baseline.yaml", `schema: cnpatroni.io/upstream-baseline/v1
upstream:
  url: https://github.com/cloudnative-pg/cloudnative-pg
  track: main
fork_base:
  commit: `+base+`
  describe: fixture
  date: "2026-01-01"
last_integrated:
  commit: `+base+`
  describe: fixture
  date: "2026-01-01"
compatibility:
  cloudnative_pg_minor: "1.30"
  cloudnative_pg_reference_tag: v1.30.0
  claimed: fixture
  verified_at: "2026-01-01"
  unadopted_upstream_commits: 0
not_adopted: []
`)
	f.Commit("record the baseline")

	return &env{git: f, root: f.Root}
}

func (e *env) run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	full := append([]string{"--repo", e.root}, args...)
	code := cli.Run(full, &stdout, &stderr)

	return code, stdout.String(), stderr.String()
}

func TestCLIRejectsAnUnknownCommand(t *testing.T) {
	e := newEnv(t, cliManifest)

	code, _, stderr := e.run("frobnicate")
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d", code, cli.ExitUsage)
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Errorf("stderr should name the unknown command: %q", stderr)
	}
}

func TestCLIPrintsUsageWithNoArguments(t *testing.T) {
	e := newEnv(t, cliManifest)

	code, _, stderr := e.run()
	if code != cli.ExitUsage {
		t.Fatalf("exit = %d, want %d", code, cli.ExitUsage)
	}
	for _, command := range []string{"setup", "validate", "report"} {
		if !strings.Contains(stderr, command) {
			t.Errorf("usage should list %q: %q", command, stderr)
		}
	}
}

func TestCLIValidateAcceptsACleanManifest(t *testing.T) {
	e := newEnv(t, cliManifest)

	code, stdout, stderr := e.run("validate", "--drift")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestCLIValidateReportsUndeclaredForkEdits(t *testing.T) {
	e := newEnv(t, cliManifest)
	e.git.Write("docs/src/faq.md", "# Frequently asked questions\n\nFork edit.\n")
	e.git.Commit("edit the documentation")

	code, stdout, _ := e.run("validate", "--drift")
	if code != cli.ExitUndeclared {
		t.Fatalf("exit = %d, want %d\n%s", code, cli.ExitUndeclared, stdout)
	}
	if !strings.Contains(stdout, "docs/src/faq.md") {
		t.Errorf("stdout should name the undeclared path:\n%s", stdout)
	}
	if !strings.Contains(stdout, "boundary.yaml") {
		t.Errorf("stdout should print the remedy:\n%s", stdout)
	}
}

func TestCLIValidateFailsOnAMalformedManifest(t *testing.T) {
	e := newEnv(t, strings.Replace(cliManifest, "cnpatroni.io/boundary/v1", "cnpatroni.io/boundary/v2", 1))

	code, _, _ := e.run("validate")
	if code != cli.ExitManifestInvalid {
		t.Fatalf("exit = %d, want %d", code, cli.ExitManifestInvalid)
	}
}

func TestCLIValidateEmitsJSON(t *testing.T) {
	e := newEnv(t, strings.Replace(cliManifest, "cnpatroni.io/boundary/v1", "cnpatroni.io/boundary/v2", 1))

	code, stdout, _ := e.run("validate", "--format", "json")
	if code != cli.ExitManifestInvalid {
		t.Fatalf("exit = %d, want %d", code, cli.ExitManifestInvalid)
	}
	if !strings.Contains(stdout, `"code": "V1"`) {
		t.Errorf("JSON output should carry the finding code:\n%s", stdout)
	}
}

func TestCLIExitZeroSuppressesTheExitCode(t *testing.T) {
	e := newEnv(t, cliManifest)
	e.git.Write("docs/src/faq.md", "# Frequently asked questions\n\nFork edit.\n")
	e.git.Commit("edit the documentation")

	code, stdout, _ := e.run("--exit-zero", "validate", "--drift")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 with --exit-zero", code)
	}
	if !strings.Contains(stdout, "docs/src/faq.md") {
		t.Error("--exit-zero must still print the findings")
	}
}

func TestCLIReportWritesBothArtifacts(t *testing.T) {
	e := newEnv(t, cliManifest)
	e.git.Checkout("upstream")
	e.git.Write("internal/controller/replicas.go", "package controller\n\n// TargetPrimary\n// upstream change\n")
	e.git.Commit("fix: adjust the replica ordering")
	e.git.Checkout("main")

	outDir := filepath.Join(t.TempDir(), "out")
	code, stdout, stderr := e.run("report", "--to", "upstream", "--out-dir", outDir, "--stdout")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	for _, name := range []string{"report.json", "report.md"} {
		//nolint:gosec // outDir is a test temporary directory
		body, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if !strings.Contains(string(body), "replicas.go") {
			t.Errorf("%s does not name the boundary file", name)
		}
	}
	if !strings.Contains(stdout, "Upstream integration report") {
		t.Errorf("--stdout should print the markdown report:\n%s", stdout)
	}
}

// A clone without the upstream remote must say what to run, not fail obscurely.
func TestCLIReportReportsAMissingUpstreamRemote(t *testing.T) {
	e := newEnv(t, cliManifest)

	code, _, stderr := e.run("report")
	if code != cli.ExitEnvironment {
		t.Fatalf("exit = %d, want %d\n%s", code, cli.ExitEnvironment, stderr)
	}
	if !strings.Contains(stderr, "setup") {
		t.Errorf("stderr should point at the setup command:\n%s", stderr)
	}
}

func TestCLISetupDryRunPrintsTheCommands(t *testing.T) {
	e := newEnv(t, cliManifest)

	code, stdout, stderr := e.run("setup", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "git remote add --no-tags upstream") {
		t.Errorf("stdout should print the remote command:\n%s", stdout)
	}
	if !strings.Contains(stdout, "https://github.com/cloudnative-pg/cloudnative-pg") {
		t.Errorf("stdout should use the URL recorded in the baseline:\n%s", stdout)
	}
}

func TestCLIVersion(t *testing.T) {
	e := newEnv(t, cliManifest)

	code, stdout, _ := e.run("version")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("version must print something")
	}
}
