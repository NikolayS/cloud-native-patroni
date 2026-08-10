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

package boundary

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/postgres-ai/cnpatroni-upstream/internal/baseline"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
)

// Severity grades a validation finding.
type Severity int

// The severities, in increasing order of consequence.
const (
	// SeverityWarning is printed but does not fail the command.
	SeverityWarning Severity = iota
	// SeverityUndeclared marks a path the manifest does not classify. It maps
	// to exit code 3.
	SeverityUndeclared
	// SeverityError marks a malformed or dishonest manifest. It maps to exit
	// code 4.
	SeverityError
)

// String implements fmt.Stringer.
func (s Severity) String() string {
	switch s {
	case SeverityWarning:
		return "warning"
	case SeverityUndeclared:
		return "undeclared"
	case SeverityError:
		return "error"
	default:
		return "unknown"
	}
}

// Finding is one validation violation.
type Finding struct {
	Code     string   `json:"code"`
	RuleID   string   `json:"rule_id,omitempty"`
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message"`
	Severity Severity `json:"-"`
}

// String renders a finding as one line, in the form the command prints.
func (f Finding) String() string {
	parts := []string{f.Code}
	if f.RuleID != "" {
		parts = append(parts, f.RuleID)
	}
	if f.Path != "" {
		parts = append(parts, f.Path)
	}

	return strings.Join(parts, ": ") + ": " + f.Message
}

// Options controls which checks Validate runs.
type Options struct {
	// Repo enables every check that needs the worktree or git history. When it
	// is nil only the structural checks run.
	Repo *gitx.Repo
	// Baseline enables the checks that need the recorded fork base.
	Baseline *baseline.Baseline
	// Strict promotes undeclared HA-vocabulary files to a hard finding even
	// while the manifest declares itself provisional.
	Strict bool
	// CheckDrift additionally reports files this fork has edited since the fork
	// base without declaring them.
	CheckDrift bool
}

// ExitCode maps a set of findings onto the tool's exit-code lattice.
func ExitCode(findings []Finding) int {
	code := 0
	for _, f := range findings {
		switch f.Severity {
		case SeverityError:
			return 4
		case SeverityUndeclared:
			code = 3
		case SeverityWarning:
		}
	}

	return code
}

// Validate checks a manifest against its own schema, against the worktree and
// against git history.
//
// It returns an error only when the repository cannot answer the question at
// all, for example because it is a shallow clone. Everything the manifest gets
// wrong is returned as a finding, so that one run reports every problem.
func Validate(m *Manifest, opts Options) ([]Finding, error) {
	var findings []Finding

	if opts.Repo != nil && opts.Baseline != nil {
		if err := opts.Repo.RequireCompleteHistory(); err != nil {
			return nil, err
		}
	}

	findings = append(findings, validateStructure(m)...)

	if opts.Repo != nil {
		worktreeFindings, err := validateAgainstWorktree(m, opts)
		if err != nil {
			return nil, err
		}
		findings = append(findings, worktreeFindings...)
	}

	if opts.Repo != nil && opts.Baseline != nil {
		findings = append(findings, validateBaseline(opts)...)
	}

	if opts.CheckDrift {
		if opts.Repo == nil || opts.Baseline == nil {
			return nil, fmt.Errorf("the drift check needs both a repository and a baseline")
		}
		driftFindings, err := checkDrift(m, opts)
		if err != nil {
			return nil, err
		}
		findings = append(findings, driftFindings...)
	}

	return findings, nil
}

