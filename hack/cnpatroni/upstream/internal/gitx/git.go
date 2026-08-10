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

// Package gitx is a thin, testable wrapper over the git plumbing commands the
// CloudNativePatroni upstream tooling needs. It never mutates the worktree.
package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// EnvironmentError reports a repository that cannot answer the question being
// asked because it was cloned or configured incompletely. It is distinct from
// an ordinary error so that callers can exit with a dedicated code and print a
// remedy instead of a stack of git output.
type EnvironmentError struct {
	Reason string
	Remedy string
}

// Error implements error.
func (e *EnvironmentError) Error() string {
	return e.Reason + "\n" + e.Remedy
}

// ToolInvocation is the shell command that runs the CloudNativePatroni upstream
// tool from a repository checkout, with %s replaced by the subcommand and its
// flags. Every remedy this tooling prints has to use it: the tool lives in its
// own Go module, so `go run ./hack/cnpatroni/upstream/cmd/cnpatroni-upstream`
// from the repository root fails with "main module does not contain package".
// The invocation changes directory into the module first, in a subshell, so
// that the caller's working directory survives.
const ToolInvocation = "(cd hack/cnpatroni/upstream && go run ./cmd/cnpatroni-upstream %s)"

// Repo is a git repository rooted at Root.
type Repo struct {
	Root string
}

// FileChange is one entry of a name-status diff.
type FileChange struct {
	Status string
	Path   string
}

// LineDelta is the insertion and deletion count for one path.
type LineDelta struct {
	Insertions int
	Deletions  int
}

// Commit is one entry of a git log listing.
type Commit struct {
	SHA     string
	Author  string
	Date    string
	Subject string
}

// Open locates the repository that contains dir.
func Open(dir string) (*Repo, error) {
	out, _, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%q is not inside a git repository: %w", dir, err)
	}

	return &Repo{Root: strings.TrimSpace(out)}, nil
}

// Run executes a git command in the repository and returns its standard output.
func (r *Repo) Run(args ...string) (string, error) {
	out, _, err := run(r.Root, args...)

	return out, err
}

// RunAllowFail executes a git command and returns its exit code instead of an
// error when git itself ran but reported a non-zero status.
func (r *Repo) RunAllowFail(args ...string) (string, int, error) {
	return run(r.Root, args...)
}

func run(dir string, args ...string) (string, int, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return string(out), exitErr.ExitCode(),
				fmt.Errorf("git %s: exit %d: %s",
					strings.Join(args, " "), exitErr.ExitCode(), strings.TrimSpace(stderr.String()))
		}

		return string(out), -1, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return string(out), 0, nil
}

