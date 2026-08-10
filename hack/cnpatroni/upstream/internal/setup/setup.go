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

// Package setup makes a fresh clone of the CloudNativePatroni fork able to
// answer questions about upstream CloudNativePG.
//
// Every step is additive: it configures a remote, fetches objects and refs,
// backfills a truncated history, and registers the boundary merge driver that
// the generated .gitattributes names. Nothing here rewrites a commit, moves a
// branch, pushes, or touches the worktree, so it is safe to run repeatedly and
// safe to run on a clone with uncommitted work in it.
package setup

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/postgres-ai/cnpatroni-upstream/internal/boundary"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
)

// driverBinaryName is the file the merge driver is installed as, inside the
// repository's git directory.
const driverBinaryName = "cnpatroni-upstream"

// driverDescription is what `git config merge.<driver>.name` reports, which is
// the text git prints when it cannot find the driver.
const driverDescription = "CloudNativePatroni boundary guard"

// Options configures the setup run.
type Options struct {
	Repo *gitx.Repo
	// URL of the upstream repository.
	URL string
	// Remote is the name to configure it under.
	Remote string
	// Track is the upstream branch this fork follows.
	Track string
	// DriverBinary is the executable registered as the boundary merge driver.
	// It defaults to the running executable, which setup installs into the
	// repository's git directory: a `go run` binary is deleted as soon as the
	// command exits, so registering its path directly would leave git pointing
	// at nothing.
	DriverBinary string
	// DryRun prints the steps without running them.
	DryRun bool
	// Out receives the human-readable transcript. Defaults to os.Stdout.
	Out io.Writer
}

// Step is one git invocation, and whether it was needed.
type Step struct {
	Name    string
	Command []string
	Skipped bool
	Reason  string
}

// Result is the transcript of a setup run.
type Result struct {
	Steps []Step
}

// Run configures the clone. It is idempotent: a second run reports every step
// as skipped except the fetches, which are cheap when there is nothing new.
func Run(opts Options) (*Result, error) {
	if opts.Repo == nil {
		return nil, fmt.Errorf("a repository is required")
	}
	if opts.URL == "" {
		return nil, fmt.Errorf("an upstream URL is required")
	}
	if opts.Remote == "" {
		opts.Remote = "upstream"
	}
	if opts.Track == "" {
		opts.Track = "main"
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}

	result := &Result{}
	runner := &runner{opts: opts, result: result}

	if err := runner.configureRemote(); err != nil {
		return nil, err
	}
	if err := runner.configureRefspecs(); err != nil {
		return nil, err
	}
	if err := runner.fetch(); err != nil {
		return nil, err
	}
	if err := runner.configureRerere(); err != nil {
		return nil, err
	}
	if err := runner.configureMergeDriver(); err != nil {
		return nil, err
	}

	return result, nil
}

type runner struct {
	opts   Options
	result *Result
}

// configureRemote adds the upstream remote, and refuses to repoint an existing
// remote of that name at a different URL: silently changing what `upstream`
// means would invalidate every recorded baseline.
func (r *runner) configureRemote() error {
	name := r.opts.Remote

	existing, _, err := r.opts.Repo.RunAllowFail("remote", "get-url", name)
	if err == nil {
		current := strings.TrimSpace(existing)
		if current != r.opts.URL {
			return fmt.Errorf(
				"remote %q already points at %q, not %q; "+
					"repoint it deliberately with `git remote set-url %s %s` if that is intended",
				name, current, r.opts.URL, name, r.opts.URL)
		}
		r.skip("add the upstream remote", []string{"remote", "add", "--no-tags", name, r.opts.URL},
			"already configured with this URL")

		return nil
	}

	return r.step("add the upstream remote",
		[]string{"remote", "add", "--no-tags", name, r.opts.URL})
}

