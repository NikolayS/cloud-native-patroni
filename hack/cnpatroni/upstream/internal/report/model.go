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

// Package report computes the divergence between the CloudNativePatroni fork
// and upstream CloudNativePG, and intersects it with the boundary manifest.
package report

import (
	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/boundary"
)

// Schema is the identifier of the machine-readable report format.
const Schema = "cnpatroni.io/upstream-report/v1"

// ManifestRef names the manifest that produced a report.
type ManifestRef struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Provisional bool   `json:"provisional"`
	RuleCount   int    `json:"rule_count"`
}

// Ref is one end of the compared range.
type Ref struct {
	Ref      string `json:"ref,omitempty"`
	Commit   string `json:"commit"`
	Describe string `json:"describe"`
	Date     string `json:"date,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

// Range records what was compared with what.
type Range struct {
	From      Ref    `json:"from"`
	To        Ref    `json:"to"`
	ForkBase  Ref    `json:"fork_base"`
	OurHead   Ref    `json:"our_head"`
	MergeBase string `json:"merge_base"`
}

// Volume is the size of the divergence, and the concentration metric the fork
// is judged by.
type Volume struct {
	UpstreamCommits      int     `json:"upstream_commits"`
	UpstreamFilesChanged int     `json:"upstream_files_changed"`
	UpstreamInsertions   int     `json:"upstream_insertions"`
	UpstreamDeletions    int     `json:"upstream_deletions"`
	BoundaryFilesChanged int     `json:"boundary_files_changed"`
	BoundaryInsertions   int     `json:"boundary_insertions"`
	BoundaryDeletions    int     `json:"boundary_deletions"`
	ConcentrationRatio   float64 `json:"concentration_ratio"`
	// AuthorityFilesChanged counts the subset of the boundary that carries
	// PostgreSQL high-availability authority, that is, rules the M0 audit
	// classified. It excludes the branding and build files, which are on the
	// boundary but carry no failover risk.
	AuthorityFilesChanged       int     `json:"authority_files_changed"`
	AuthorityConcentrationRatio float64 `json:"authority_concentration_ratio"`
}

// File is one upstream-changed path, classified.
type File struct {
	Path              string             `json:"path"`
	UpstreamStatus    string             `json:"upstream_status"`
	Insertions        int                `json:"insertions"`
	Deletions         int                `json:"deletions"`
	Ownership         boundary.Ownership `json:"ownership"`
	RuleID            string             `json:"rule_id"`
	MatchedPattern    string             `json:"matched_pattern"`
	AuditClass        string             `json:"audit_class,omitempty"`
	Destination       string             `json:"destination,omitempty"`
	Gate              []string           `json:"gate"`
	SpecRefs          []string           `json:"spec_refs"`
	OnBoundary        bool               `json:"on_boundary"`
	NeedsReview       bool               `json:"needs_review"`
	ForkModified      bool               `json:"fork_modified"`
	PredictedConflict bool               `json:"predicted_conflict"`
	ConflictKind      string             `json:"conflict_kind,omitempty"`
}

// Commit is one upstream commit that touched the boundary.
type Commit struct {
	SHA            string   `json:"sha"`
	Subject        string   `json:"subject"`
	Author         string   `json:"author"`
	Date           string   `json:"date"`
	BoundaryFiles  []string `json:"boundary_files"`
	HASignal       bool     `json:"ha_signal"`
	PullRequestRef string   `json:"pull_request,omitempty"`
}

// AuthoritySurfaceDelta records how upstream moved the high-availability
// vocabulary between the two ends of the range.
type AuthoritySurfaceDelta struct {
	NewHAFiles     []string `json:"new_ha_files"`
	RemovedHAFiles []string `json:"removed_ha_files"`
	Undeclared     []string `json:"undeclared"`
}

// ManifestStability records the ways the manifest has fallen out of step with
// upstream.
type ManifestStability struct {
	DeclaredButMissingUpstream []string `json:"declared_but_missing_upstream"`
	CollisionsWithOwned        []string `json:"collisions_with_cnpatroni_owned"`
}

// GeneratedArtifactDrift records whether the merge forces a code-generation
// step.
type GeneratedArtifactDrift struct {
	Touched              []string `json:"touched"`
	RegenerationRequired bool     `json:"regeneration_required"`
}

// Conflicts records the predicted textual conflicts of the merge.
type Conflicts struct {
	Predicted []string `json:"predicted"`
	Checked   bool     `json:"checked"`
}

// Gate is the mechanical decision the report reaches.
type Gate struct {
	RequiresAuthorityAudit bool   `json:"requires_authority_audit"`
	RequiresChaosGate      bool   `json:"requires_chaos_gate"`
	RequiresADRReview      bool   `json:"requires_adr_review"`
	RequiresRegeneration   bool   `json:"requires_regeneration"`
	Verdict                string `json:"verdict"`
	ExitCode               int    `json:"exit_code"`
}

// Report is the whole machine-readable divergence report.
type Report struct {
	Schema                 string                 `json:"schema"`
	GeneratedAt            string                 `json:"generated_at"`
	ToolVersion            string                 `json:"tool_version"`
	Repository             string                 `json:"repository"`
	Manifest               ManifestRef            `json:"manifest"`
	Range                  Range                  `json:"range"`
	Volume                 Volume                 `json:"volume"`
	Files                  []File                 `json:"files"`
	Commits                []Commit               `json:"commits"`
	AuthoritySurfaceDelta  AuthoritySurfaceDelta  `json:"authority_surface_delta"`
	ManifestStability      ManifestStability      `json:"manifest_stability"`
	GeneratedArtifactDrift GeneratedArtifactDrift `json:"generated_artifact_drift"`
	Conflicts              Conflicts              `json:"conflicts"`
	Gate                   Gate                   `json:"gate"`
}

// BoundaryFiles returns the changed files that the manifest does not classify
// as plain upstream content.
func (r *Report) BoundaryFiles() []File {
	files := make([]File, 0, len(r.Files))
	for _, f := range r.Files {
		if f.OnBoundary {
			files = append(files, f)
		}
	}

	return files
}
