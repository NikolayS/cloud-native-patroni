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
	"path/filepath"
	"strings"
	"testing"

	"github.com/postgres-ai/cnpatroni-upstream/internal/gittest"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
	"github.com/postgres-ai/cnpatroni-upstream/internal/setup"
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
