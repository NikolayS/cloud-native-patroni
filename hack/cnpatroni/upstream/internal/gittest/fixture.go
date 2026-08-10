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

// Package gittest builds throwaway git repositories for the upstream tooling
// tests. Every repository lives under t.TempDir(), so the Go test runner
// removes it when the test finishes.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
)

// fixedDate keeps commit identifiers reproducible across runs.
const fixedDate = "2026-01-01T00:00:00+00:00"

// Fixture is a disposable git repository.
type Fixture struct {
	t    *testing.T
	Root string
}

// New initialises an empty repository on a branch named main.
func New(t *testing.T) *Fixture {
	t.Helper()

	return NewNamed(t, "repo")
}

// NewNamed exists because merge-driver tests need a repository whose path carries shell metacharacters.
func NewNamed(t *testing.T, dirName string) *Fixture {
	t.Helper()

	root := filepath.Join(t.TempDir(), dirName)
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("MkdirAll(%q): %v", root, err)
	}

	f := &Fixture{t: t, Root: root}
	f.Git("init", "--quiet", "--initial-branch", "main")
	f.Git("config", "user.name", "CloudNativePatroni test")
	f.Git("config", "user.email", "test@example.invalid")
	f.Git("config", "commit.gpgsign", "false")
	f.Git("config", "core.autocrlf", "false")
	f.Git("config", "gc.auto", "0")

	return f
}

// Repo opens the fixture through the production git wrapper.
func (f *Fixture) Repo(t *testing.T) *gitx.Repo {
	t.Helper()

	repo, err := gitx.Open(f.Root)
	if err != nil {
		t.Fatalf("gitx.Open(%q): %v", f.Root, err)
	}

	return repo
}

// Write creates or replaces a file, creating parent directories as needed.
func (f *Fixture) Write(path, content string) {
	f.t.Helper()

	full := filepath.Join(f.Root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		f.t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		f.t.Fatalf("WriteFile: %v", err)
	}
}

// Remove deletes a file from the worktree.
func (f *Fixture) Remove(path string) {
	f.t.Helper()

	if err := os.Remove(filepath.Join(f.Root, filepath.FromSlash(path))); err != nil {
		f.t.Fatalf("Remove: %v", err)
	}
}

// Commit stages everything and records a commit, returning its identifier.
func (f *Fixture) Commit(subject string) string {
	f.t.Helper()

	f.Git("add", "--all")
	f.Git("commit", "--quiet", "--allow-empty", "--message", subject)

	return strings.TrimSpace(f.Git("rev-parse", "HEAD"))
}

// Branch creates a branch at HEAD without checking it out.
func (f *Fixture) Branch(name string) {
	f.t.Helper()
	f.Git("branch", name)
}

// Checkout switches the worktree to a ref.
func (f *Fixture) Checkout(ref string) {
	f.t.Helper()
	f.Git("checkout", "--quiet", ref)
}

// Tag creates a lightweight tag.
func (f *Fixture) Tag(name, ref string) {
	f.t.Helper()
	f.Git("tag", name, ref)
}

// AddRemote registers another fixture as a remote reachable over file://.
func (f *Fixture) AddRemote(name string, other *Fixture) {
	f.t.Helper()
	f.Git("remote", "add", name, "file://"+other.Root)
}

// Git runs a git command inside the fixture and fails the test on error.
func (f *Fixture) Git(args ...string) string {
	f.t.Helper()

	return RunGit(f.t, f.Root, args...)
}

// RunGit runs git in dir, or in the process working directory when dir is
// empty, and fails the test if git reports an error.
func RunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+fixedDate,
		"GIT_COMMITTER_DATE="+fixedDate,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return string(out)
}