// configureRefspecs narrows the remote to the branches this fork tracks. A
// plain `git remote add` would mirror every upstream development branch.
func (r *runner) configureRefspecs() error {
	name := r.opts.Remote
	key := "remote." + name + ".fetch"
	want := []string{
		fmt.Sprintf("+refs/heads/%s:refs/remotes/%s/%s", r.opts.Track, name, r.opts.Track),
		fmt.Sprintf("+refs/heads/release-*:refs/remotes/%s/release-*", name),
	}

	current, _, _ := r.opts.Repo.RunAllowFail("config", "--get-all", key)
	if equalLines(current, want) {
		r.skip("record the fetch refspecs", []string{"config", "--get-all", key},
			"already narrowed to the tracked branches")
	} else {
		if err := r.step("record the fetch refspec for the tracked branch",
			[]string{"config", "--replace-all", key, want[0]}); err != nil {
			return err
		}
		if err := r.step("record the fetch refspec for the release branches",
			[]string{"config", "--add", key, want[1]}); err != nil {
			return err
		}
	}

	tagOpt, _, _ := r.opts.Repo.RunAllowFail("config", "--get", "remote."+name+".tagOpt")
	if strings.TrimSpace(tagOpt) == "--no-tags" {
		r.skip("disable automatic tag following",
			[]string{"config", "remote." + name + ".tagOpt", "--no-tags"}, "already set")

		return nil
	}

	return r.step("disable automatic tag following",
		[]string{"config", "remote." + name + ".tagOpt", "--no-tags"})
}

// fetch backfills the history. On a shallow clone `--unshallow` is the step that
// removes .git/shallow; it only ever adds objects.
func (r *runner) fetch() error {
	name := r.opts.Remote

	shallow, err := r.opts.Repo.IsShallow()
	if err != nil {
		return err
	}

	fetchArgs := []string{"fetch", "--no-tags", name}
	fetchName := "fetch the tracked upstream branches"
	if shallow {
		fetchArgs = []string{"fetch", "--unshallow", "--no-tags", name}
		fetchName = "backfill the truncated history and fetch the tracked branches"
	}
	if err := r.step(fetchName, fetchArgs); err != nil {
		return err
	}

	return r.step("fetch the upstream version tags",
		[]string{"fetch", name, "refs/tags/v*:refs/tags/v*"})
}

// configureRerere makes git remember how a boundary hunk was resolved, so the
// next integration does not ask the same question twice. autoUpdate stays off:
// a remembered resolution must still be looked at.
func (r *runner) configureRerere() error {
	settings := []struct {
		key, value string
	}{
		{"rerere.enabled", "true"},
		{"rerere.autoUpdate", "false"},
	}

	for _, s := range settings {
		current, _, _ := r.opts.Repo.RunAllowFail("config", "--get", s.key)
		if strings.TrimSpace(current) == s.value {
			r.skip("remember conflict resolutions ("+s.key+")",
				[]string{"config", s.key, s.value}, "already set")

			continue
		}
		if err := r.step("remember conflict resolutions ("+s.key+")",
			[]string{"config", s.key, s.value}); err != nil {
			return err
		}
	}

	return nil
}

// configureMergeDriver registers the always-conflict merge driver that the
// generated .gitattributes names. Git refuses to take a driver definition from
// a tracked file, so this registration is the whole difference between a guard
// and an inert attribute: without it git falls back to its ordinary three-way
// merge and absorbs an upstream change to a boundary file silently, which is
// the failure docs/cnpatroni/fork-maintenance.md records.
func (r *runner) configureMergeDriver() error {
	source, err := r.driverSource()
	if err != nil {
		return err
	}

	commonDir, err := r.opts.Repo.CommonDir()
	if err != nil {
		return err
	}

	// The git directory, not the worktree: an executable under bin/ would show
	// up as an untracked file in every merge this guard is supposed to police.
	destination := filepath.Join(commonDir, "cnpatroni", driverBinaryName)
	quotedDestination, err := shellQuote(destination)
	if err != nil {
		return err
	}
	if err := r.installDriver(source, destination); err != nil {
		return err
	}

	settings := []struct {
		key, value string
	}{
		{"merge." + boundary.MergeDriverName + ".name", driverDescription},
		{"merge." + boundary.MergeDriverName + ".driver", quotedDestination + " merge-driver '%O' '%A' '%B' '%L' '%P'"},
	}

	for _, s := range settings {
		name := "register the boundary merge driver (" + s.key + ")"
		current, _, _ := r.opts.Repo.RunAllowFail("config", "--get", s.key)
		if strings.TrimSpace(current) == s.value {
			r.skip(name, []string{"config", s.key, s.value}, "already set")

			continue
		}
		if err := r.step(name, []string{"config", s.key, s.value}); err != nil {
			return err
		}
	}

	return nil
}