// validateStructure runs the checks that need nothing but the manifest itself.
func validateStructure(m *Manifest) []Finding {
	var findings []Finding

	if m.Schema != Schema {
		findings = append(findings, Finding{
			Code:     "V1",
			Message:  fmt.Sprintf("schema is %q, want %q", m.Schema, Schema),
			Severity: SeverityError,
		})
	}

	switch m.EffectiveClassificationState() {
	case ClassificationApplied, ClassificationTarget:
	default:
		findings = append(findings, Finding{
			Code: "V13",
			Message: fmt.Sprintf("classification_state is %q, want %q or %q",
				m.ClassificationState, ClassificationApplied, ClassificationTarget),
			Severity: SeverityError,
		})
	}

	if len(m.Rules) == 0 {
		return append(findings, Finding{
			Code:     "V2",
			Message:  "the manifest declares no rules",
			Severity: SeverityError,
		})
	}

	last := m.Rules[len(m.Rules)-1]
	if !last.IsCatchAll() || last.Ownership != OwnershipUpstreamUntouched {
		findings = append(findings, Finding{
			Code:   "V2",
			RuleID: last.ID,
			Message: `the last rule must be the catch-all ["**"] with ownership upstream-untouched, ` +
				"so that every path is classified",
			Severity: SeverityError,
		})
	}
	for i := range m.Rules[:len(m.Rules)-1] {
		if m.Rules[i].IsCatchAll() {
			findings = append(findings, Finding{
				Code:     "V2",
				RuleID:   m.Rules[i].ID,
				Message:  "the catch-all rule must be declared last",
				Severity: SeverityError,
			})
		}
	}

	seenID := map[string]bool{}
	seenLiteral := map[string]string{}
	for i := range m.Rules {
		rule := &m.Rules[i]

		if rule.ID == "" {
			findings = append(findings, Finding{
				Code:     "V0",
				Message:  fmt.Sprintf("rule %d has no id", i),
				Severity: SeverityError,
			})
		} else if seenID[rule.ID] {
			findings = append(findings, Finding{
				Code:     "V0",
				RuleID:   rule.ID,
				Message:  "duplicate rule id",
				Severity: SeverityError,
			})
		}
		seenID[rule.ID] = true

		findings = append(findings, validateRuleFields(rule)...)

		for _, pattern := range rule.Paths {
			g, err := CompileGlob(pattern)
			if err != nil {
				findings = append(findings, Finding{
					Code:     "V4",
					RuleID:   rule.ID,
					Path:     pattern,
					Message:  err.Error(),
					Severity: SeverityError,
				})

				continue
			}
			if !g.IsLiteral() {
				continue
			}
			if owner, ok := seenLiteral[pattern]; ok {
				findings = append(findings, Finding{
					Code:     "V3",
					RuleID:   rule.ID,
					Path:     pattern,
					Message:  fmt.Sprintf("also declared by rule %q", owner),
					Severity: SeverityError,
				})
			}
			seenLiteral[pattern] = rule.ID
		}
	}

	return findings
}

func validateRuleFields(rule *Rule) []Finding {
	var findings []Finding

	if !rule.Ownership.Valid() {
		findings = append(findings, Finding{
			Code:     "V0",
			RuleID:   rule.ID,
			Message:  fmt.Sprintf("unknown ownership class %q", rule.Ownership),
			Severity: SeverityError,
		})
	}
	if len(rule.Paths) == 0 {
		findings = append(findings, Finding{
			Code:     "V0",
			RuleID:   rule.ID,
			Message:  "declares no paths",
			Severity: SeverityError,
		})
	}
	if rule.Ownership == OwnershipDisabled && rule.Mechanism == "" {
		findings = append(findings, Finding{
			Code:     "V0",
			RuleID:   rule.ID,
			Message:  "a disabled rule must state how the path was severed",
			Severity: SeverityError,
		})
	}
	if rule.Mechanism == MechanismMovedAside && rule.MovedTo == "" {
		findings = append(findings, Finding{
			Code:     "V0",
			RuleID:   rule.ID,
			Message:  "a moved-aside rule must state moved_to",
			Severity: SeverityError,
		})
	}
	if rule.EffectiveState() == StatePlanned && rule.Ownership != OwnershipCNPatroniOwned {
		findings = append(findings, Finding{
			Code:     "V10",
			RuleID:   rule.ID,
			Message:  "state: planned is only legal on a cnpatroni-owned rule",
			Severity: SeverityError,
		})
	}

	return findings
}

