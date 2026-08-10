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
// Every step is additive: it configures a remote, fetches objects and refs, and
// backfills a truncated history. Nothing here rewrites a commit, moves a
// branch, pushes, or touches the worktree, so it is safe to run repeatedly and
// safe to run on a clone with uncommitted work in it.
package setup

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
)

// Options configures the setup run.
type Options struct {
	Repo *gitx.Repo
	// URL of the upstream repository.
	URL string
	// Remote is the name to configure it under.
	Remote string
	// Track is the upstream branch this fork follows.
	Track string
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