// CommonDir returns the absolute path of the repository's shared git
// directory. It is where a per-clone artefact belongs: the directory is outside
// the worktree, so nothing written there can dirty a merge or show up in
// `git status`.
func (r *Repo) CommonDir() (string, error) {
	out, err := r.Run("rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}

	dir := strings.TrimSpace(out)
	if dir == "" {
		return "", fmt.Errorf("git did not report a git directory for %q", r.Root)
	}
	if !filepath.IsAbs(dir) {
		// git reports the path relative to the directory it ran in.
		dir = filepath.Join(r.Root, dir)
	}

	return dir, nil
}

// IsShallow reports whether the repository has a truncated history.
func (r *Repo) IsShallow() (bool, error) {
	out, err := r.Run("rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(out) == "true", nil
}

// HasRemote reports whether a remote of that name is configured.
func (r *Repo) HasRemote(name string) (bool, error) {
	out, err := r.Run("remote")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Fields(out) {
		if line == name {
			return true, nil
		}
	}

	return false, nil
}

// RequireRemote fails with an EnvironmentError when the remote is missing.
func (r *Repo) RequireRemote(name string) error {
	present, err := r.HasRemote(name)
	if err != nil {
		return err
	}
	if present {
		return nil
	}

	return &EnvironmentError{
		Reason: fmt.Sprintf("this clone has no %q remote, so upstream CloudNativePG cannot be compared", name),
		Remedy: fmt.Sprintf("Run `%s` once in this clone, or `%s` to print the exact git commands first.",
			fmt.Sprintf(ToolInvocation, "setup"), fmt.Sprintf(ToolInvocation, "setup --dry-run")),
	}
}

// RequireCompleteHistory fails with an EnvironmentError on a shallow clone.
func (r *Repo) RequireCompleteHistory() error {
	shallow, err := r.IsShallow()
	if err != nil {
		return err
	}
	if !shallow {
		return nil
	}

	return &EnvironmentError{
		Reason: "this clone is shallow, so the fork baseline and the upstream range cannot be resolved",
		Remedy: fmt.Sprintf("Run `%s` once in this clone, or in CI check out with `fetch-depth: 0`.",
			fmt.Sprintf(ToolInvocation, "setup")),
	}
}

// Resolve turns a ref into a full commit identifier.
func (r *Repo) Resolve(ref string) (string, error) {
	out, err := r.Run("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve ref %q: %w", ref, err)
	}

	return strings.TrimSpace(out), nil
}

// Describe returns `git describe` output, or the abbreviated commit when the
// repository carries no tags.
func (r *Repo) Describe(ref string) string {
	out, _, err := r.RunAllowFail("describe", "--tags", "--always", ref)
	if err != nil {
		return ShortSHA(ref)
	}

	return strings.TrimSpace(out)
}

// CommitDate returns the author date of a commit in YYYY-MM-DD form.
func (r *Repo) CommitDate(ref string) string {
	out, _, err := r.RunAllowFail("show", "--no-patch", "--format=%ad", "--date=short", ref)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(out)
}

// CurrentBranch returns the checked-out branch name, or an empty string when
// the repository has a detached HEAD.
func (r *Repo) CurrentBranch() string {
	out, _, err := r.RunAllowFail("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(out)
	if name == "HEAD" {
		return ""
	}

	return name
}

// IsAncestor reports whether a is an ancestor of b.
func (r *Repo) IsAncestor(a, b string) (bool, error) {
	_, code, err := r.RunAllowFail("merge-base", "--is-ancestor", a, b)
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, err
	}
}

// MergeBase returns the best common ancestor of two refs.
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := r.Run("merge-base", a, b)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// PathExistsAt reports whether a path is present in the tree of a ref.
func (r *Repo) PathExistsAt(ref, path string) (bool, error) {
	_, code, err := r.RunAllowFail("cat-file", "-e", ref+":"+path)
	switch {
	case code == 0:
		return true, nil
	case code > 0:
		return false, nil
	default:
		return false, err
	}
}

// ChangedFiles lists the paths that differ between two commits. Renames are
// disabled so that the resulting path set stays comparable with the path set of
// any other diff.
func (r *Repo) ChangedFiles(from, to string) ([]FileChange, error) {
	out, err := r.Run("diff-tree", "-r", "--no-renames", "-z", "--name-status", from, to)
	if err != nil {
		return nil, err
	}

	fields := splitNUL(out)
	changes := make([]FileChange, 0, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		changes = append(changes, FileChange{Status: fields[i], Path: fields[i+1]})
	}

	return changes, nil
}

// NumStat returns per-path line counts between two commits. Binary files report
// zero insertions and zero deletions.
func (r *Repo) NumStat(from, to string) (map[string]LineDelta, error) {
	out, err := r.Run("diff-tree", "-r", "--no-renames", "--numstat", from, to)
	if err != nil {
		return nil, err
	}

	stats := map[string]LineDelta{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		insertions, _ := strconv.Atoi(parts[0])
		deletions, _ := strconv.Atoi(parts[1])
		stats[parts[2]] = LineDelta{Insertions: insertions, Deletions: deletions}
	}

	return stats, nil
}

// CommitCount counts the commits reachable from to but not from.
func (r *Repo) CommitCount(from, to string) (int, error) {
	out, err := r.Run("rev-list", "--count", from+".."+to)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(strings.TrimSpace(out))
}

const logRecordSeparator = "\x1f"

// Commits lists the non-merge commits in a range, optionally restricted to a
// set of paths.
func (r *Repo) Commits(from, to string, paths []string) ([]Commit, error) {
	args := []string{
		"log", "--no-merges",
		"--format=%H" + logRecordSeparator + "%an" + logRecordSeparator + "%ad" + logRecordSeparator + "%s",
		"--date=short", from + ".." + to,
	}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	}

	out, err := r.Run(args...)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, logRecordSeparator, 4)
		if len(parts) != 4 {
			continue
		}
		commits = append(commits, Commit{SHA: parts[0], Author: parts[1], Date: parts[2], Subject: parts[3]})
	}

	return commits, nil
}

