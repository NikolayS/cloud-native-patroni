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
	"encoding/json"
	"fmt"
	"strings"
)

// JSON renders the machine-readable report with a stable key order, two-space
// indentation and a trailing newline.
func (r *Report) JSON() ([]byte, error) {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(body, '\n'), nil
}

// Markdown renders the human-readable report. Headings are sentence case.
func (r *Report) Markdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Upstream integration report — %s\n\n", r.Range.To.Date)
	fmt.Fprintf(&b, "**Verdict:** %s\n\n", r.Gate.Verdict)

	writeSummaryTable(&b, r)
	writeBoundaryFiles(&b, r)
	writeList(&b, "New high-availability vocabulary outside the manifest",
		r.AuthoritySurfaceDelta.Undeclared)
	writeList(&b, "Upstream paths colliding with CloudNativePatroni-owned paths",
		r.ManifestStability.CollisionsWithOwned)
	writeList(&b, "Declared paths upstream no longer carries",
		r.ManifestStability.DeclaredButMissingUpstream)
	writeList(&b, "Predicted textual conflicts", r.Conflicts.Predicted)
	writeRegeneration(&b, r)
	writeCommits(&b, r)

	return b.String()
}

func writeSummaryTable(b *strings.Builder, r *Report) {
	b.WriteString("| Field | Value |\n|---|---|\n")
	fmt.Fprintf(b, "| From | `%s` (%s, %s) |\n",
		short(r.Range.From.Commit), r.Range.From.Describe, r.Range.From.Date)
	fmt.Fprintf(b, "| To | `%s` (%s, %s) |\n",
		short(r.Range.To.Commit), r.Range.To.Describe, r.Range.To.Date)
	fmt.Fprintf(b, "| Fork base | `%s` (%s) |\n",
		short(r.Range.ForkBase.Commit), r.Range.ForkBase.Describe)
	fmt.Fprintf(b, "| Upstream commits in range | %d |\n", r.Volume.UpstreamCommits)
	fmt.Fprintf(b, "| Files changed upstream | %d (+%d / -%d) |\n",
		r.Volume.UpstreamFilesChanged, r.Volume.UpstreamInsertions, r.Volume.UpstreamDeletions)
	fmt.Fprintf(b, "| Of those, on the boundary | %d (+%d / -%d) |\n",
		r.Volume.BoundaryFilesChanged, r.Volume.BoundaryInsertions, r.Volume.BoundaryDeletions)
	fmt.Fprintf(b, "| Concentration ratio | %.3f |\n", r.Volume.ConcentrationRatio)
	fmt.Fprintf(b, "| Of those, carrying high-availability authority | %d (ratio %.3f) |\n",
		r.Volume.AuthorityFilesChanged, r.Volume.AuthorityConcentrationRatio)
	fmt.Fprintf(b, "| Predicted textual conflicts | %s |\n", conflictCell(r))
	fmt.Fprintf(b, "| Gates required | %s |\n", gatesCell(r))
	fmt.Fprintf(b, "| Manifest | `%s` (%d rules, provisional: %t) |\n",
		r.Manifest.Path, r.Manifest.RuleCount, r.Manifest.Provisional)
	b.WriteString("\n")
}

func conflictCell(r *Report) string {
	if !r.Conflicts.Checked {
		return "not checked"
	}

	return fmt.Sprintf("%d", len(r.Conflicts.Predicted))
}

func gatesCell(r *Report) string {
	var gates []string
	if r.Gate.RequiresAuthorityAudit {
		gates = append(gates, "authority-audit")
	}
	if r.Gate.RequiresChaosGate {
		gates = append(gates, "chaos")
	}
	if r.Gate.RequiresADRReview {
		gates = append(gates, "adr-review")
	}
	if r.Gate.RequiresRegeneration {
		gates = append(gates, "regenerate")
	}
	if len(gates) == 0 {
		return "none"
	}

	return strings.Join(gates, ", ")
}

func writeBoundaryFiles(b *strings.Builder, r *Report) {
	b.WriteString("## Boundary files touched\n\n")

	files := r.BoundaryFiles()
	if len(files) == 0 {
		b.WriteString("None.\n\n")

		return
	}

	b.WriteString("| Path | Ownership | Rule | Gate | +/- | Status | Conflict |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, f := range files {
		gate := strings.Join(f.Gate, ", ")
		if gate == "" {
			gate = "—"
		}
		conflict := "no"
		if f.PredictedConflict {
			conflict = "yes"
		}
		if f.ConflictKind != "" {
			conflict = f.ConflictKind
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | +%d/-%d | %s | %s |\n",
			f.Path, f.Ownership, f.RuleID, gate, f.Insertions, f.Deletions, f.UpstreamStatus, conflict)
	}
	b.WriteString("\n")
}

func writeList(b *strings.Builder, heading string, items []string) {
	fmt.Fprintf(b, "## %s\n\n", heading)
	if len(items) == 0 {
		b.WriteString("None.\n\n")

		return
	}
	for _, item := range items {
		fmt.Fprintf(b, "- `%s`\n", item)
	}
	b.WriteString("\n")
}

func writeRegeneration(b *strings.Builder, r *Report) {
	b.WriteString("## Regeneration required\n\n")
	if !r.GeneratedArtifactDrift.RegenerationRequired {
		b.WriteString("No.\n\n")

		return
	}
	b.WriteString("Upstream changed a generated-artifact source:\n\n")
	for _, path := range r.GeneratedArtifactDrift.Touched {
		fmt.Fprintf(b, "- `%s`\n", path)
	}
	b.WriteString("\nRun `make fmt vet generate manifests apidoc wordlist-ordered` " +
		"after the merge and commit the regenerated files separately.\n\n")
}

func writeCommits(b *strings.Builder, r *Report) {
	b.WriteString("## Commits touching the boundary\n\n")
	if len(r.Commits) == 0 {
		b.WriteString("None.\n")

		return
	}
	for _, c := range r.Commits {
		signal := ""
		if c.HASignal {
			signal = " — high-availability signal"
		}
		fmt.Fprintf(b, "- `%s` %s (%s, %s)%s\n", short(c.SHA), c.Subject, c.Author, c.Date, signal)
	}
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}

	return commit
}
