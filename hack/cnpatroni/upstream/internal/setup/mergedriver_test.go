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

package setup_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/boundary"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/gittest"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/setup"
)

// guardManifest declares one boundary file, so that the generated attributes
// route exactly that path through the always-conflict merge driver.
const guardManifest = `schema: cnpatroni.io/boundary/v1
generated_from: fixture
provisional: false
default_ownership: upstream-untouched
audit_scan: {include: ["**/*.go"], exclude: []}
audit_terms: []
generated_artifacts: []
required_paths: []
rules:
  - id: adapt.process-primitives
    ownership: adapted
    paths: ["pkg/management/postgres/instance.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`

// buildTool compiles the command line so that a real git merge can invoke it.
func buildTool(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no Go toolchain to build the merge driver with: %v", err)
	}

	binary := filepath.Join(t.TempDir(), "cnpatroni-upstream")
	cmd := exec.Command("go", "build", "-o", binary, "./cmd/cnpatroni-upstream")
	cmd.Dir = filepath.Join("..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	return binary
}

// TestTheGuardStopsAMergeGitWouldHaveTakenSilently is the end-to-end proof of
// the mechanism docs/cnpatroni/fork-maintenance.md asks for: the fork adapts one
// end of a boundary file, upstream changes the other end, and git's own
// three-way merge resolves that without a word. With the generated attributes in
// place and the driver registered, the same merge has to stop.
func TestTheGuardStopsAMergeGitWouldHaveTakenSilently(t *testing.T) {
	binary := buildTool(t)

	const boundaryPath = "pkg/management/postgres/instance.go"
	const untouchedPath = "docs/src/faq.md"

	manifest, err := boundary.Parse([]byte(guardManifest))
	if err != nil {
		t.Fatalf("boundary.Parse: %v", err)
	}

	fork := gittest.New(t)
	fork.Write(boundaryPath, "package postgres\n\nfunc first() {}\nfunc second() {}\n")
	fork.Write(untouchedPath, "# Frequently asked questions\n\nOne.\n")
	fork.Write(".gitattributes", boundary.GitAttributes(manifest))
	fork.Commit("fork base")
	fork.Branch("upstream-change")

	// Upstream appends at the bottom of both files.
	fork.Checkout("upstream-change")
	fork.Write(boundaryPath, "package postgres\n\nfunc first() {}\nfunc second() {}\nfunc upstreamAddition() {}\n")
	fork.Write(untouchedPath, "# Frequently asked questions\n\nOne.\n\nTwo.\n")
	fork.Commit("upstream: add a code path")

	// CloudNativePatroni adapts the top of both files.
	fork.Checkout("main")
	fork.Write(boundaryPath, "package postgres\n\nfunc adapted() {}\nfunc second() {}\n")
	fork.Write(untouchedPath, "# Questions\n\nOne.\n")
	fork.Commit("adapt the boundary file")

	// Control: without the guard git merges both files cleanly and says nothing.
	conflicts, err := fork.Repo(t).MergeTreeConflicts("main", "upstream-change")
	if err != nil {
		t.Fatalf("MergeTreeConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("the fixture does not reproduce a silent automerge: git predicts conflicts in %v", conflicts)
	}

	upstream := newUpstream(t)
	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DriverBinary = binary
	if _, err := setup.Run(opts); err != nil {
		t.Fatalf("setup.Run: %v", err)
	}

	out, code, _ := fork.Repo(t).RunAllowFail("merge", "--no-ff", "--no-edit", "upstream-change")
	if code == 0 {
		t.Fatalf("the merge succeeded; the boundary guard did not fire\n%s", out)
	}

	unmerged, err := fork.Repo(t).Run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		t.Fatalf("listing the unmerged paths: %v", err)
	}
	got := strings.Fields(unmerged)
	if len(got) != 1 || got[0] != boundaryPath {
		t.Fatalf("unmerged paths = %v, want only %q", got, boundaryPath)
	}

	// The guard is targeted, not a blanket refusal: the undeclared file merged.
	faq := readWorktree(t, fork.Root, untouchedPath)
	if !strings.Contains(faq, "# Questions") || !strings.Contains(faq, "Two.") {
		t.Errorf("the undeclared file was not merged normally:\n%s", faq)
	}

	// The boundary file keeps both sides' work, so resolving it is a review of
	// the upstream hunks rather than a re-application of them.
	instance := readWorktree(t, fork.Root, boundaryPath)
	for _, want := range []string{"func adapted() {}", "func upstreamAddition() {}"} {
		if !strings.Contains(instance, want) {
			t.Errorf("the conflicted file lost %q:\n%s", want, instance)
		}
	}
}

