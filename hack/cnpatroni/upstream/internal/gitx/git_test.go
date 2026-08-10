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

package gitx_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/postgres-ai/cnpatroni-upstream/internal/gittest"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
)

// A remedy is worth nothing if the command in it does not run. This tool is its
// own Go module, so `go run ./hack/...` from the repository root fails with
// "main module does not contain package"; the remedies have to carry the
// invocation that works.
func TestToolInvocationRuns(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no shell available: %v", err)
	}

	root := strings.TrimSpace(gittest.RunGit(t, "", "rev-parse", "--show-toplevel"))
	command := fmt.Sprintf(gitx.ToolInvocation, "version")

	cmd := exec.Command(shell, "-c", command) //nolint:gosec // the command under test is a constant
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the remedy %q does not run from %s: %v\n%s", command, root, err, out)
	}
	if !strings.Contains(string(out), "cnpatroni-upstream") {
		t.Errorf("the remedy %q printed %q, want the tool version", command, out)
	}
}

// Every remedy has to route through the one constant, so that the next fix
// happens once rather than three times.
func TestEnvironmentRemediesUseTheToolInvocation(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	f.Commit("base")

	remedies := []string{
		remedyFor(t, f.Repo(t).RequireRemote("upstream")),
		remedyFor(t, shallowCloneError(t)),
	}
	want := fmt.Sprintf(gitx.ToolInvocation, "setup")
	for _, remedy := range remedies {
		if !strings.Contains(remedy, want) {
			t.Errorf("remedy %q does not contain %q", remedy, want)
		}
		if strings.Contains(remedy, "go run ./hack/") {
			t.Errorf("remedy %q tells the reader to run the tool from the repository root, which fails", remedy)
		}
	}
}

func remedyFor(t *testing.T, err error) string {
	t.Helper()

	var envErr *gitx.EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("error %v is not an EnvironmentError", err)
	}

	return envErr.Remedy
}