func shellQuote(value string) (string, error) {
	if strings.ContainsRune(value, '\'') || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", fmt.Errorf(
			"cannot register the boundary merge driver at %s: the path contains a character that cannot be quoted for the shell git runs the driver with; move the clone to a path without quotes or control characters",
			strconv.Quote(value))
	}

	return "'" + value + "'", nil
}

// driverSource resolves the executable to register, and refuses a path that is
// not there: a registration pointing at a missing binary is worse than none,
// because git would then fail every boundary merge for the wrong reason.
func (r *runner) driverSource() (string, error) {
	path := r.opts.DriverBinary
	if path == "" {
		self, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("cannot locate this executable to register it as the merge driver: %w", err)
		}
		path = self
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the merge driver binary %q: %w", path, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("cannot register the boundary merge driver: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("the merge driver binary %q is a directory", absolute)
	}

	return absolute, nil
}

func (r *runner) installDriver(source, destination string) error {
	same, err := sameContent(source, destination)
	if err != nil {
		return err
	}
	command := []string{"install", source, destination}
	if same {
		r.skip("install the boundary merge driver", command, "already installed")

		return nil
	}

	return r.localStep("install the boundary merge driver", command, func() error {
		return installExecutable(source, destination)
	})
}

// localStep records a step that configures the clone without invoking git.
func (r *runner) localStep(name string, command []string, apply func() error) error {
	step := Step{Name: name, Command: command}

	if r.opts.DryRun {
		step.Skipped = true
		step.Reason = "dry run"
		r.record(step)

		return nil
	}

	if err := apply(); err != nil {
		r.record(step)

		return fmt.Errorf("%s: %w", name, err)
	}
	r.record(step)

	return nil
}

// installExecutable copies the binary through a temporary file in the target
// directory, so that a merge running while setup runs never sees a truncated
// driver.
func installExecutable(source, destination string) error {
	body, err := os.ReadFile(source) //nolint:gosec // the operator names the binary to install
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return err
	}

	temporary := destination + ".new"
	if err := os.WriteFile(temporary, body, 0o700); err != nil { //nolint:gosec // it has to be executable
		return err
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)

		return err
	}

	return nil
}

// sameContent reports whether the driver is already installed unchanged. A
// missing destination is not an error: it is the first run.
func sameContent(source, destination string) (bool, error) {
	installed, err := os.ReadFile(destination) //nolint:gosec // a path this package computed
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}

		return false, err
	}
	current, err := os.ReadFile(source) //nolint:gosec // the operator names the binary to install
	if err != nil {
		return false, err
	}

	return bytes.Equal(installed, current), nil
}

func (r *runner) step(name string, args []string) error {
	step := Step{Name: name, Command: append([]string{"git"}, args...)}

	if r.opts.DryRun {
		step.Skipped = true
		step.Reason = "dry run"
		r.record(step)

		return nil
	}

	if _, err := r.opts.Repo.Run(args...); err != nil {
		r.record(step)

		return fmt.Errorf("%s: %w", name, err)
	}
	r.record(step)

	return nil
}

func (r *runner) skip(name string, args []string, reason string) {
	r.record(Step{
		Name:    name,
		Command: append([]string{"git"}, args...),
		Skipped: true,
		Reason:  reason,
	})
}

func (r *runner) record(step Step) {
	r.result.Steps = append(r.result.Steps, step)

	marker := "run "
	if step.Skipped {
		marker = "skip"
	}
	suffix := ""
	if step.Reason != "" {
		suffix = "   # " + step.Reason
	}
	_, _ = fmt.Fprintf(r.opts.Out, "%s  %s%s\n", marker, strings.Join(step.Command, " "), suffix)
}

func equalLines(actual string, want []string) bool {
	got := strings.Fields(strings.TrimSpace(actual))
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}

	return true
}
