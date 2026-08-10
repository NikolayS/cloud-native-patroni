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

package report

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/postgres-ai/cnpatroni-upstream/internal/baseline"
	"github.com/postgres-ai/cnpatroni-upstream/internal/boundary"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
)

// haSignal matches the commit subjects that are worth a second look even when
// they touch no declared boundary path. It is advisory and never a gate on its
// own.
var haSignal = regexp.MustCompile(
	`(?i)promot|demot|failover|switchover|fenc|primary|standby|rewind|shutdown|restart|probe|readiness|sync.*replic`)

// Exit codes, shared with the command-line interface.
const (
	// ExitOK means nothing in the range needs a human.
	ExitOK = 0
	// ExitNeedsReview means upstream changed a path this fork adapts,
	// disables or deletes.
	ExitNeedsReview = 1
	// ExitConflict means the merge is predicted to conflict textually.
	ExitConflict = 2
	// ExitUndeclared means upstream introduced high-availability code the
	// manifest does not classify, or collided with a path this fork owns.
	ExitUndeclared = 3
)

// Options configures a report computation.
type Options struct {
	Repo     *gitx.Repo
	Manifest *boundary.Manifest
	Baseline *baseline.Baseline

	// From defaults to the baseline's last integrated commit.
	From string
	// To defaults to the tracking ref of the configured upstream remote.
	To string
	// SkipConflictCheck omits the `git merge-tree` prediction.
	SkipConflictCheck bool

	ToolVersion string
	// Now is injected so that reports are reproducible in tests.
	Now func() time.Time
}

// Build computes the divergence report. It never mutates the worktree.
func Build(opts Options) (*Report, error) {
	if opts.Repo == nil || opts.Manifest == nil || opts.Baseline == nil {
		return nil, fmt.Errorf("a repository, a manifest and a baseline are all required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	if err := opts.Repo.RequireCompleteHistory(); err != nil {
		return nil, err
	}

	toRef := opts.To
	if toRef == "" {
		if err := opts.Repo.RequireRemote(opts.Baseline.Upstream.Remote); err != nil {
			return nil, err
		}
		toRef = opts.Baseline.TrackingRef()
	}
	fromRef := opts.From
	if fromRef == "" {
		fromRef = opts.Baseline.LastIntegrated.Commit
	}

	from, err := opts.Repo.Resolve(fromRef)
	if err != nil {
		return nil, err
	}
	to, err := opts.Repo.Resolve(toRef)
	if err != nil {
		return nil, err
	}
	head, err := opts.Repo.Resolve("HEAD")
	if err != nil {
		return nil, err
	}

	r := &Report{
		Schema:      Schema,
		GeneratedAt: opts.Now().UTC().Format(time.RFC3339),
		ToolVersion: opts.ToolVersion,
		Repository:  opts.Repo.Root,
		Manifest: ManifestRef{
			Path:        relativeToRoot(opts.Repo.Root, opts.Manifest.Path()),
			SHA256:      opts.Manifest.SHA256(),
			Provisional: opts.Manifest.Provisional,
			RuleCount:   len(opts.Manifest.Rules),
		},
		Files:   []File{},
		Commits: []Commit{},
		AuthoritySurfaceDelta: AuthoritySurfaceDelta{
			NewHAFiles: []string{}, RemovedHAFiles: []string{}, Undeclared: []string{},
		},
		ManifestStability: ManifestStability{
			DeclaredButMissingUpstream: []string{}, CollisionsWithOwned: []string{},
		},
		GeneratedArtifactDrift: GeneratedArtifactDrift{Touched: []string{}},
		Conflicts:              Conflicts{Predicted: []string{}},
	}

	r.Range = buildRange(opts, fromRef, from, toRef, to, head)

	if err := buildFiles(r, opts, from, to, head); err != nil {
		return nil, err
	}
	if err := buildCommits(r, opts, from, to); err != nil {
		return nil, err
	}
	if err := buildAuthorityDelta(r, opts, from, to); err != nil {
		return nil, err
	}
	if err := buildManifestStability(r, opts, to); err != nil {
		return nil, err
	}

	r.Volume.UpstreamCommits, err = opts.Repo.CommitCount(from, to)
	if err != nil {
		return nil, err
	}

	buildGate(r)

	return r, nil
}

// relativeToRoot renders a path relative to the repository root, so that a
// report is identical no matter where the clone lives.
func relativeToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}

	return filepath.ToSlash(rel)
}

