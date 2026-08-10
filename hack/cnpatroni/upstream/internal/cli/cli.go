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

// Package cli is the command-line surface of the CloudNativePatroni upstream
// tooling.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/baseline"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/boundary"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/gitx"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/report"
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/setup"
)

// Version is the tool version, reported in every machine-readable report.
const Version = "0.1.0"

// Exit codes. Codes 0 to 4 are shared with the report and the validator; 5 and
// 6 belong to the command line.
const (
	// ExitOK means nothing needs a human.
	ExitOK = 0
	// ExitNeedsReview means upstream changed a path this fork adapts,
	// disables or deletes.
	ExitNeedsReview = 1
	// ExitConflict means a textual conflict is predicted.
	ExitConflict = 2
	// ExitUndeclared means a path is not classified by the manifest.
	ExitUndeclared = 3
	// ExitManifestInvalid means the manifest or the baseline is malformed or
	// dishonest.
	ExitManifestInvalid = 4
	// ExitUsage means the command line was wrong.
	ExitUsage = 5
	// ExitEnvironment means the clone cannot answer the question: it is
	// shallow, or the upstream remote is missing. The message says what to run.
	ExitEnvironment = 6
)

// Default locations, relative to the repository root.
const (
	defaultManifestPath = "hack/cnpatroni/upstream/boundary.yaml"
	defaultBaselinePath = "hack/cnpatroni/upstream/upstream-baseline.yaml"
	defaultReportDir    = "docs/cnpatroni/upstream-integration"
	// defaultAttributesPath is the only place git honours a repository-wide
	// attribute for every path the manifest declares.
	defaultAttributesPath = ".gitattributes"
)

// mergeDriverCommand is the subcommand git itself runs. It is named here
// because its exit code has to survive --exit-zero.
const mergeDriverCommand = "merge-driver"

const ownershipRatchetCommand = "ownership-ratchet"

type globals struct {
	repo         string
	manifestPath string
	baselinePath string
	exitZero     bool
}

// Run executes one command line and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	var g globals

	fs := flag.NewFlagSet("cnpatroni-upstream", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&g.repo, "repo", "", "repository root (default: the repository containing the working directory)")
	fs.StringVar(&g.manifestPath, "manifest", "", "boundary manifest (default: "+defaultManifestPath+")")
	fs.StringVar(&g.baselinePath, "baseline", "", "upstream baseline (default: "+defaultBaselinePath+")")
	fs.BoolVar(&g.exitZero, "exit-zero", false, "always exit 0; findings are still printed")
	fs.Usage = func() { usage(stderr) }

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	rest := fs.Args()
	if len(rest) == 0 {
		usage(stderr)

		return ExitUsage
	}

	code := dispatch(rest[0], rest[1:], g, stdout, stderr)

	// --exit-zero is for informational runs of the report and the validator.
	// Letting it reach either boundary gate would suppress the one answer that
	// command must never give.
	if g.exitZero && code != ExitUsage &&
		rest[0] != mergeDriverCommand && rest[0] != ownershipRatchetCommand {
		return ExitOK
	}

	return code
}

func dispatch(command string, args []string, g globals, stdout, stderr io.Writer) int {
	switch command {
	case "setup":
		return runSetup(args, g, stdout, stderr)
	case "validate":
		return runValidate(args, g, stdout, stderr)
	case "report":
		return runReport(args, g, stdout, stderr)
	case "gitattributes":
		return runGitAttributes(args, g, stdout, stderr)
	case ownershipRatchetCommand:
		return runOwnershipRatchet(args, g, stdout, stderr)
	case mergeDriverCommand:
		return runMergeDriver(args, stderr)
	case "version":
		_, _ = fmt.Fprintf(stdout, "cnpatroni-upstream %s\n", Version)

		return ExitOK
	case "help", "-h", "--help":
		usage(stdout)

		return ExitOK
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n", command)
		usage(stderr)

		return ExitUsage
	}
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `cnpatroni-upstream keeps the CloudNativePatroni fork answerable about upstream
CloudNativePG. It never mutates the worktree and never pushes.

Usage:
  cnpatroni-upstream [global flags] <command> [command flags]

Global flags:
  --repo       repository root
  --manifest   boundary manifest (default `+defaultManifestPath+`)
  --baseline   upstream baseline (default `+defaultBaselinePath+`)
  --exit-zero  always exit 0; findings are still printed

Commands:
  setup      configure this clone: add the upstream remote, fetch tags, unshallow,
             register the boundary merge driver
  validate   check the boundary manifest against the worktree and the history
  report     compute the divergence between the recorded baseline and upstream
  gitattributes
             render `+defaultAttributesPath+` from the boundary manifest
  ownership-ratchet
             compare protected ownership rules with a base manifest
  merge-driver
             git's always-conflict merge driver for a boundary path; git runs it,
             you do not
  version    print the tool version

Exit codes:
  0 ok   1 needs review   2 conflict predicted   3 undeclared path
  4 manifest invalid      5 usage                6 clone not set up
`)
}

