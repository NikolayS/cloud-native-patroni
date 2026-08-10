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

package report_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/postgres-ai/cnpatroni-upstream/internal/baseline"
	"github.com/postgres-ai/cnpatroni-upstream/internal/boundary"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gittest"
	"github.com/postgres-ai/cnpatroni-upstream/internal/gitx"
	"github.com/postgres-ai/cnpatroni-upstream/internal/report"
)

const reportManifest = `
schema: cnpatroni.io/boundary/v1
generated_from: fixture
provisional: false
default_ownership: upstream-untouched
audit_scan:
  include: ["**/*.go"]
  exclude: ["**/*_test.go"]
audit_terms: ["TargetPrimary", "pg_ctl"]
generated_artifacts: ["api/**"]
required_paths: []
rules:
  - id: owned.tooling
    ownership: cnpatroni-owned
    state: planned
    paths: ["internal/patroni/**"]
  - id: adapt.process-primitives
    ownership: adapted
    gate: ["authority-audit", "chaos"]
    spec_refs: ["7.4"]
    audit_class: adapt
    destination: cnpatroni-agent
    paths: ["pkg/management/postgres/instance.go"]
  - id: disable.election
    ownership: disabled
    mechanism: unreferenced
    gate: ["authority-audit"]
    paths: ["internal/controller/replicas.go"]
  - id: adapt.api
    ownership: adapted
    gate: ["regenerate"]
    paths: ["api/v1/cluster_types.go"]
  - id: absorb
    ownership: upstream-untouched
    paths: ["**"]
`

// scenario is a two-branch repository: `main` is CloudNativePatroni, `upstream`
// stands in for upstream CloudNativePG.
type scenario struct {
	git      *gittest.Fixture
	repo     *gitx.Repo
	baseline *baseline.Baseline
	base     string
}

func newScenario(t *testing.T) *scenario {
	t.Helper()

	f := gittest.New(t)
	f.Write("pkg/management/postgres/instance.go", "package postgres\n\n// pg_ctl lives here.\n")
	f.Write("internal/controller/replicas.go", "package controller\n\n// TargetPrimary\n")
	f.Write("api/v1/cluster_types.go", "package v1\n")
	f.Write("docs/src/faq.md", "# Frequently asked questions\n")
	f.Write("go.sum", "checksums\n")
	base := f.Commit("fork base")
	f.Branch("upstream")

	repo, err := gitx.Open(f.Root)
	if err != nil {
		t.Fatalf("gitx.Open: %v", err)
	}

	b, err := baseline.Parse([]byte(`
schema: cnpatroni.io/upstream-baseline/v1
upstream:
  url: https://github.com/cloudnative-pg/cloudnative-pg
  track: main
fork_base:
  commit: ` + base + `
  describe: fixture
  date: "2026-01-01"
last_integrated:
  commit: ` + base + `
  describe: fixture
  date: "2026-01-01"
compatibility:
  cloudnative_pg_minor: "1.30"
  cloudnative_pg_reference_tag: v1.30.0
  claimed: fixture
  verified_at: "2026-01-01"
  unadopted_upstream_commits: 0
not_adopted: []
`))
	if err != nil {
		t.Fatalf("baseline.Parse: %v", err)
	}

	return &scenario{git: f, repo: repo, baseline: b, base: base}
}

// upstreamCommit records a commit on the upstream branch and returns to main.
func (s *scenario) upstreamCommit(subject string, files map[string]string) {
	s.git.Checkout("upstream")
	for path, content := range files {
		s.git.Write(path, content)
	}
	s.git.Commit(subject)
	s.git.Checkout("main")
}

func (s *scenario) build(t *testing.T) *report.Report {
	t.Helper()

	return s.buildWithManifest(t, reportManifest)
}