func buildRange(opts Options, fromRef, from, toRef, to, head string) Range {
	repo := opts.Repo
	mergeBase, _ := repo.MergeBase(head, to)

	return Range{
		From: Ref{
			Ref: fromRef, Commit: from,
			Describe: repo.Describe(from), Date: repo.CommitDate(from),
		},
		To: Ref{
			Ref: toRef, Commit: to,
			Describe: repo.Describe(to), Date: repo.CommitDate(to),
		},
		ForkBase: Ref{
			Commit:   opts.Baseline.ForkBase.Commit,
			Describe: opts.Baseline.ForkBase.Describe,
			Date:     opts.Baseline.ForkBase.Date,
		},
		OurHead:   Ref{Commit: head, Describe: repo.Describe(head), Branch: repo.CurrentBranch()},
		MergeBase: mergeBase,
	}
}

// buildFiles classifies every upstream-changed path and computes the volume
// counters, including the concentration ratio the fork is judged by.
func buildFiles(r *Report, opts Options, from, to, head string) error {
	repo := opts.Repo

	changes, err := repo.ChangedFiles(from, to)
	if err != nil {
		return err
	}
	stats, err := repo.NumStat(from, to)
	if err != nil {
		return err
	}

	forkChanged := map[string]bool{}
	forkChanges, err := repo.ChangedFiles(opts.Baseline.ForkBase.Commit, head)
	if err != nil {
		return err
	}
	for _, c := range forkChanges {
		forkChanged[c.Path] = true
	}

	conflicts := map[string]bool{}
	if !opts.SkipConflictCheck {
		predicted, err := repo.MergeTreeConflicts("HEAD", to)
		if err != nil {
			return err
		}
		sort.Strings(predicted)
		r.Conflicts = Conflicts{Predicted: predicted, Checked: true}
		if r.Conflicts.Predicted == nil {
			r.Conflicts.Predicted = []string{}
		}
		for _, path := range predicted {
			conflicts[path] = true
		}
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })

	collisions := r.ManifestStability.CollisionsWithOwned
	for _, change := range changes {
		file := classify(opts.Manifest, change, stats[change.Path])
		file.ForkModified = forkChanged[change.Path]
		file.PredictedConflict = conflicts[change.Path]
		if change.Status == "D" && file.Ownership == boundary.OwnershipAdapted {
			file.ConflictKind = "modify/delete"
		}

		r.Volume.UpstreamFilesChanged++
		r.Volume.UpstreamInsertions += file.Insertions
		r.Volume.UpstreamDeletions += file.Deletions
		if file.OnBoundary {
			r.Volume.BoundaryFilesChanged++
			r.Volume.BoundaryInsertions += file.Insertions
			r.Volume.BoundaryDeletions += file.Deletions
			if file.AuditClass != "" {
				r.Volume.AuthorityFilesChanged++
			}
		}
		if opts.Manifest.IsGeneratedArtifact(change.Path) {
			r.GeneratedArtifactDrift.Touched = append(r.GeneratedArtifactDrift.Touched, change.Path)
			r.GeneratedArtifactDrift.RegenerationRequired = true
		}
		if file.Ownership == boundary.OwnershipCNPatroniOwned {
			collisions = append(collisions, change.Path)
		}

		r.Files = append(r.Files, file)
	}

	r.ManifestStability.CollisionsWithOwned = collisions

	if total := float64(r.Volume.UpstreamFilesChanged); total > 0 {
		r.Volume.ConcentrationRatio = float64(r.Volume.BoundaryFilesChanged) / total
		r.Volume.AuthorityConcentrationRatio = float64(r.Volume.AuthorityFilesChanged) / total
	}

	return nil
}