// openRepo resolves the repository and the two data files the commands need.
func (g globals) openRepo() (*gitx.Repo, error) {
	dir := g.repo
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = cwd
	}

	return gitx.Open(dir)
}

func (g globals) manifestFile(repo *gitx.Repo) string {
	if g.manifestPath != "" {
		return g.manifestPath
	}

	return filepath.Join(repo.Root, filepath.FromSlash(defaultManifestPath))
}

func (g globals) baselineFile(repo *gitx.Repo) string {
	if g.baselinePath != "" {
		return g.baselinePath
	}

	return filepath.Join(repo.Root, filepath.FromSlash(defaultBaselinePath))
}

// classify turns an error into the right exit code, so that a clone that is
// simply not set up is never confused with a broken manifest.
func classify(err error, stderr io.Writer) int {
	var envErr *gitx.EnvironmentError
	if errors.As(err, &envErr) {
		_, _ = fmt.Fprintf(stderr, "%s\n\n%s\n", envErr.Reason, envErr.Remedy)

		return ExitEnvironment
	}
	_, _ = fmt.Fprintf(stderr, "%v\n", err)

	return ExitManifestInvalid
}

func runSetup(args []string, g globals, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	url := fs.String("url", "", "upstream repository URL (default: the URL recorded in the baseline)")
	remote := fs.String("remote", "", "remote name (default: the name recorded in the baseline)")
	track := fs.String("track", "", "upstream branch to track (default: the branch recorded in the baseline)")
	dryRun := fs.Bool("dry-run", false, "print the git commands without running them")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	repo, err := g.openRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)

		return ExitUsage
	}

	opts := setup.Options{Repo: repo, URL: *url, Remote: *remote, Track: *track, DryRun: *dryRun, Out: stdout}

	// The baseline records which upstream this fork follows, so setup does not
	// need the URL on the command line.
	if b, loadErr := baseline.Load(g.baselineFile(repo)); loadErr == nil {
		if opts.URL == "" {
			opts.URL = b.Upstream.URL
		}
		if opts.Remote == "" {
			opts.Remote = b.Upstream.Remote
		}
		if opts.Track == "" {
			opts.Track = b.Upstream.Track
		}
	} else if opts.URL == "" {
		_, _ = fmt.Fprintf(stderr, "no upstream URL: %v\npass --url explicitly\n", loadErr)

		return ExitUsage
	}

	if _, err := setup.Run(opts); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)

		return ExitManifestInvalid
	}

	if *dryRun {
		_, _ = fmt.Fprint(stdout, "\nDry run: nothing was changed. Re-run without --dry-run to apply.\n")
	}

	return ExitOK
}

func runValidate(args []string, g globals, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strict := fs.Bool("strict", false,
		"treat undeclared high-availability files as an error even while the manifest is provisional")
	drift := fs.Bool("drift", false, "also report files this fork changed without declaring them")
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *format != "text" && *format != "json" {
		_, _ = fmt.Fprintf(stderr, "unknown format %q\n", *format)

		return ExitUsage
	}

	repo, err := g.openRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)

		return ExitUsage
	}

	manifestPath := g.manifestFile(repo)
	m, err := boundary.Load(manifestPath)
	if err != nil {
		return classify(err, stderr)
	}
	b, err := baseline.Load(g.baselineFile(repo))
	if err != nil {
		return classify(err, stderr)
	}

	findings, err := boundary.Validate(m, boundary.Options{
		Repo: repo, Baseline: b, Strict: *strict, CheckDrift: *drift,
	})
	if err != nil {
		return classify(err, stderr)
	}

	if *format == "json" {
		body, marshalErr := json.MarshalIndent(findings, "", "  ")
		if marshalErr != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", marshalErr)

			return ExitManifestInvalid
		}
		_, _ = fmt.Fprintf(stdout, "%s\n", body)
	} else {
		printFindings(stdout, manifestPath, findings)
	}

	return boundary.ExitCode(findings)
}

func printFindings(stdout io.Writer, manifestPath string, findings []boundary.Finding) {
	if len(findings) == 0 {
		_, _ = fmt.Fprintf(stdout, "%s: valid\n", manifestPath)

		return
	}
	for _, f := range findings {
		_, _ = fmt.Fprintf(stdout, "%s: %s\n", f.Severity, f)
	}
	if remedy := boundary.RemediationFor(manifestPath, findings); remedy != "" {
		_, _ = fmt.Fprintf(stdout, "\n%s", remedy)
	}
}

