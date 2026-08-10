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

	patternLists := []struct {
		name string
		err  []patternError
	}{
		{"audit_scan.include", m.auditIncludeErrors},
		{"audit_scan.exclude", m.auditExcludeErrors},
		{"generated_artifacts", m.generatedArtifactErrors},
	}
	for _, list := range patternLists {
		for _, patternErr := range list.err {
			findings = append(findings, Finding{
				Code:     "V4",
				Path:     patternErr.pattern,
				Message:  fmt.Sprintf("%s: %v", list.name, patternErr.err),
				Severity: SeverityError,
			})
		}
	}

	if len(m.AuditTerms) == 0 {
		findings = append(findings, Finding{
			Code:     "V14",
			Message:  "audit_terms is empty, so the V9 high-availability vocabulary check reports nothing",
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
		if !m.InAuditScan(path) {
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

// checkDrift reports paths this fork has changed since the fork base whose
// declaration does not account for the change: paths no rule classifies (D1),
// paths whose declared ownership class promises no change at all (D2), and
// paths declared adapted that did not exist at the fork base (D3). It is the
// half of the gate that watches our own pull requests rather than upstream's.
//
// Matching a specific rule is not on its own an explanation. An
// upstream-untouched rule states that the file is upstream's verbatim, so
// suppressing the finding for it would let any fork edit be laundered past the
// gate simply by naming the path. That class is therefore proved rather than
// believed, against the content of the fork base.
func checkDrift(m *Manifest, opts Options) ([]Finding, error) {
	changes, err := opts.Repo.ChangedFiles(opts.Baseline.ForkBase.Commit, "HEAD")
	if err != nil {
		return nil, err
	}

	var findings []Finding
	var verbatimClaims []FileClaim
	for _, change := range changes {
		rule := m.Match(change.Path)
		switch {
		case rule == nil || rule.IsCatchAll():
			findings = append(findings, Finding{
				Code:    "D1",
				Path:    change.Path,
				Message: "changed in this fork but not declared in the boundary manifest",
				// Undeclared rather than an error: the remedy is to declare the
				// path, which is a manifest edit, not a code revert.
				Severity: SeverityUndeclared,
			})
		case rule.Ownership == OwnershipAdapted:
			// This relies on ChangedFiles yielding only blob paths; cat-file -e also succeeds for directories.
			existed, existsErr := opts.Repo.PathExistsAt(opts.Baseline.ForkBase.Commit, change.Path)
			if existsErr != nil {
				return nil, existsErr
			}
			if !existed {
				findings = append(findings, Finding{
					Code:   "D3",
					RuleID: rule.ID,
					Path:   change.Path,
					Message: "declared adapted but did not exist at the fork base; " +
						"reclassify it as cnpatroni-owned",
					Severity: SeverityUndeclared,
				})
			}
		case ownershipExplainsChange(rule, change.Path, opts.Repo.Root):
		case rule.Ownership == OwnershipUpstreamUntouched:
			// Deferred: the content check needs one pass over two trees, so it
			// runs once for every claim rather than once per path.
			verbatimClaims = append(verbatimClaims, FileClaim{Path: change.Path, Rule: rule})
		default:
			findings = append(findings, Finding{
				Code:     "D2",
				RuleID:   rule.ID,
				Path:     change.Path,
				Message:  misdeclaredMessage(rule),
				Severity: SeverityUndeclared,
			})
		}
	}

	if len(verbatimClaims) > 0 {
		content, contentErr := newForkBaseContent(opts.Repo, opts.Baseline.ForkBase.Commit)
		if contentErr != nil {
			return nil, contentErr
		}
		for _, claim := range verbatimClaims {
			present, verbatim := content.holds(claim.Path)
			if verbatim {
				continue
			}
			findings = append(findings, Finding{
				Code:     "D2",
				RuleID:   claim.Rule.ID,
				Path:     claim.Path,
				Message:  unverifiedMessage(present),
				Severity: SeverityUndeclared,
			})
		}
	}

	// D1 before D2 before D3, each group by path, so one run reads the same way
	// three times.
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Code != findings[j].Code {
			return findings[i].Code < findings[j].Code
		}

		return findings[i].Path < findings[j].Path
	})

	return findings, nil
}

// FileClaim is a path whose declared class still has to be proved.
type FileClaim struct {
	Path string
	Rule *Rule
}

// forkBaseContent answers whether a path at HEAD holds bytes that were already
// in the fork base tree, wherever they sat in it. The question is content
// identity, not path identity: a verbatim copy parked at a new path is still
// upstream's bytes, while an edit to a file kept at its old path is not, and
// no line of the manifest can change either answer. That is what makes
// upstream-untouched a measurement rather than a promise.
type forkBaseContent struct {
	head map[string]string
	base map[string]bool
}

func newForkBaseContent(repo *gitx.Repo, forkBase string) (*forkBaseContent, error) {
	head, err := repo.TreeBlobs("HEAD")
	if err != nil {
		return nil, err
	}
	baseBlobs, err := repo.TreeBlobs(forkBase)
	if err != nil {
		return nil, err
	}

	base := make(map[string]bool, len(baseBlobs))
	for _, blob := range baseBlobs {
		base[blob] = true
	}

	return &forkBaseContent{head: head, base: base}, nil
}

// holds reports whether the path exists at HEAD, and whether its content came
// from the fork base. A path the fork removed is present: false, and a path
// whose bytes are not in the fork base tree is verbatim: false. Absence of
// proof is drift.
func (c *forkBaseContent) holds(path string) (present, verbatim bool) {
	blob, present := c.head[path]
	if !present {
		return false, false
	}

	return true, c.base[blob]
}

// ownershipExplainsChange reports whether the ownership class a rule declares
// legitimately accounts for the path differing from the fork base. It answers
// only for the classes that need no evidence beyond the worktree;
// upstream-untouched is settled against the fork base content instead.
func ownershipExplainsChange(rule *Rule, path, root string) bool {
	switch rule.Ownership {
	case OwnershipAdapted, OwnershipDisabled, OwnershipCNPatroniOwned:
		return true
	case OwnershipDeleted:
		// Only absence explains it. A deleted path still in the worktree is
		// reported by V6 or V7 when it is declared literally, and by nothing at
		// all when a glob declares it.
		return !existsInWorktree(root, path)
	case OwnershipUpstreamUntouched:
		return false
	default:
		// An unknown class is already a V0 error. Fail closed here too.
		return false
	}
}

// unverifiedMessage names which half of the upstream-untouched claim failed,
// because a path holding bytes this fork wrote is a different problem from a
// path this fork removed.
func unverifiedMessage(present bool) string {
	if !present {
		return "removed in this fork but declared upstream-untouched; declare it deleted, " +
			"or restore the file"
	}

	return "declared upstream-untouched but holds content that is not the bytes it had at the fork base; " +
		"restore the upstream content, or reclassify the path"
}

func misdeclaredMessage(rule *Rule) string {
	if rule.Ownership == OwnershipDeleted {
		return "changed in this fork and declared deleted, but the path is still present in the worktree"
	}

	// Reached only by an ownership class this tool does not know, which V0
	// already reports as an error.
	return fmt.Sprintf("changed in this fork but declared %s, which does not account for a change",
		rule.Ownership)
}

// RemediationFor renders the copy-pasteable remedy printed after drift
// findings.
func RemediationFor(manifestPath string, findings []Finding) string {
	var undeclared, misdeclared, falselyAdapted []string
	for _, f := range findings {
		switch f.Code {
		case "D1":
			undeclared = append(undeclared, "  "+f.Path)
		case "D2":
			misdeclared = append(misdeclared, fmt.Sprintf("  %s (rule %s)", f.Path, f.RuleID))
		case "D3":
			falselyAdapted = append(falselyAdapted, fmt.Sprintf("  %s (rule %s)", f.Path, f.RuleID))
		}
	}

	rerun := "then run\n  " + fmt.Sprintf(gitx.ToolInvocation, "validate --drift") + "\n"

	var b strings.Builder
	if len(undeclared) > 0 {
		fmt.Fprintf(&b,
			"%d file(s) changed in this fork are not declared in %s:\n\n%s\n\n"+
				"Declare each one under an existing rule, or add a new rule above the catch-all, %s",
			len(undeclared), manifestPath, strings.Join(undeclared, "\n"), rerun)
	}
	if len(misdeclared) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b,
			"%d file(s) changed in this fork do not hold the content their rule in %s claims:\n\n%s\n\n"+
				"These are declared under a rule that promises no change. upstream-untouched is\n"+
				"proved against the fork base, not asserted: a path is clean only while it holds\n"+
				"bytes that were already in the fork base tree, wherever they sat in it.\n"+
				"Restore the upstream content, or reclassify each path as adapted, disabled,\n"+
				"cnpatroni-owned or deleted, %s",
			len(misdeclared), manifestPath, strings.Join(misdeclared, "\n"), rerun)
	}
	if len(falselyAdapted) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b,
			"%d file(s) new to this fork are declared adapted in %s:\n\n%s\n\n"+
				"Adapted is only for inherited paths that existed at the fork base.\n"+
				"Reclassify each path as cnpatroni-owned, or remove it if it was unintended, %s",
			len(falselyAdapted), manifestPath, strings.Join(falselyAdapted, "\n"), rerun)
	}

	return b.String()
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