func classify(m *boundary.Manifest, change gitx.FileChange, delta gitx.LineDelta) File {
	file := File{
		Path:           change.Path,
		UpstreamStatus: change.Status,
		Insertions:     delta.Insertions,
		Deletions:      delta.Deletions,
		Ownership:      boundary.OwnershipUpstreamUntouched,
		Gate:           []string{},
		SpecRefs:       []string{},
		MatchedPattern: m.MatchGlob(change.Path),
	}

	rule := m.Match(change.Path)
	if rule == nil {
		return file
	}

	file.Ownership = rule.Ownership
	file.RuleID = rule.ID
	file.AuditClass = rule.AuditClass
	file.Destination = rule.Destination
	if rule.Gate != nil {
		file.Gate = rule.Gate
	}
	if rule.SpecRefs != nil {
		file.SpecRefs = rule.SpecRefs
	}
	file.NeedsReview = rule.Ownership.NeedsReview()
	file.OnBoundary = !rule.IsCatchAll() && rule.Ownership != boundary.OwnershipUpstreamUntouched

	return file
}

// buildCommits lists the upstream commits that touched a boundary path.
func buildCommits(r *Report, opts Options, from, to string) error {
	boundaryPaths := make([]string, 0, len(r.Files))
	byPath := map[string]bool{}
	for _, f := range r.Files {
		if f.OnBoundary {
			boundaryPaths = append(boundaryPaths, f.Path)
			byPath[f.Path] = true
		}
	}
	if len(boundaryPaths) == 0 {
		return nil
	}

	commits, err := opts.Repo.Commits(from, to, boundaryPaths)
	if err != nil {
		return err
	}

	for _, c := range commits {
		files, err := changedBoundaryFiles(opts, c.SHA, byPath)
		if err != nil {
			return err
		}
		r.Commits = append(r.Commits, Commit{
			SHA:           c.SHA,
			Subject:       c.Subject,
			Author:        c.Author,
			Date:          c.Date,
			BoundaryFiles: files,
			HASignal:      haSignal.MatchString(c.Subject),
		})
	}

	return nil
}

func changedBoundaryFiles(opts Options, sha string, boundaryPaths map[string]bool) ([]string, error) {
	changes, err := opts.Repo.ChangedFiles(sha+"^", sha)
	if err != nil {
		return nil, err
	}

	files := []string{}
	for _, change := range changes {
		if boundaryPaths[change.Path] {
			files = append(files, change.Path)
		}
	}
	sort.Strings(files)

	return files, nil
}