func runOwnershipRatchet(args []string, g globals, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(ownershipRatchetCommand, flag.ContinueOnError)
	fs.SetOutput(stderr)
	basePath := fs.String("base", "", "base boundary manifest")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}
	if *basePath == "" {
		_, _ = fmt.Fprintln(stderr, "ownership-ratchet requires --base")

		return ExitUsage
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "ownership-ratchet does not accept positional arguments: %s\n",
			strings.Join(fs.Args(), " "))

		return ExitUsage
	}

	repo, err := g.openRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot open the repository for ownership-ratchet: %v\n", err)

		return ExitUsage
	}

	base, err := boundary.Load(*basePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot load base boundary manifest %s: %v\n", *basePath, err)

		return ExitManifestInvalid
	}
	if !ratchetManifestIsValid("base", base, stderr) {
		return ExitManifestInvalid
	}
	currentPath := g.manifestFile(repo)
	current, err := boundary.Load(currentPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot load current boundary manifest %s: %v\n", currentPath, err)

		return ExitManifestInvalid
	}
	if !ratchetManifestIsValid("current", current, stderr) {
		return ExitManifestInvalid
	}

	result := boundary.Ratchet(base, current)
	for _, rename := range result.Renames {
		_, _ = fmt.Fprintf(stdout, "Ownership rule rename: %s -> %s; class and paths preserved.\n",
			rename.OldRule, rename.NewRule)
	}
	for _, allowance := range result.AppliedAllowances {
		_, _ = fmt.Fprintf(stdout, "Acknowledged ownership narrowing: rule %s path %q: %s\n",
			allowance.Rule, allowance.Path, allowance.Reason)
	}
	for _, allowance := range result.StaleAllowances {
		_, _ = fmt.Fprintf(stdout, "Stale ownership ratchet allowance: rule %s path %q: %s\n",
			allowance.Rule, allowance.Path, allowance.Reason)
	}
	for _, finding := range result.Findings {
		_, _ = fmt.Fprintf(stderr, "Ownership ratchet finding: %s\n", finding)
	}
	if len(result.Findings) > 0 {
		return ExitManifestInvalid
	}

	_, _ = fmt.Fprintf(stdout, "Compared %d protected rules: ownership boundary did not shrink.\n",
		result.ProtectedRules)

	return ExitOK
}

func ratchetManifestIsValid(name string, manifest *boundary.Manifest, stderr io.Writer) bool {
	findings, err := boundary.Validate(manifest, boundary.Options{})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot validate %s boundary manifest: %v\n", name, err)

		return false
	}
	if len(findings) == 0 {
		return true
	}
	for _, finding := range findings {
		_, _ = fmt.Fprintf(stderr, "%s boundary manifest invalid: %s\n", name, finding)
	}

	return false
}

func runReport(args []string, g globals, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	from := fs.String("from", "", "range start (default: the last integrated commit in the baseline)")
	to := fs.String("to", "", "range end (default: the tracked upstream branch)")
	fetch := fs.Bool("fetch", false, "fetch the upstream remote first")
	outDir := fs.String("out-dir", "",
		"directory for report.json and report.md (default: "+defaultReportDir+"/<date>-<sha12>)")
	toStdout := fs.Bool("stdout", false, "also print the markdown report")
	noConflictCheck := fs.Bool("no-conflict-check", false, "skip the merge conflict prediction")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	repo, err := g.openRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)

		return ExitUsage
	}

	manifestPath := g.manifestFile(repo)
	m, err := boundary.Load(manifestPath)
	if err != nil {
		return classify(err, stderr)
	}
	findings, err := boundary.Validate(m, boundary.Options{})
	if err != nil {
		return classify(err, stderr)
	}
	if boundary.ExitCode(findings) == ExitManifestInvalid {
		printFindings(stderr, manifestPath, findings)

		return ExitManifestInvalid
	}
	b, err := baseline.Load(g.baselineFile(repo))
	if err != nil {
		return classify(err, stderr)
	}

	if *fetch {
		if err := repo.RequireRemote(b.Upstream.Remote); err != nil {
			return classify(err, stderr)
		}
		if _, err := repo.Run("fetch", "--no-tags", b.Upstream.Remote); err != nil {
			_, _ = fmt.Fprintf(stderr, "%v\n", err)

			return ExitManifestInvalid
		}
	}

	r, err := report.Build(report.Options{
		Repo: repo, Manifest: m, Baseline: b,
		From: *from, To: *to,
		SkipConflictCheck: *noConflictCheck,
		ToolVersion:       Version,
		Now:               time.Now,
	})
	if err != nil {
		return classify(err, stderr)
	}

	dir := *outDir
	if dir == "" {
		dir = filepath.Join(repo.Root, filepath.FromSlash(defaultReportDir),
			fmt.Sprintf("%s-%s", r.Range.To.Date, gitx.ShortSHA(r.Range.To.Commit)))
	}
	if err := writeReport(dir, r); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)

		return ExitManifestInvalid
	}

	markdown := r.Markdown()
	if *toStdout {
		_, _ = fmt.Fprint(stdout, markdown)
	}
	_, _ = fmt.Fprintf(stderr, "%s\nReport written to %s\n", r.Gate.Verdict, dir)

	return r.Gate.ExitCode
}

