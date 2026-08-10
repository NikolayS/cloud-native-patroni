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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/gittest"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/gitx"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/setup"
)

// newUpstream builds a stand-in for the upstream CloudNativePG repository.
func newUpstream(t *testing.T) *gittest.Fixture {
	t.Helper()

	u := gittest.New(t)
	u.Write("pkg/management/postgres/instance.go", "package postgres\n")
	u.Commit("upstream one")
	u.Tag("v1.30.0", "HEAD")
	u.Write("pkg/management/postgres/instance.go", "package postgres\n// two\n")
	u.Commit("upstream two")

	return u
}

func options(repo *gitx.Repo, url string) setup.Options {
	return setup.Options{
		Repo:   repo,
		URL:    url,
		Remote: "upstream",
		Track:  "main",
		Out:    io.Discard,
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DryRun = true

	result, err := setup.Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Steps) == 0 {
		t.Fatal("a dry run must still print the steps it would take")
	}

	present, err := fork.Repo(t).HasRemote("upstream")
	if err != nil {
		t.Fatalf("HasRemote: %v", err)
	}
	if present {
		t.Error("a dry run must not add the remote")
	}
}

func TestSetupAddsTheRemoteAndFetches(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	head := fork.Commit("fork")

	if _, err := setup.Run(options(fork.Repo(t), "file://"+upstream.Root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	repo := fork.Repo(t)
	present, err := repo.HasRemote("upstream")
	if err != nil || !present {
		t.Fatalf("remote present = %v, err = %v", present, err)
	}
	if _, err := repo.Resolve("upstream/main"); err != nil {
		t.Fatalf("upstream/main did not become resolvable: %v", err)
	}
	if _, err := repo.Resolve("v1.30.0"); err != nil {
		t.Fatalf("the upstream version tag was not fetched: %v", err)
	}

	if got := strings.TrimSpace(fork.Git("rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved to %s, want %s", got, head)
	}
	if status := fork.Git("status", "--porcelain"); status != "" {
		t.Errorf("setup left worktree changes, want empty porcelain status: %q", status)
	}
}

func TestSetupIsIdempotent(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	url := "file://" + upstream.Root
	first, err := setup.Run(options(fork.Repo(t), url))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := setup.Run(options(fork.Repo(t), url))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if countApplied(first) == 0 {
		t.Fatal("the first run must apply at least one step")
	}
	for _, step := range second.Steps {
		if step.Name == "add the upstream remote" && !step.Skipped {
			t.Error("the second run must not add the remote again")
		}
		if strings.HasPrefix(step.Name, "record the fetch refspec") && !step.Skipped {
			t.Errorf("the second run must not rewrite %q", step.Name)
		}
	}

	refspecs := fork.Git("config", "--get-all", "remote.upstream.fetch")
	if strings.Count(strings.TrimSpace(refspecs), "\n") != 1 {
		t.Errorf("refspecs were duplicated:\n%s", refspecs)
	}
}

func TestSetupRefusesADifferentRemoteURL(t *testing.T) {
	upstream := newUpstream(t)
	other := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")
	fork.AddRemote("upstream", other)

	_, err := setup.Run(options(fork.Repo(t), "file://"+upstream.Root))
	if err == nil {
		t.Fatal("setup must refuse to repoint an existing remote at a different URL")
	}
	if !strings.Contains(err.Error(), "upstream") {
		t.Errorf("error %q should name the remote", err)
	}
}

func TestSetupUnshallowsAShallowClone(t *testing.T) {
	upstream := newUpstream(t)

	shallowRoot := filepath.Join(t.TempDir(), "shallow")
	gittest.RunGit(t, "", "clone", "--depth", "1", "--no-local",
		"file://"+upstream.Root, shallowRoot)
	gittest.RunGit(t, shallowRoot, "config", "user.name", "CloudNativePatroni test")
	gittest.RunGit(t, shallowRoot, "config", "user.email", "test@example.invalid")

	repo, err := gitx.Open(shallowRoot)
	if err != nil {
		t.Fatalf("gitx.Open: %v", err)
	}
	if shallow, _ := repo.IsShallow(); !shallow {
		t.Fatal("the fixture clone is not shallow")
	}

	if _, err := setup.Run(options(repo, "file://"+upstream.Root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	shallow, err := repo.IsShallow()
	if err != nil {
		t.Fatalf("IsShallow: %v", err)
	}
	if shallow {
		t.Fatal("setup must unshallow the clone")
	}
}

func TestSetupNeverRunsADestructiveCommand(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DryRun = true

	result, err := setup.Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	forbidden := []string{"push", "reset", "checkout", "rebase", "commit", "--amend", "--force", "clean"}
	for _, step := range result.Steps {
		line := strings.Join(step.Command, " ")
		for _, word := range forbidden {
			if strings.Contains(line, word) {
				t.Errorf("step %q runs a destructive command: %s", step.Name, line)
			}
		}
	}
}

func countApplied(r *setup.Result) int {
	applied := 0
	for _, step := range r.Steps {
		if !step.Skipped {
			applied++
		}
	}

	return applied
}

// driverBinary writes a stand-in for the tool's own executable, so that the
// registration tests do not have to copy a real one.
func driverBinary(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cnpatroni-upstream")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 2\n"), 0o700); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

func TestSetupRegistersTheBoundaryMergeDriver(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DriverBinary = driverBinary(t)
	if _, err := setup.Run(opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	name := strings.TrimSpace(fork.Git("config", "--get", "merge.cnpatroni-boundary.name"))
	if name == "" {
		t.Error("the merge driver was declared without a human-readable name")
	}

	driver := strings.TrimSpace(fork.Git("config", "--get", "merge.cnpatroni-boundary.driver"))
	// Git hands the whole command to a shell, so the path and every placeholder
	// are single-quoted; see TestSetupQuotesTheRegisteredDriverPath.
	quoted, ok := strings.CutSuffix(driver, " merge-driver '%O' '%A' '%B' '%L' '%P'")
	if !ok {
		t.Fatalf("driver command %q does not pass git's five placeholders, quoted", driver)
	}
	binary, ok := strings.CutPrefix(quoted, "'")
	if !ok {
		t.Fatalf("driver command %q does not quote the driver path", driver)
	}
	binary, ok = strings.CutSuffix(binary, "'")
	if !ok {
		t.Fatalf("driver command %q does not quote the driver path", driver)
	}
	if !filepath.IsAbs(binary) {
		t.Fatalf("driver command %q must start with an absolute path", driver)
	}
	if info, err := os.Stat(binary); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("the registered driver %q is not an executable file: %v", binary, err)
	}

	// The installed binary must sit outside the worktree, or every merge would
	// start from a dirty tree.
	if strings.HasPrefix(binary, filepath.Join(fork.Root, "bin")) {
		t.Errorf("the driver was installed inside the worktree: %s", binary)
	}
	if status := strings.TrimSpace(fork.Git("status", "--porcelain")); status != "" {
		t.Errorf("registering the driver dirtied the worktree:\n%s", status)
	}
}

func TestSetupQuotesTheRegisteredDriverPath(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.NewNamed(t, "x;true;#")
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	repo := fork.Repo(t)
	opts := options(repo, "file://"+upstream.Root)
	opts.DriverBinary = driverBinary(t)
	if _, err := setup.Run(opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	commonDir, err := repo.CommonDir()
	if err != nil {
		t.Fatalf("CommonDir: %v", err)
	}
	destination := filepath.Join(commonDir, "cnpatroni", "cnpatroni-upstream")
	want := "'" + destination + "' merge-driver '%O' '%A' '%B' '%L' '%P'"
	if got := strings.TrimSpace(fork.Git("config", "--get", "merge.cnpatroni-boundary.driver")); got != want {
		t.Errorf("driver command = %q, want %q", got, want)
	}

	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("the installed driver %q does not exist: %v", destination, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("the installed driver %q is not executable: mode %v", destination, info.Mode())
	}
}

func TestSetupRefusesAGitDirectoryThatCannotBeQuoted(t *testing.T) {
	for _, dirName := range []string{"it's-a-clone", "we\nird"} {
		t.Run(dirName, func(t *testing.T) {
			upstream := newUpstream(t)
			fork := gittest.NewNamed(t, dirName)
			fork.Write("a.go", "package a\n")
			fork.Commit("fork")

			repo := fork.Repo(t)
			commonDir, err := repo.CommonDir()
			if err != nil {
				t.Fatalf("CommonDir: %v", err)
			}
			destination := filepath.Join(commonDir, "cnpatroni", "cnpatroni-upstream")
			opts := options(repo, "file://"+upstream.Root)
			opts.DriverBinary = driverBinary(t)

			_, err = setup.Run(opts)
			if err == nil {
				t.Fatalf("Run returned no error for git directory %q", commonDir)
			}
			if !strings.Contains(err.Error(), strconv.Quote(destination)) || !strings.Contains(err.Error(), "move the clone") {
				t.Errorf("error %q should name %q and say the clone must be moved", err, destination)
			}
			if got, _, configErr := repo.RunAllowFail("config", "--get", "merge.cnpatroni-boundary.driver"); configErr == nil {
				t.Errorf("the boundary driver was registered as %q after setup rejected its path", strings.TrimSpace(got))
			}
		})
	}
}

func TestShellQuoteProducesASingleShellWord(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}

	for _, input := range []string{
		"plain",
		"with space",
		"x;true;#",
		"$(touch marker)",
		`back\slash`,
		`"double"`,
	} {
		t.Run(input, func(t *testing.T) {
			upstream := newUpstream(t)
			fork := gittest.NewNamed(t, input)
			fork.Write("a.go", "package a\n")
			fork.Commit("fork")

			repo := fork.Repo(t)
			opts := options(repo, "file://"+upstream.Root)
			opts.DriverBinary = driverBinary(t)
			if _, err := setup.Run(opts); err != nil {
				t.Fatalf("Run: %v", err)
			}

			driver := strings.TrimSpace(fork.Git("config", "--get", "merge.cnpatroni-boundary.driver"))
			// The placeholders are quoted too: %P is a tracked pathname that
			// upstream controls, so leaving it bare is a second injection
			// surface of exactly the kind this test exists to close.
			quoted, ok := strings.CutSuffix(driver, " merge-driver '%O' '%A' '%B' '%L' '%P'")
			if !ok {
				t.Fatalf("driver command %q does not have the expected argument suffix", driver)
			}
			commonDir, err := repo.CommonDir()
			if err != nil {
				t.Fatalf("CommonDir: %v", err)
			}
			want := filepath.Join(commonDir, "cnpatroni", "cnpatroni-upstream")
			cmd := exec.Command(bash, "-c", "printf '%s' "+quoted)
			cmd.Dir = t.TempDir()
			got, err := cmd.Output()
			if err != nil {
				t.Fatalf("evaluating shell word %q: %v", quoted, err)
			}
			if string(got) != want {
				t.Errorf("shell word %q produced %q, want %q", quoted, got, want)
			}
		})
	}
}

func TestSetupMergeDriverRegistrationIsIdempotent(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DriverBinary = driverBinary(t)
	if _, err := setup.Run(opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := setup.Run(opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	for _, step := range second.Steps {
		if strings.Contains(step.Name, "merge driver") && !step.Skipped {
			t.Errorf("the second run repeated %q", step.Name)
		}
	}
}

func TestDryRunDoesNotRegisterTheMergeDriver(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DriverBinary = driverBinary(t)
	opts.DryRun = true

	result, err := setup.Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var announced bool
	for _, step := range result.Steps {
		if strings.Contains(step.Name, "merge driver") {
			announced = true
		}
	}
	if !announced {
		t.Error("a dry run must still print the merge-driver steps it would take")
	}

	if _, _, err := fork.Repo(t).RunAllowFail("config", "--get", "merge.cnpatroni-boundary.driver"); err == nil {
		t.Error("a dry run must not register the merge driver")
	}
}

// A registration pointing at a binary that is not there is worse than none: git
// would report every boundary merge as failed for the wrong reason.
func TestSetupRefusesAMissingDriverBinary(t *testing.T) {
	upstream := newUpstream(t)
	fork := gittest.New(t)
	fork.Write("a.go", "package a\n")
	fork.Commit("fork")

	opts := options(fork.Repo(t), "file://"+upstream.Root)
	opts.DriverBinary = filepath.Join(t.TempDir(), "absent")

	_, err := setup.Run(opts)
	if err == nil {
		t.Fatal("expected an error for a missing driver binary, got none")
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("error %q should name the missing binary", err)
	}
}