// MergeTreeConflicts predicts the textual conflicts of merging other into ours
// without touching the worktree. It requires git 2.38 or newer.
func (r *Repo) MergeTreeConflicts(ours, other string) ([]string, error) {
	out, code, err := r.RunAllowFail("merge-tree", "--write-tree", "-z", "--name-only",
		"--no-messages", ours, other)
	switch code {
	case 0:
		return nil, nil
	case 1:
		// Output is the tree identifier, then the conflicted paths, then an
		// empty field that terminates the list.
		fields := splitNUL(out)
		if len(fields) < 2 {
			return nil, nil
		}

		return fields[1:], nil
	default:
		return nil, fmt.Errorf("merge-tree failed: %w", err)
	}
}

// GrepFilesAt lists the files at a ref that match any of the extended regular
// expressions in terms, optionally restricted to pathspecs.
func (r *Repo) GrepFilesAt(ref string, terms, pathspecs []string) ([]string, error) {
	if len(terms) == 0 {
		return nil, nil
	}

	args := []string{"grep", "--files-with-matches", "--extended-regexp", "-z"}
	for _, term := range terms {
		args = append(args, "-e", term)
	}
	args = append(args, ref)
	if len(pathspecs) > 0 {
		args = append(args, "--")
		args = append(args, pathspecs...)
	}

	out, code, err := r.RunAllowFail(args...)
	if code == 1 {
		return nil, nil // git grep reports 1 when nothing matched
	}
	if err != nil {
		return nil, err
	}

	prefix := ref + ":"
	fields := splitNUL(out)
	matches := make([]string, 0, len(fields))
	for _, field := range fields {
		matches = append(matches, strings.TrimPrefix(field, prefix))
	}

	return matches, nil
}

func splitNUL(s string) []string {
	var fields []string
	for _, field := range strings.Split(s, "\x00") {
		if field != "" {
			fields = append(fields, field)
		}
	}

	return fields
}

// ShortSHA truncates a commit identifier to the twelve characters used in reports.
func ShortSHA(ref string) string {
	if len(ref) > 12 {
		return ref[:12]
	}

	return ref
}

// TreeBlobs maps every file in the tree of a ref to its blob identifier. It
// answers content questions without reading a single file: two paths hold the
// same bytes exactly when they carry the same blob.
func (r *Repo) TreeBlobs(ref string) (map[string]string, error) {
	out, err := r.Run("ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, err
	}

	blobs := map[string]string{}
	for _, entry := range splitNUL(out) {
		// <mode> SP <type> SP <object> TAB <path>
		metadata, path, found := strings.Cut(entry, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		blobs[path] = fields[2]
	}

	return blobs, nil
}

// TreePaths lists every file present in the tree of a ref.
func (r *Repo) TreePaths(ref string) (map[string]bool, error) {
	out, err := r.Run("ls-tree", "-r", "-z", "--name-only", ref)
	if err != nil {
		return nil, err
	}

	paths := map[string]bool{}
	for _, path := range splitNUL(out) {
		paths[path] = true
	}

	return paths, nil
}