// buildAuthorityDelta reports the files that carry high-availability vocabulary
// at the far end of the range but did not at the near end. An entry that the
// manifest classifies only by the catch-all is the signal that upstream has
// grown a new decision point this fork does not track.
func buildAuthorityDelta(r *Report, opts Options, from, to string) error {
	m := opts.Manifest
	if len(m.AuditTerms) == 0 || len(r.Files) == 0 {
		return nil
	}

	candidates := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		if f.UpstreamStatus != "D" {
			candidates = append(candidates, f.Path)
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	atTo, err := opts.Repo.GrepFilesAt(to, m.AuditTerms, candidates)
	if err != nil {
		return err
	}
	atFrom, err := opts.Repo.GrepFilesAt(from, m.AuditTerms, candidates)
	if err != nil {
		return err
	}

	wasHA := map[string]bool{}
	for _, path := range atFrom {
		wasHA[path] = true
	}
	isHA := map[string]bool{}

	for _, path := range atTo {
		if !inAuditScan(m, path) {
			continue
		}
		isHA[path] = true
		if !wasHA[path] {
			r.AuthoritySurfaceDelta.NewHAFiles = append(r.AuthoritySurfaceDelta.NewHAFiles, path)
		}

		rule := m.Match(path)
		reviewed := rule != nil && rule.AuditReviewed
		untouched := rule == nil || rule.Ownership == boundary.OwnershipUpstreamUntouched
		if untouched && !reviewed {
			r.AuthoritySurfaceDelta.Undeclared = append(r.AuthoritySurfaceDelta.Undeclared, path)
		}
	}

	removed := r.AuthoritySurfaceDelta.RemovedHAFiles
	for _, path := range atFrom {
		if !isHA[path] && inAuditScan(m, path) {
			removed = append(removed, path)
		}
	}
	r.AuthoritySurfaceDelta.RemovedHAFiles = removed

	sort.Strings(r.AuthoritySurfaceDelta.NewHAFiles)
	sort.Strings(r.AuthoritySurfaceDelta.RemovedHAFiles)
	sort.Strings(r.AuthoritySurfaceDelta.Undeclared)

	return nil
}

func inAuditScan(m *boundary.Manifest, path string) bool {
	included := len(m.AuditScan.Include) == 0
	for _, pattern := range m.AuditScan.Include {
		if g, err := boundary.CompileGlob(pattern); err == nil && g.Match(path) {
			included = true

			break
		}
	}
	if !included {
		return false
	}
	for _, pattern := range m.AuditScan.Exclude {
		if g, err := boundary.CompileGlob(pattern); err == nil && g.Match(path) {
			return false
		}
	}

	return true
}

// buildManifestStability reports the declared literal paths that upstream no
// longer carries, which is how a rule quietly stops classifying anything.
func buildManifestStability(r *Report, opts Options, to string) error {
	tree, err := opts.Repo.TreePaths(to)
	if err != nil {
		return err
	}

	m := opts.Manifest
	missing := r.ManifestStability.DeclaredButMissingUpstream
	for i := range m.Rules {
		rule := &m.Rules[i]
		if rule.IsCatchAll() || rule.EffectiveState() == boundary.StatePlanned {
			continue
		}
		switch rule.Ownership {
		case boundary.OwnershipAdapted, boundary.OwnershipDisabled:
		case boundary.OwnershipUpstreamUntouched, boundary.OwnershipCNPatroniOwned,
			boundary.OwnershipDeleted:
			continue
		default:
			continue
		}

		for _, g := range rule.Globs() {
			if !g.IsLiteral() || tree[g.Pattern()] {
				continue
			}
			missing = append(missing, g.Pattern())
		}
	}
	sort.Strings(missing)
	r.ManifestStability.DeclaredButMissingUpstream = missing

	return nil
}

// buildGate turns the report into the one decision the maintainer needs.
func buildGate(r *Report) {
	gates := map[string]bool{}
	needsReview := 0
	for _, f := range r.Files {
		if f.NeedsReview {
			needsReview++
		}
		if !f.OnBoundary {
			continue
		}
		for _, gate := range f.Gate {
			gates[gate] = true
		}
	}

	r.Gate.RequiresAuthorityAudit = gates["authority-audit"]
	r.Gate.RequiresChaosGate = gates["chaos"]
	r.Gate.RequiresADRReview = gates["adr-review"]
	r.Gate.RequiresRegeneration = gates["regenerate"] || r.GeneratedArtifactDrift.RegenerationRequired

	code := ExitOK
	if needsReview > 0 {
		code = ExitNeedsReview
	}
	if len(r.Conflicts.Predicted) > 0 && code < ExitConflict {
		code = ExitConflict
	}
	if len(r.AuthoritySurfaceDelta.Undeclared) > 0 || len(r.ManifestStability.CollisionsWithOwned) > 0 {
		code = ExitUndeclared
	}
	r.Gate.ExitCode = code

	switch code {
	case ExitOK:
		r.Gate.Verdict = fmt.Sprintf("absorb: %d boundary files touched by %d upstream commits",
			r.Volume.BoundaryFilesChanged, r.Volume.UpstreamCommits)
	case ExitConflict:
		r.Gate.Verdict = fmt.Sprintf(
			"needs review, conflicts predicted: %d boundary files and %d conflicting paths",
			r.Volume.BoundaryFilesChanged, len(r.Conflicts.Predicted))
	case ExitUndeclared:
		r.Gate.Verdict = fmt.Sprintf(
			"needs review, manifest incomplete: %d undeclared high-availability files, %d collisions",
			len(r.AuthoritySurfaceDelta.Undeclared), len(r.ManifestStability.CollisionsWithOwned))
	default:
		r.Gate.Verdict = fmt.Sprintf("needs review: %d boundary files touched by %d upstream commits",
			r.Volume.BoundaryFilesChanged, r.Volume.UpstreamCommits)
	}
}