func TestTheGuardHoldsWhenTheClonePathCarriesShellMetacharacters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shell-command injection fixture is not portable to Windows")
	}

	binary := buildTool(t)

	const boundaryPath = "pkg/management/postgres/instance.go"
	const untouchedPath = "docs/src/faq.md"

	manifest, err := boundary.Parse([]byte(guardManifest))
	if err != nil {
		t.Fatalf("boundary.Parse: %v", err)
	}

	fork := gittest.NewNamed(t, "x;true;#")
	fork.Write(boundaryPath, "package postgres\n\nfunc first() {}\nfunc second() {}\n")
	fork.Write(untouchedPath, "# Frequently asked questions\n\nOne.\n")
	fork.Write(".gitattributes", boundary.GitAttributes(manifest))
	fork.Commit("fork base")
	fork.Branch("upstream-change")

	// Upstream appends at the bottom of both files.
	fork.Checkout("upstream-change")
	fork.Write(boundaryPath, "package postgres\n\nfunc first() {}\nfunc second() {}\nfunc upstreamAddition() {}\n")
	fork.Write(untouchedPath, "# Frequently asked questions\n\nOne.\n\nTwo.\n")
	fork.Commit("upstream: add a code path")

	// CloudNativePatroni adapts the top of both files.
	fork.Checkout("main")
	fork.Write(boundaryPath, "package postgres\n\nfunc adapted() {}\nfunc second() {}\n")
	fork.Write(untouchedPath, "# Questions\n\nOne.\n")
	fork.Commit("adapt the boundary file")

	// Control: without the guard git merges both files cleanly and says nothing.
	conflicts, err := fork.Repo(t).MergeTreeConflicts("main", "upstream-change")
	if err != nil {
		t.Fatalf("MergeTreeConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("the fixture does not reproduce a silent automerge: git predicts conflicts in %v", conflicts)
	}

	upstream := newUpstream(t)
	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DriverBinary = binary
	if _, err := setup.Run(opts); err != nil {
		t.Fatalf("setup.Run: %v", err)
	}

	out, code, _ := fork.Repo(t).RunAllowFail("merge", "--no-ff", "--no-edit", "upstream-change")
	if code == 0 {
		t.Fatalf("the merge succeeded; the boundary guard did not fire\n%s", out)
	}

	unmerged, err := fork.Repo(t).Run("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		t.Fatalf("listing the unmerged paths: %v", err)
	}
	got := strings.Fields(unmerged)
	if len(got) != 1 || got[0] != boundaryPath {
		t.Fatalf("unmerged paths = %v, want only %q", got, boundaryPath)
	}

	instance := readWorktree(t, fork.Root, boundaryPath)
	for _, want := range []string{"func adapted() {}", "func upstreamAddition() {}"} {
		if !strings.Contains(instance, want) {
			t.Errorf("the conflicted file lost %q:\n%s", want, instance)
		}
	}

	faq := readWorktree(t, fork.Root, untouchedPath)
	if !strings.Contains(faq, "# Questions") || !strings.Contains(faq, "Two.") {
		t.Errorf("the undeclared file was not merged normally:\n%s", faq)
	}
}

func readWorktree(t *testing.T, root, path string) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path))) //nolint:gosec // a test fixture path
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}

	return string(body)
}
