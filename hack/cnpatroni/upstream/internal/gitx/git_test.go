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
	"path/filepath"
	"testing"

	"github.com/postgres-ai/cnpatroni-upstream/internal/gittest"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
)

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