func writeReport(dir string, r *report.Report) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}

	body, err := r.JSON()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), body, 0o600); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, "report.md"), []byte(r.Markdown()), 0o600)
}

// regenerateHint is printed whenever the checked-in attributes and the manifest
// disagree, because a stale file guards a boundary that has moved.
var regenerateHint = "Regenerate it with:\n  " +
	fmt.Sprintf(gitx.ToolInvocation, "gitattributes") + "\n"

const mergeDriverUsage = `cnpatroni-upstream merge-driver %O %A %B %L %P

Git runs this; you do not. It is registered by ` + "`cnpatroni-upstream setup`" + ` as
merge.` + boundary.MergeDriverName + `.driver, and it always reports the merge of a
boundary path as unresolved.
`

// runGitAttributes renders the .gitattributes the manifest implies. It is the
// only generated artefact of the boundary system, so it carries the generator's
// own header rather than being editable by hand.
func runGitAttributes(args []string, g globals, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("gitattributes", flag.ContinueOnError)
	fs.SetOutput(stderr)
	check := fs.Bool("check", false, "do not write; fail when the checked-in file is not what the manifest implies")
	toStdout := fs.Bool("stdout", false, "print the rendered file instead of writing it")
	out := fs.String("out", "", "file to write (default: "+defaultAttributesPath+" at the repository root)")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	repo, err := g.openRepo()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)

		return ExitUsage
	}

	manifestPath := g.manifestFile(repo)
	m, err := boundary.Load(manifestPath)
	if err != nil {
		return classify(err, stderr)
	}

	body := boundary.GitAttributes(m)
	if *toStdout {
		_, _ = fmt.Fprint(stdout, body)

		return ExitOK
	}

	path := *out
	if path == "" {
		path = filepath.Join(repo.Root, defaultAttributesPath)
	}

	if *check {
		return checkGitAttributes(path, manifestPath, body, stdout, stderr)
	}

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		_, _ = fmt.Fprintf(stderr, "%v\n", err)

		return ExitManifestInvalid
	}
	_, _ = fmt.Fprintf(stderr, "Wrote %s\n", path)

	return ExitOK
}

func checkGitAttributes(path, manifestPath, body string, stdout, stderr io.Writer) int {
	current, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied by design
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cannot read %s: %v\n\n%s", path, err, regenerateHint)

		return ExitManifestInvalid
	}
	if string(current) != body {
		_, _ = fmt.Fprintf(stderr, "%s is not what %s implies\n\n%s", path, manifestPath, regenerateHint)

		return ExitManifestInvalid
	}
	_, _ = fmt.Fprintf(stdout, "%s: up to date\n", path)

	return ExitOK
}

// runMergeDriver is git's merge driver for a boundary path. It reports a
// conflict whatever happens, so its exit code is never ExitOK.
func runMergeDriver(args []string, stderr io.Writer) int {
	// %P was added to git's placeholder set later than the other four, so it is
	// accepted but not required.
	if len(args) < 4 || len(args) > 5 {
		_, _ = fmt.Fprint(stderr, mergeDriverUsage)

		return ExitUsage
	}

	in := boundary.MergeInputs{Ancestor: args[0], Ours: args[1], Theirs: args[2]}
	// An unparsable %L falls back to git's own marker size inside the driver.
	in.MarkerSize, _ = strconv.Atoi(args[3])
	if len(args) == 5 {
		in.Path = args[4]
	}

	clean, err := boundary.MergeDriver(in)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", boundary.MergeDriverName, err)

		return ExitConflict
	}
	_, _ = fmt.Fprint(stderr, mergeDriverMessage(in.Path, clean))

	return ExitConflict
}

func mergeDriverMessage(path string, clean bool) string {
	if path == "" {
		path = "this boundary path"
	}

	reason := "git's own three-way merge left conflict markers in the file."
	if clean {
		reason = "git's own three-way merge resolved this file without a conflict, " +
			"which is the silent absorption docs/cnpatroni/fork-maintenance.md records. " +
			"The merged text is in the worktree; nothing was lost."
	}

	return fmt.Sprintf("%s: %s is a boundary path, so this merge is not resolved automatically.\n"+
		"%s\n"+
		"Read the upstream hunks before resolving:\n"+
		"  git diff --merge-base HEAD MERGE_HEAD -- %s\n"+
		"Then resolve the file and run `git add %s`.\n",
		boundary.MergeDriverName, path, reason, path, path)
}