func (s *scenario) buildWithManifest(t *testing.T, body string) *report.Report {
	t.Helper()

	m, err := boundary.Parse([]byte(body))
	if err != nil {
		t.Fatalf("boundary.Parse: %v", err)
	}

	r, err := report.Build(report.Options{
		Repo:        s.repo,
		Manifest:    m,
		Baseline:    s.baseline,
		To:          "upstream",
		ToolVersion: "test",
		Now:         func() time.Time { return time.Date(2026, 8, 9, 22, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("report.Build: %v", err)
	}

	return r
}

func fileByPath(r *report.Report, path string) *report.File {
	for i := range r.Files {
		if r.Files[i].Path == path {
			return &r.Files[i]
		}
	}

	return nil
}

func TestReportNoChanges(t *testing.T) {
	s := newScenario(t)
	r := s.build(t)

	if r.Gate.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", r.Gate.ExitCode)
	}
	if r.Volume.UpstreamCommits != 0 || r.Volume.UpstreamFilesChanged != 0 {
		t.Errorf("volume = %+v, want an empty range", r.Volume)
	}
	if r.Files == nil {
		t.Error("files must serialise as an empty list, not null")
	}

	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"files":[]`) {
		t.Errorf("files serialised as null: %s", encoded)
	}
}

func TestReportChangesOutsideTheBoundary(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("docs: rewrite the FAQ", map[string]string{
		"docs/src/faq.md": "# Frequently asked questions\n\nMore text.\n",
		"go.sum":          "checksums\nmore\n",
	})

	r := s.build(t)

	if r.Gate.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0: %+v", r.Gate.ExitCode, r.Gate)
	}
	if r.Volume.UpstreamFilesChanged != 2 {
		t.Errorf("upstream files changed = %d, want 2", r.Volume.UpstreamFilesChanged)
	}
	if r.Volume.BoundaryFilesChanged != 0 {
		t.Errorf("boundary files changed = %d, want 0", r.Volume.BoundaryFilesChanged)
	}
	if r.Gate.RequiresAuthorityAudit || r.Gate.RequiresChaosGate {
		t.Errorf("no gate should be required: %+v", r.Gate)
	}
}

func TestReportChangesInsideTheBoundary(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("fix: shorten the fast shutdown timeout", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n\n// pg_ctl lives here.\n// New line.\n",
	})

	r := s.build(t)

	if r.Gate.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1: %+v", r.Gate.ExitCode, r.Gate)
	}
	f := fileByPath(r, "pkg/management/postgres/instance.go")
	if f == nil {
		t.Fatal("the boundary file is missing from the report")
	}
	if f.Ownership != boundary.OwnershipAdapted {
		t.Errorf("ownership = %q, want adapted", f.Ownership)
	}
	if f.RuleID != "adapt.process-primitives" {
		t.Errorf("rule = %q, want adapt.process-primitives", f.RuleID)
	}
	if f.Insertions != 1 || f.Deletions != 0 {
		t.Errorf("line counts = +%d/-%d, want +1/-0", f.Insertions, f.Deletions)
	}
	if !r.Gate.RequiresAuthorityAudit || !r.Gate.RequiresChaosGate {
		t.Errorf("gates = %+v, want authority-audit and chaos", r.Gate)
	}
	if !strings.Contains(r.Gate.Verdict, "needs review") {
		t.Errorf("verdict = %q, want it to say needs review", r.Gate.Verdict)
	}
}

func TestReportUnionsGatesAcrossZones(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("refactor: touch two zones", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n// pg_ctl\n// a\n",
		"internal/controller/replicas.go":     "package controller\n// TargetPrimary\n// b\n",
	})

	r := s.build(t)

	if r.Volume.BoundaryFilesChanged != 2 {
		t.Errorf("boundary files = %d, want 2", r.Volume.BoundaryFilesChanged)
	}
	if !r.Gate.RequiresAuthorityAudit || !r.Gate.RequiresChaosGate {
		t.Errorf("gates = %+v, want the union of both rules", r.Gate)
	}
	if got := fileByPath(r, "internal/controller/replicas.go"); got == nil ||
		got.Ownership != boundary.OwnershipDisabled {
		t.Errorf("replicas.go = %+v, want ownership disabled", got)
	}
}

func TestReportFlagsGeneratedArtifacts(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("feat: add a field", map[string]string{
		"api/v1/cluster_types.go": "package v1\n\ntype Extra struct{}\n",
	})

	r := s.build(t)

	if !r.GeneratedArtifactDrift.RegenerationRequired {
		t.Error("touching api/ must require regeneration")
	}
	if !r.Gate.RequiresRegeneration {
		t.Error("gate must require regeneration")
	}
}

func TestReportFlagsCollisionsWithOwnedPaths(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("feat: add a package that collides", map[string]string{
		"internal/patroni/config/render.go": "package config\n",
	})

	r := s.build(t)

	if r.Gate.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3: %+v", r.Gate.ExitCode, r.Gate)
	}
	if len(r.ManifestStability.CollisionsWithOwned) != 1 ||
		r.ManifestStability.CollisionsWithOwned[0] != "internal/patroni/config/render.go" {
		t.Errorf("collisions = %v", r.ManifestStability.CollisionsWithOwned)
	}
}

func TestReportFlagsNewUndeclaredHAVocabulary(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("feat: add an undeclared decision point", map[string]string{
		"internal/controller/newthing.go": "package controller\n\n// sets TargetPrimary\n",
	})

	r := s.build(t)

	if r.Gate.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3: %+v", r.Gate.ExitCode, r.Gate)
	}
	if len(r.AuthoritySurfaceDelta.Undeclared) != 1 ||
		r.AuthoritySurfaceDelta.Undeclared[0] != "internal/controller/newthing.go" {
		t.Errorf("undeclared = %v", r.AuthoritySurfaceDelta.Undeclared)
	}
}

func TestReportFlagsUndeclaredHAVocabularyWhenAnIncludePatternIsBroken(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("feat: add an undeclared decision point", map[string]string{
		"internal/controller/newthing.go": "package controller\n\n// sets TargetPrimary\n",
	})
	manifest := strings.Replace(reportManifest,
		`include: ["**/*.go"]`, `include: ["**/*.go["]`, 1)

	r := s.buildWithManifest(t, manifest)

	if len(r.AuthoritySurfaceDelta.Undeclared) != 1 ||
		r.AuthoritySurfaceDelta.Undeclared[0] != "internal/controller/newthing.go" {
		t.Errorf("AuthoritySurfaceDelta.Undeclared = %v, want the undeclared HA file",
			r.AuthoritySurfaceDelta.Undeclared)
	}
}

func TestReportDoesNotFlagDeclaredHAVocabulary(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("feat: extend a declared file", map[string]string{
		"internal/controller/replicas.go": "package controller\n// TargetPrimary\n// more\n",
	})

	r := s.build(t)

	if len(r.AuthoritySurfaceDelta.Undeclared) != 0 {
		t.Errorf("undeclared = %v, want none", r.AuthoritySurfaceDelta.Undeclared)
	}
	if r.Gate.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", r.Gate.ExitCode)
	}
}

func TestReportPredictsTextualConflicts(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("fix: change the shared line", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n\n// pg_ctl upstream wording.\n",
	})
	s.git.Write("pkg/management/postgres/instance.go", "package postgres\n\n// pg_ctl CloudNativePatroni wording.\n")
	s.git.Commit("guard the process primitives")

	r := s.build(t)

	if r.Gate.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2: %+v", r.Gate.ExitCode, r.Gate)
	}
	f := fileByPath(r, "pkg/management/postgres/instance.go")
	if f == nil || !f.PredictedConflict {
		t.Errorf("instance.go = %+v, want predicted_conflict true", f)
	}
	if !f.ForkModified {
		t.Error("instance.go must be reported as modified by this fork")
	}
}

// A boundary file that merges cleanly must still be reported, because git
// merging it without complaint is exactly the failure this report exists to
// catch.
func TestReportFlagsCleanMergeOnBoundaryFile(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("feat: append to a boundary file", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n\n// pg_ctl lives here.\n// appended\n",
	})
	s.git.Write("docs/src/faq.md", "# Frequently asked questions\n\nFork edit.\n")
	s.git.Commit("edit the documentation")

	r := s.build(t)

	if r.Gate.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1 even though the merge is clean", r.Gate.ExitCode)
	}
	f := fileByPath(r, "pkg/management/postgres/instance.go")
	if f == nil || f.PredictedConflict {
		t.Errorf("instance.go = %+v, want a clean but reported boundary file", f)
	}
}

func TestReportRecordsUpstreamDeletionOfAdaptedPath(t *testing.T) {
	s := newScenario(t)
	s.git.Checkout("upstream")
	s.git.Remove("pkg/management/postgres/instance.go")
	s.git.Commit("refactor: drop the file")
	s.git.Checkout("main")

	r := s.build(t)

	f := fileByPath(r, "pkg/management/postgres/instance.go")
	if f == nil || f.UpstreamStatus != "D" {
		t.Fatalf("instance.go = %+v, want status D", f)
	}
	if len(r.ManifestStability.DeclaredButMissingUpstream) != 1 {
		t.Errorf("declared_but_missing_upstream = %v",
			r.ManifestStability.DeclaredButMissingUpstream)
	}
}

func TestReportListsCommitsTouchingTheBoundary(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("fix: promote faster", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n// pg_ctl\n// x\n",
	})
	s.upstreamCommit("docs: unrelated", map[string]string{
		"docs/src/faq.md": "# Frequently asked questions\n\nx\n",
	})

	r := s.build(t)

	if len(r.Commits) != 1 {
		t.Fatalf("boundary commits = %d, want 1: %+v", len(r.Commits), r.Commits)
	}
	if r.Commits[0].Subject != "fix: promote faster" {
		t.Errorf("subject = %q", r.Commits[0].Subject)
	}
	if !r.Commits[0].HASignal {
		t.Error("a subject containing \"promote\" must be flagged as an HA signal")
	}
	if r.Volume.UpstreamCommits != 2 {
		t.Errorf("upstream commits = %d, want 2", r.Volume.UpstreamCommits)
	}
}

func TestReportComputesConcentrationRatio(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("chore: touch four files", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n// pg_ctl\n// x\n",
		"docs/src/faq.md":                     "# Frequently asked questions\n\nx\n",
		"go.sum":                              "checksums\nx\n",
		"api/v1/cluster_types.go":             "package v1\n\ntype X struct{}\n",
	})

	r := s.build(t)

	if r.Volume.UpstreamFilesChanged != 4 {
		t.Fatalf("upstream files = %d, want 4", r.Volume.UpstreamFilesChanged)
	}
	if r.Volume.BoundaryFilesChanged != 2 {
		t.Fatalf("boundary files = %d, want 2", r.Volume.BoundaryFilesChanged)
	}
	if r.Volume.ConcentrationRatio != 0.5 {
		t.Errorf("concentration ratio = %v, want 0.5", r.Volume.ConcentrationRatio)
	}
}

func TestBuildRefusesAMissingUpstreamRemote(t *testing.T) {
	s := newScenario(t)
	m, err := boundary.Parse([]byte(reportManifest))
	if err != nil {
		t.Fatalf("boundary.Parse: %v", err)
	}

	// An empty To falls back to the tracking ref of the configured remote,
	// which this fixture does not have.
	_, err = report.Build(report.Options{
		Repo: s.repo, Manifest: m, Baseline: s.baseline, ToolVersion: "test",
	})

	var envErr *gitx.EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("error %v is not an EnvironmentError", err)
	}
	if !strings.Contains(envErr.Remedy, "setup") {
		t.Errorf("remedy %q should point at the setup command", envErr.Remedy)
	}
}

func TestBuildRefusesAShallowClone(t *testing.T) {
	origin := gittest.New(t)
	origin.Write("a.go", "package a\n")
	origin.Commit("one")
	origin.Write("a.go", "package a\n// two\n")
	origin.Commit("two")

	shallowRoot := t.TempDir() + "/shallow"
	gittest.RunGit(t, "", "clone", "--depth", "1", "--no-local", "file://"+origin.Root, shallowRoot)

	repo, err := gitx.Open(shallowRoot)
	if err != nil {
		t.Fatalf("gitx.Open: %v", err)
	}
	m, err := boundary.Parse([]byte(reportManifest))
	if err != nil {
		t.Fatalf("boundary.Parse: %v", err)
	}
	b, err := baseline.Parse([]byte(`
schema: cnpatroni.io/upstream-baseline/v1
upstream: {url: "https://example.invalid", track: main}
fork_base: {commit: ` + strings.Repeat("0", 40) + `, describe: x, date: "2026-01-01"}
last_integrated: {commit: ` + strings.Repeat("0", 40) + `, describe: x, date: "2026-01-01"}
compatibility:
  cloudnative_pg_minor: "1.30"
  cloudnative_pg_reference_tag: v1.30.0
  claimed: fixture
  verified_at: "2026-01-01"
  unadopted_upstream_commits: 0
not_adopted: []
`))
	if err != nil {
		t.Fatalf("baseline.Parse: %v", err)
	}

	_, err = report.Build(report.Options{
		Repo: repo, Manifest: m, Baseline: b, To: "HEAD", ToolVersion: "test",
	})

	var envErr *gitx.EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("error %v is not an EnvironmentError", err)
	}
}

// The concentration ratio must separate the paths that carry high-availability
// authority from the branding and build files, which are on the boundary but
// carry no failover risk.
func TestReportSeparatesAuthorityConcentration(t *testing.T) {
	s := newScenario(t)
	s.upstreamCommit("chore: touch two boundary zones", map[string]string{
		"pkg/management/postgres/instance.go": "package postgres\n// pg_ctl\n// x\n",
		"api/v1/cluster_types.go":             "package v1\n\ntype X struct{}\n",
		"docs/src/faq.md":                     "# Frequently asked questions\n\nx\n",
		"go.sum":                              "checksums\nx\n",
	})

	r := s.build(t)

	if r.Volume.BoundaryFilesChanged != 2 {
		t.Fatalf("boundary files = %d, want 2", r.Volume.BoundaryFilesChanged)
	}
	// Only adapt.process-primitives declares an audit_class in this manifest.
	if r.Volume.AuthorityFilesChanged != 1 {
		t.Fatalf("authority files = %d, want 1", r.Volume.AuthorityFilesChanged)
	}
	if r.Volume.AuthorityConcentrationRatio != 0.25 {
		t.Errorf("authority ratio = %v, want 0.25", r.Volume.AuthorityConcentrationRatio)
	}
}

func TestReportRecordsTheManifestPathRelativeToTheRoot(t *testing.T) {
	s := newScenario(t)
	s.git.Write("hack/cnpatroni/upstream/boundary.yaml", reportManifest)
	s.git.Commit("record the manifest")

	m, err := boundary.Load(s.git.Root + "/hack/cnpatroni/upstream/boundary.yaml")
	if err != nil {
		t.Fatalf("boundary.Load: %v", err)
	}
	r, err := report.Build(report.Options{
		Repo: s.repo, Manifest: m, Baseline: s.baseline, To: "upstream", ToolVersion: "test",
	})
	if err != nil {
		t.Fatalf("report.Build: %v", err)
	}

	if r.Manifest.Path != "hack/cnpatroni/upstream/boundary.yaml" {
		t.Fatalf("manifest path = %q, want the repository-relative form", r.Manifest.Path)
	}
	if r.Manifest.SHA256 == "" {
		t.Error("the report must record the manifest digest")
	}
}