func shallowCloneError(t *testing.T) error {
	t.Helper()

	origin := gittest.New(t)
	origin.Write("a.go", "package a\n")
	origin.Commit("one")
	origin.Write("b.go", "package b\n")
	origin.Commit("two")

	shallowRoot := filepath.Join(t.TempDir(), "shallow")
	gittest.RunGit(t, "", "clone", "--depth", "1", "--no-local", "file://"+origin.Root, shallowRoot)

	repo, err := gitx.Open(shallowRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	return repo.RequireCompleteHistory()
}

func TestOpenRejectsNonRepository(t *testing.T) {
	dir := t.TempDir()

	if _, err := gitx.Open(dir); err == nil {
		t.Fatalf("expected an error opening %q, got none", dir)
	}
}

func TestOpenFindsRepositoryRoot(t *testing.T) {
	f := gittest.New(t)
	f.Write("pkg/a/b.go", "package b\n")
	f.Commit("seed")

	repo, err := gitx.Open(filepath.Join(f.Root, "pkg", "a"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	wantRoot, err := filepath.EvalSymlinks(f.Root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	gotRoot, err := filepath.EvalSymlinks(repo.Root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if gotRoot != wantRoot {
		t.Fatalf("root = %q, want %q", gotRoot, wantRoot)
	}
}

func TestShortSHA(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "full SHA",
			input: "0123456789abcdef0123456789abcdef01234567",
			want:  "0123456789ab",
		},
		{name: "twelve characters", input: "abcdefghijkl", want: "abcdefghijkl"},
		{name: "thirteen characters", input: "abcdefghijklm", want: "abcdefghijkl"},
		{name: "empty", input: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gitx.ShortSHA(tc.input); got != tc.want {
				t.Errorf("ShortSHA(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestChangedFilesReportsStatusAndPaths(t *testing.T) {
	f := gittest.New(t)
	f.Write("keep.go", "package keep\n")
	f.Write("gone.go", "package gone\n")
	base := f.Commit("base")

	f.Write("keep.go", "package keep\n\n// changed\n")
	f.Write("added.go", "package added\n")
	f.Remove("gone.go")
	head := f.Commit("work")

	repo := f.Repo(t)
	changes, err := repo.ChangedFiles(base, head)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}

	got := map[string]string{}
	for _, c := range changes {
		got[c.Path] = c.Status
	}
	want := map[string]string{"keep.go": "M", "added.go": "A", "gone.go": "D"}
	for path, status := range want {
		if got[path] != status {
			t.Errorf("status for %q = %q, want %q", path, got[path], status)
		}
	}
	if len(got) != len(want) {
		t.Errorf("changed %d paths, want %d: %v", len(got), len(want), got)
	}
}

func TestNumStatCountsLines(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	base := f.Commit("base")
	f.Write("a.go", "package a\n\nconst X = 1\nconst Y = 2\n")
	head := f.Commit("grow")

	stats, err := f.Repo(t).NumStat(base, head)
	if err != nil {
		t.Fatalf("NumStat: %v", err)
	}
	if stats["a.go"].Insertions != 3 || stats["a.go"].Deletions != 0 {
		t.Fatalf("a.go = +%d/-%d, want +3/-0",
			stats["a.go"].Insertions, stats["a.go"].Deletions)
	}
}

func TestIsAncestor(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	base := f.Commit("base")
	f.Write("b.go", "package b\n")
	head := f.Commit("head")

	repo := f.Repo(t)
	ok, err := repo.IsAncestor(base, head)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ok {
		t.Error("base should be an ancestor of head")
	}

	ok, err = repo.IsAncestor(head, base)
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if ok {
		t.Error("head must not be an ancestor of base")
	}
}

func TestPathExistsAt(t *testing.T) {
	f := gittest.New(t)
	f.Write("gone.go", "package gone\n")
	base := f.Commit("base")
	f.Remove("gone.go")
	head := f.Commit("remove")

	repo := f.Repo(t)
	if ok, err := repo.PathExistsAt(base, "gone.go"); err != nil || !ok {
		t.Fatalf("gone.go at base: ok=%v err=%v, want ok=true", ok, err)
	}
	if ok, err := repo.PathExistsAt(head, "gone.go"); err != nil || ok {
		t.Fatalf("gone.go at head: ok=%v err=%v, want ok=false", ok, err)
	}
}

func TestResolveUnknownRefIsUsageError(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	f.Commit("base")

	_, err := f.Repo(t).Resolve("refs/heads/nope")
	if err == nil {
		t.Fatal("expected an error resolving a missing ref")
	}
	var envErr *gitx.EnvironmentError
	if errors.As(err, &envErr) {
		t.Fatalf("a missing ref must not be an EnvironmentError, got %v", envErr)
	}
}

func TestRequireUpstreamRemoteReportsRemedy(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	f.Commit("base")

	err := f.Repo(t).RequireRemote("upstream")
	if err == nil {
		t.Fatal("expected an error when the upstream remote is missing")
	}
	var envErr *gitx.EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("error %v is not an EnvironmentError", err)
	}
	if envErr.Remedy == "" {
		t.Error("EnvironmentError.Remedy must tell the maintainer what to run")
	}
}

func TestRequireCompleteHistoryDetectsShallowClone(t *testing.T) {
	origin := gittest.New(t)
	origin.Write("a.go", "package a\n")
	origin.Commit("one")
	origin.Write("b.go", "package b\n")
	origin.Commit("two")
	origin.Write("c.go", "package c\n")
	origin.Commit("three")

	shallowRoot := filepath.Join(t.TempDir(), "shallow")
	gittest.RunGit(t, "", "clone", "--depth", "1", "--no-local",
		"file://"+origin.Root, shallowRoot)

	repo, err := gitx.Open(shallowRoot)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	shallow, err := repo.IsShallow()
	if err != nil {
		t.Fatalf("IsShallow: %v", err)
	}
	if !shallow {
		t.Fatal("clone --depth 1 must be reported as shallow")
	}

	err = repo.RequireCompleteHistory()
	var envErr *gitx.EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("error %v is not an EnvironmentError", err)
	}
	if envErr.Remedy == "" {
		t.Error("EnvironmentError.Remedy must tell the maintainer what to run")
	}
}

func TestTreeBlobsIdentifiesContentAcrossPaths(t *testing.T) {
	f := gittest.New(t)
	f.Write("pkg/a.go", "package a\n")
	f.Write("pkg/b.go", "package b\n")
	base := f.Commit("base")

	// The same bytes at a new path, and an edit at the old one.
	f.Write("parked/a.go", "package a\n")
	f.Remove("pkg/a.go")
	f.Write("pkg/b.go", "package b\n\n// edited\n")
	f.Commit("park one file and edit the other")

	repo := f.Repo(t)
	baseBlobs, err := repo.TreeBlobs(base)
	if err != nil {
		t.Fatalf("TreeBlobs(base): %v", err)
	}
	headBlobs, err := repo.TreeBlobs("HEAD")
	if err != nil {
		t.Fatalf("TreeBlobs(HEAD): %v", err)
	}

	if len(baseBlobs) != 2 {
		t.Errorf("base tree has %d blobs, want 2: %v", len(baseBlobs), baseBlobs)
	}
	if headBlobs["parked/a.go"] != baseBlobs["pkg/a.go"] {
		t.Errorf("a verbatim move changed the blob: %q != %q",
			headBlobs["parked/a.go"], baseBlobs["pkg/a.go"])
	}
	if headBlobs["pkg/b.go"] == baseBlobs["pkg/b.go"] {
		t.Error("an edited file kept its blob")
	}
	if _, present := headBlobs["pkg/a.go"]; present {
		t.Error("a removed path is still listed at HEAD")
	}
}

func TestTreeBlobsRejectsAnUnknownRef(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	f.Commit("base")

	if _, err := f.Repo(t).TreeBlobs("no-such-ref"); err == nil {
		t.Fatal("expected an error for a ref that does not exist")
	}
}

func TestMergeTreeConflictsFindsOverlappingEdit(t *testing.T) {
	f := gittest.New(t)
	f.Write("shared.go", "package shared\n\nconst V = 1\n")
	f.Commit("base")
	f.Branch("upstream")

	f.Write("shared.go", "package shared\n\nconst V = 2\n")
	f.Commit("ours")

	f.Checkout("upstream")
	f.Write("shared.go", "package shared\n\nconst V = 3\n")
	f.Commit("theirs")
	f.Checkout("main")

	conflicts, err := f.Repo(t).MergeTreeConflicts("main", "upstream")
	if err != nil {
		t.Fatalf("MergeTreeConflicts: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0] != "shared.go" {
		t.Fatalf("conflicts = %v, want [shared.go]", conflicts)
	}
}

func TestMergeTreeConflictsIsEmptyOnCleanMerge(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	f.Commit("base")
	f.Branch("upstream")

	f.Write("ours.go", "package ours\n")
	f.Commit("ours")

	f.Checkout("upstream")
	f.Write("theirs.go", "package theirs\n")
	f.Commit("theirs")
	f.Checkout("main")

	conflicts, err := f.Repo(t).MergeTreeConflicts("main", "upstream")
	if err != nil {
		t.Fatalf("MergeTreeConflicts: %v", err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none", conflicts)
	}
}

func TestGrepFilesAtScansASpecificRef(t *testing.T) {
	f := gittest.New(t)
	f.Write("plain.go", "package plain\n")
	f.Write("ha.go", "package ha\n\n// TargetPrimary is decided here.\n")
	head := f.Commit("base")

	matches, err := f.Repo(t).GrepFilesAt(head, []string{"TargetPrimary"}, nil)
	if err != nil {
		t.Fatalf("GrepFilesAt: %v", err)
	}
	if len(matches) != 1 || matches[0] != "ha.go" {
		t.Fatalf("matches = %v, want [ha.go]", matches)
	}
}

// The shared git directory is where per-clone artefacts belong: it is outside
// the worktree, so writing there cannot dirty a merge.
func TestCommonDirIsAbsoluteAndOutsideTheWorktree(t *testing.T) {
	f := gittest.New(t)
	f.Write("a.go", "package a\n")
	f.Commit("base")

	dir, err := f.Repo(t).CommonDir()
	if err != nil {
		t.Fatalf("CommonDir: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Errorf("CommonDir returned the relative path %q", dir)
	}
	if info, statErr := os.Stat(dir); statErr != nil || !info.IsDir() {
		t.Fatalf("CommonDir returned %q, which is not a directory: %v", dir, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "HEAD")); statErr != nil {
		t.Errorf("CommonDir returned %q, which does not hold HEAD: %v", dir, statErr)
	}
}

func TestCommonDirRejectsANonRepository(t *testing.T) {
	repo := &gitx.Repo{Root: t.TempDir()}

	if _, err := repo.CommonDir(); err == nil {
		t.Fatal("expected an error outside a repository, got none")
	}
}
