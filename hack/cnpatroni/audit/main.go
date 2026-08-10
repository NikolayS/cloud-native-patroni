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

// Command audit inventories every reachable code path that can influence
// PostgreSQL role or process lifecycle in this repository, cross-checks it
// against a curated classification, and fails when an unclassified path appears
// (specification sections 9.1 and 9.3).
//
// The same binary renders the ignored local audit document and responsibility
// map. The authority-audit workflow runs that renderer on every invocation and
// uploads both outputs as one artifact. Producing the documents and the gate
// from one program is deliberate: an audit written once by hand and a checker
// written separately disagree within two upstream merges, and then both are
// ignored.
//
//	audit scan       print the inventory
//	audit check      fail on unclassified hits, baseline growth or stale entries
//	audit baseline   record the ratchet, or compare it against another copy
//	audit doc        render the audit document and the responsibility map
//	audit stub       print classification stubs for unclassified hits
//
// Exit codes: 0 clean, 1 policy violation, 2 tool or configuration error,
// 3 hygiene. Separating 1 from 2 matters, because a tree that does not compile
// must never be reported as policy compliance.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	exitClean     = 0
	exitViolation = 1
	exitToolError = 2
	exitHygiene   = 3
)

type options struct {
	root           string
	rules          string
	classification string
	baseline       string
	narrative      string
	auditOut       string
	mapOut         string
	format         string
	milestone      string
	write          bool
	compare        string
	warnHygiene    bool
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: audit <scan|check|baseline|doc|stub> [flags]")
		return exitToolError
	}

	command, rest := args[0], args[1:]
	opts, err := parseFlags(command, rest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return exitToolError
	}

	switch command {
	case "scan":
		return runScan(opts)
	case "check":
		return runCheck(opts)
	case "baseline":
		return runBaseline(opts)
	case "doc":
		return runDoc(opts)
	case "stub":
		return runStub(opts)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", command)
		return exitToolError
	}
}

func parseFlags(command string, args []string) (*options, error) {
	opts := &options{}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.StringVar(&opts.root, "root", "../../..", "repository root")
	fs.StringVar(&opts.rules, "rules", "policy/authority-rules.yaml", "rule set")
	fs.StringVar(&opts.classification, "classification", "policy/authority-classification.yaml",
		"curated classification")
	fs.StringVar(&opts.baseline, "baseline", "policy/authority-baseline.yaml", "recorded baseline")
	fs.StringVar(&opts.narrative, "narrative", "policy/authority-audit-narrative.md",
		"prose fragment embedded in the generated audit document")
	fs.StringVar(&opts.auditOut, "audit-out", "docs/cnpatroni/authority-audit.md",
		"ignored generated audit document, relative to the repository root")
	fs.StringVar(&opts.mapOut, "map-out", "docs/cnpatroni/responsibility-map.md",
		"ignored generated responsibility map, relative to the repository root")
	fs.StringVar(&opts.format, "format", "text", "text, github or json")
	fs.StringVar(&opts.milestone, "milestone", "M0", "current milestone, for allowlist expiry")
	fs.BoolVar(&opts.write, "write", false, "baseline: rewrite the recorded baseline")
	fs.StringVar(&opts.compare, "compare", "", "baseline: compare against this recorded baseline")
	fs.BoolVar(&opts.warnHygiene, "warn-only-hygiene", false, "report hygiene without failing")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return opts, nil
}

// load reads everything a subcommand needs, so that a configuration mistake is
// reported once, as a tool error, before any scanning happens.
func (o *options) load() (*RuleSet, *ScanResult, error) {
	rules, err := LoadRules(o.rules)
	if err != nil {
		return nil, nil, err
	}
	res, err := ScanRepo(o.root, rules)
	if err != nil {
		return nil, nil, err
	}
	return rules, res, nil
}

func runScan(o *options) int {
	_, res, err := o.load()
	if err != nil {
		return toolError(err)
	}
	return printFindings(o.format, res)
}