// validateAgainstWorktree runs the checks that compare the manifest with the
// files actually present in the fork.
func validateAgainstWorktree(m *Manifest, opts Options) ([]Finding, error) {
	var findings []Finding

	for i := range m.Rules {
		rule := &m.Rules[i]
		if rule.IsCatchAll() || rule.EffectiveState() == StatePlanned {
			continue
		}

		for _, g := range rule.Globs() {
			ruleFindings, err := validateRulePath(rule, g, opts)
			if err != nil {
				return nil, err
			}
			findings = append(findings, ruleFindings...)
		}
	}

	findings = append(findings, validateRequiredPaths(m)...)

	auditFindings, err := auditTermScan(m, opts)
	if err != nil {
		return nil, err
	}

	return append(findings, auditFindings...), nil
}

func validateRulePath(rule *Rule, g *Glob, opts Options) ([]Finding, error) {
	// Only a literal pattern names a path whose existence can be asserted; a
	// glob that matches nothing is reported by the divergence report instead.
	if !g.IsLiteral() {
		return nil, nil
	}

	pattern := g.Pattern()
	present := existsInWorktree(opts.Repo.Root, pattern)

	switch {
	case rule.Ownership == OwnershipDeleted:
		return validateDeletedPath(rule, pattern, present, opts)

	case rule.Ownership == OwnershipDisabled && rule.Mechanism == MechanismMovedAside:
		if present {
			return []Finding{{
				Code:     "V6",
				RuleID:   rule.ID,
				Path:     pattern,
				Message:  fmt.Sprintf("declared moved-aside to %q but still present", rule.MovedTo),
				Severity: SeverityError,
			}}, nil
		}
		destination := rule.MovedTo
		if strings.HasSuffix(destination, "/") {
			destination += filepath.Base(pattern)
		}
		if !existsInWorktree(opts.Repo.Root, destination) {
			return []Finding{{
				Code:     "V6",
				RuleID:   rule.ID,
				Path:     pattern,
				Message:  fmt.Sprintf("declared moved-aside but %q does not exist", destination),
				Severity: SeverityError,
			}}, nil
		}

		return nil, nil

	case !present:
		return []Finding{{
			Code:     "V5",
			RuleID:   rule.ID,
			Path:     pattern,
			Message:  fmt.Sprintf("declared %s but absent from the worktree", rule.Ownership),
			Severity: SeverityError,
		}}, nil

	default:
		return nil, nil
	}
}

func validateDeletedPath(rule *Rule, pattern string, present bool, opts Options) ([]Finding, error) {
	if present {
		return []Finding{{
			Code:     "V7",
			RuleID:   rule.ID,
			Path:     pattern,
			Message:  "declared deleted but still present in the worktree",
			Severity: SeverityError,
		}}, nil
	}
	if opts.Baseline == nil {
		return nil, nil
	}

	existed, err := opts.Repo.PathExistsAt(opts.Baseline.ForkBase.Commit, pattern)
	if err != nil {
		return nil, err
	}
	if !existed {
		return []Finding{{
			Code:     "V7",
			RuleID:   rule.ID,
			Path:     pattern,
			Message:  "declared deleted but never existed at the fork base",
			Severity: SeverityError,
		}}, nil
	}

	return nil, nil
}

func validateRequiredPaths(m *Manifest) []Finding {
	var findings []Finding

	for _, path := range m.RequiredPaths {
		rule := m.Match(path)
		if rule == nil || rule.IsCatchAll() {
			findings = append(findings, Finding{
				Code:     "V8",
				Path:     path,
				Message:  "listed in required_paths but classified only by the catch-all rule",
				Severity: SeverityError,
			})
		}
	}

	return findings
}

// auditTermScan reports files carrying high-availability vocabulary that the
// manifest classifies as upstream-untouched without recording that a human read
// them. This is the check that stops the fork from quietly absorbing an
// upstream change to code that decides which instance is the primary.
func auditTermScan(m *Manifest, opts Options) ([]Finding, error) {
	if len(m.AuditTerms) == 0 {
		return nil, nil
	}

	matches, err := opts.Repo.GrepFilesAt("HEAD", m.AuditTerms, nil)
	if err != nil {
		return nil, err
	}

	severity := SeverityUndeclared
	if m.Provisional && !opts.Strict {
		severity = SeverityWarning
	}

	findings := make([]Finding, 0, len(matches))
	for _, path := range matches {
		if !inAuditScan(m, path) {
			continue
		}
		rule := m.Match(path)
		if rule != nil && rule.Ownership != OwnershipUpstreamUntouched {
			continue
		}
		if rule != nil && rule.AuditReviewed {
			continue
		}

		ruleID := ""
		if rule != nil {
			ruleID = rule.ID
		}
		findings = append(findings, Finding{
			Code:   "V9",
			RuleID: ruleID,
			Path:   path,
			Message: "carries high-availability vocabulary but is classified upstream-untouched " +
				"without audit_reviewed: true",
			Severity: severity,
		})
	}

	return findings, nil
}

func inAuditScan(m *Manifest, path string) bool {
	included := len(m.AuditScan.Include) == 0
	for _, pattern := range m.AuditScan.Include {
		if g, err := CompileGlob(pattern); err == nil && g.Match(path) {
			included = true

			break
		}
	}
	if !included {
		return false
	}
	for _, pattern := range m.AuditScan.Exclude {
		if g, err := CompileGlob(pattern); err == nil && g.Match(path) {
			return false
		}
	}

	return true
}

// validateBaseline asserts that the recorded baseline cannot lie: both the fork
// base and the last integrated commit must actually be in this history.
func validateBaseline(opts Options) []Finding {
	var findings []Finding

	checks := []struct {
		code   string
		field  string
		commit string
	}{
		{"V12", "fork_base.commit", opts.Baseline.ForkBase.Commit},
		{"V11", "last_integrated.commit", opts.Baseline.LastIntegrated.Commit},
	}

	for _, check := range checks {
		if _, err := opts.Repo.Resolve(check.commit); err != nil {
			findings = append(findings, Finding{
				Code:     check.code,
				Path:     check.field,
				Message:  fmt.Sprintf("commit %s is not present in this repository", short(check.commit)),
				Severity: SeverityError,
			})

			continue
		}

		ancestor, err := opts.Repo.IsAncestor(check.commit, "HEAD")
		if err != nil || !ancestor {
			findings = append(findings, Finding{
				Code:     check.code,
				Path:     check.field,
				Message:  fmt.Sprintf("commit %s is not an ancestor of HEAD", short(check.commit)),
				Severity: SeverityError,
			})
		}
	}

	return findings
}

// checkDrift reports paths this fork has changed since the fork base that no
// rule classifies. It is the half of the gate that watches our own pull
// requests rather than upstream's.
func checkDrift(m *Manifest, opts Options) ([]Finding, error) {
	changes, err := opts.Repo.ChangedFiles(opts.Baseline.ForkBase.Commit, "HEAD")
	if err != nil {
		return nil, err
	}

	var undeclared []string
	for _, change := range changes {
		rule := m.Match(change.Path)
		if rule == nil || rule.IsCatchAll() {
			undeclared = append(undeclared, change.Path)
		}
	}
	sort.Strings(undeclared)

	findings := make([]Finding, 0, len(undeclared))
	for _, path := range undeclared {
		findings = append(findings, Finding{
			Code:    "D1",
			Path:    path,
			Message: "changed in this fork but not declared in the boundary manifest",
			// Undeclared rather than an error: the remedy is to declare the
			// path, which is a manifest edit, not a code revert.
			Severity: SeverityUndeclared,
		})
	}

	return findings, nil
}

// RemediationFor renders the copy-pasteable remedy printed after drift
// findings.
func RemediationFor(manifestPath string, findings []Finding) string {
	paths := make([]string, 0, len(findings))
	for _, f := range findings {
		if f.Code == "D1" {
			paths = append(paths, "  "+f.Path)
		}
	}
	if len(paths) == 0 {
		return ""
	}

	return fmt.Sprintf(
		"%d file(s) changed in this fork are not declared in %s:\n\n%s\n\n"+
			"Declare each one under an existing rule, or add a new rule above the catch-all, then run\n"+
			"  go run ./hack/cnpatroni/upstream/cmd/cnpatroni-upstream validate --drift\n",
		len(paths), manifestPath, strings.Join(paths, "\n"))
}

func existsInWorktree(root, path string) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))

	return err == nil
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}

	return commit
}