func printFindings(format string, res *ScanResult) int {
	switch format {
	case "json":
		out, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return toolError(err)
		}
		fmt.Println(string(out))
	case "github":
		for _, f := range res.Findings {
			fmt.Println(f.GitHub())
		}
	case "text":
		for _, f := range res.Findings {
			fmt.Println(f.Text())
		}
		fmt.Printf("\n%d findings in %d packages, %d files\n", len(res.Findings), res.Packages, res.Files)
	default:
		return toolError(fmt.Errorf("unknown format %q, want text, github or json", format))
	}
	return exitClean
}

func runCheck(o *options) int {
	rules, res, err := o.load()
	if err != nil {
		return toolError(err)
	}
	cls, err := LoadClassification(o.classification)
	if err != nil {
		return toolError(err)
	}
	baseline, err := LoadBaseline(o.baseline)
	if err != nil {
		return toolError(err)
	}

	report := Check(rules, res, cls, baseline, o.milestone)

	for _, v := range report.Violations {
		fmt.Fprintln(os.Stderr, v)
	}
	for _, h := range report.Hygiene {
		fmt.Fprintln(os.Stderr, h)
	}
	fmt.Printf("authority audit: %d findings, %d classified symbols, %d violations, %d hygiene notes\n",
		len(res.Findings), len(cls.Entries), len(report.Violations), len(report.Hygiene))

	switch {
	case len(report.Violations) > 0:
		return exitViolation
	case len(report.Hygiene) > 0 && !o.warnHygiene:
		return exitHygiene
	default:
		return exitClean
	}
}

func runBaseline(o *options) int {
	if o.compare != "" {
		return runBaselineCompare(o)
	}

	rules, res, err := o.load()
	if err != nil {
		return toolError(err)
	}
	cls, err := LoadClassification(o.classification)
	if err != nil {
		return toolError(err)
	}

	allowedTotal := 0
	for _, finding := range res.Findings {
		if finding.Severity == SeverityForbidden && cls.allows(finding) {
			allowedTotal++
		}
	}
	baseline := BuildBaseline(rules, res, FilterAllowed(res.Findings, cls), allowedTotal,
		headCommit(o.root), nowUTC())
	if !o.write {
		fmt.Printf("authority baseline: %d forbidden hits in %d buckets\n",
			baseline.Total, len(baseline.Buckets))
		return exitClean
	}
	if err := baseline.Write(o.baseline); err != nil {
		return toolError(err)
	}
	fmt.Printf("wrote %s: %d forbidden hits in %d buckets\n",
		o.baseline, baseline.Total, len(baseline.Buckets))
	return exitClean
}

func runBaselineCompare(o *options) int {
	base, err := LoadBaseline(o.compare)
	if err != nil {
		return toolError(err)
	}
	head, err := LoadBaseline(o.baseline)
	if err != nil {
		return toolError(err)
	}

	grew, lines := CompareBaselines(base, head)
	for _, line := range lines {
		fmt.Println(line)
	}
	appendStepSummary(lines[0])
	if grew {
		fmt.Fprintln(os.Stderr,
			"the authority baseline grew; a change that adds forbidden calls needs an explicit"+
				" human decision, not a regenerated baseline. Input weakening has no in-band approval;"+
				" narrow the audit only by changing the audit tool itself, and review the rules diff")
		return exitViolation
	}
	return exitClean
}

func runStub(o *options) int {
	_, res, err := o.load()
	if err != nil {
		return toolError(err)
	}
	cls, err := LoadClassification(o.classification)
	if err != nil {
		return toolError(err)
	}

	entries := cls.bySymbol()
	printed := map[string]bool{}
	for _, f := range res.Findings {
		if f.Severity == SeverityObserve || entries[f.Symbol] != nil || cls.allows(f) || printed[f.Symbol] {
			continue
		}
		printed[f.Symbol] = true
		fmt.Print(stubFor(f))
	}
	return exitClean
}

func toolError(err error) int {
	fmt.Fprintf(os.Stderr, "audit: %v\n", err)
	fmt.Fprintln(os.Stderr, "this is a tool or configuration error, not a policy failure")
	return exitToolError
}

func nowUTC() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
}

// headCommit records which tree the baseline was taken from. A repository
// without git still produces a usable baseline; the field simply says so.
func headCommit(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func appendStepSummary(line string) {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s\n", line)
}
