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

package boundary_test

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/postgres-ai/cnpatroni-upstream/internal/boundary"
)

func ratchetManifest(rules ...boundary.Rule) *boundary.Manifest {
	return &boundary.Manifest{Rules: rules}
}

func ratchetRule(id string, ownership boundary.Ownership, paths ...string) boundary.Rule {
	return boundary.Rule{ID: id, Ownership: ownership, Paths: paths}
}

func findingText(findings []boundary.RatchetFinding) string {
	parts := make([]string, 0, len(findings))
	for _, finding := range findings {
		parts = append(parts, finding.String())
	}

	return strings.Join(parts, "\n")
}

func TestRatchetAcceptsIdenticalManifests(t *testing.T) {
	base := ratchetManifest(
		ratchetRule("owned.patroni-poc", boundary.OwnershipCNPatroniOwned, "poc/**"),
		ratchetRule("disable.election-engine", boundary.OwnershipDisabled, "internal/controller/primary_lease.go"),
	)

	result := boundary.Ratchet(base, base)
	if len(result.Findings) != 0 || len(result.AppliedAllowances) != 0 || len(result.StaleAllowances) != 0 {
		t.Fatalf("identity result = %#v, want no findings or allowances", result)
	}
	if result.ProtectedRules != 2 {
		t.Fatalf("protected rules = %d, want 2", result.ProtectedRules)
	}
}

func TestRatchetReportsReclassification(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.patroni-poc", boundary.OwnershipCNPatroniOwned, "poc/**"))
	current := ratchetManifest(ratchetRule("owned.patroni-poc", boundary.OwnershipAdapted, "poc/**"))

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 1 || result.Findings[0].Kind != boundary.RatchetReclassification {
		t.Fatalf("findings = %#v, want one reclassification", result.Findings)
	}
	for _, want := range []string{"owned.patroni-poc", "cnpatroni-owned", "adapted"} {
		if !strings.Contains(result.Findings[0].String(), want) {
			t.Errorf("finding should name %q: %s", want, result.Findings[0])
		}
	}
}

func TestRatchetReportsRemovedOwnedRule(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.project-metadata", boundary.OwnershipCNPatroniOwned,
		"CLAUDE.md", "NOTICE"))

	result := boundary.Ratchet(base, ratchetManifest())
	if len(result.Findings) != 1 || result.Findings[0].Kind != boundary.RatchetRemoval {
		t.Fatalf("findings = %#v, want one removal", result.Findings)
	}
	for _, want := range []string{"owned.project-metadata", "removed or renamed without preserving its paths", "CLAUDE.md", "NOTICE"} {
		if !strings.Contains(result.Findings[0].String(), want) {
			t.Errorf("finding should name %q: %s", want, result.Findings[0])
		}
	}
}

func TestRatchetReportsEveryDroppedPatroniPackagePattern(t *testing.T) {
	paths := []string{
		"internal/patroni/**",
		"internal/controller/patroni/**",
		"cmd/cnpatroni-agent/**",
		"pkg/patroni/**",
		"tests/chaos/**",
		"brand/**",
	}
	base := ratchetManifest(ratchetRule("owned.patroni-packages", boundary.OwnershipCNPatroniOwned, paths...))
	current := ratchetManifest(ratchetRule("owned.patroni-packages", boundary.OwnershipCNPatroniOwned,
		"internal/patroni/**"))

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 5 {
		t.Fatalf("findings = %#v, want five narrowings", result.Findings)
	}
	got := make([]string, 0, len(result.Findings))
	for _, finding := range result.Findings {
		if finding.Kind != boundary.RatchetNarrowing {
			t.Errorf("kind = %q, want narrowing", finding.Kind)
		}
		if !strings.Contains(finding.String(), finding.Pattern) {
			t.Errorf("finding should name its pattern: %#v", finding)
		}
		got = append(got, finding.Pattern)
	}
	want := append([]string(nil), paths[1:]...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dropped patterns = %v, want %v", got, want)
	}
}

func TestRatchetCatchesSingleFileNarrowing(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/**"))
	current := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/guard.go"))

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 1 || result.Findings[0].Pattern != "internal/cnpatroni/guard/**" {
		t.Fatalf("findings = %#v, want the dropped directory pattern", result.Findings)
	}
}

func TestRatchetAppliesAnExactAllowance(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/**"))
	current := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/guard.go"))
	current.OwnershipRatchetAllow = []boundary.RatchetAllowance{{
		Rule: "owned.runtime-guard", Path: "internal/cnpatroni/guard/**", Reason: "the package moved to a narrower stable surface",
	}}

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 0 || len(result.AppliedAllowances) != 1 || len(result.StaleAllowances) != 0 {
		t.Fatalf("result = %#v, want one applied allowance", result)
	}
	if got := result.AppliedAllowances[0].Reason; got != "the package moved to a narrower stable surface" {
		t.Errorf("reason = %q", got)
	}
}

func TestRatchetRequiresAnExactAllowancePath(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/**"))
	current := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/guard.go"))
	current.OwnershipRatchetAllow = []boundary.RatchetAllowance{{
		Rule: "owned.runtime-guard", Path: "internal/cnpatroni/**", Reason: "intentionally different",
	}}

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 1 || len(result.AppliedAllowances) != 0 || len(result.StaleAllowances) != 1 {
		t.Fatalf("result = %#v, want a finding and one stale allowance", result)
	}
}

func TestRatchetReportsAnAllowanceForAnUnchangedRuleAsStale(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/**"))
	current := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/**"))
	current.OwnershipRatchetAllow = []boundary.RatchetAllowance{{
		Rule: "owned.runtime-guard", Path: "internal/cnpatroni/guard/**", Reason: "already merged",
	}}

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 0 || len(result.StaleAllowances) != 1 {
		t.Fatalf("result = %#v, want one non-failing stale allowance", result)
	}
}

func TestRatchetRejectsMalformedAllowances(t *testing.T) {
	for _, tc := range []struct {
		name      string
		allowance boundary.RatchetAllowance
		missing   string
	}{
		{name: "reason", allowance: boundary.RatchetAllowance{Rule: "owned.runtime-guard", Path: "guard/**"}, missing: "reason"},
		{name: "path", allowance: boundary.RatchetAllowance{Rule: "owned.runtime-guard", Reason: "surface removed"}, missing: "path"},
		{name: "rule", allowance: boundary.RatchetAllowance{Path: "guard/**", Reason: "surface removed"}, missing: "rule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := ratchetManifest()
			current.OwnershipRatchetAllow = []boundary.RatchetAllowance{tc.allowance}

			result := boundary.Ratchet(ratchetManifest(), current)
			if len(result.Findings) != 1 || result.Findings[0].Kind != boundary.RatchetInvalidAllowance {
				t.Fatalf("findings = %#v, want one malformed-allowance finding", result.Findings)
			}
			if !strings.Contains(result.Findings[0].String(), tc.missing) {
				t.Errorf("finding should name missing %s: %s", tc.missing, result.Findings[0])
			}
			if len(result.StaleAllowances) != 0 {
				t.Errorf("a malformed allowance must not also be stale: %#v", result.StaleAllowances)
			}
		})
	}
}

func TestRatchetAcceptsARenameWithCoveragePreserved(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/**"))
	current := ratchetManifest(ratchetRule("owned.lifecycle-guard", boundary.OwnershipCNPatroniOwned,
		"internal/cnpatroni/guard/**"))

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 0 || len(result.Renames) != 1 {
		t.Fatalf("result = %#v, want one rename notice", result)
	}
	if result.Renames[0].OldRule != "owned.runtime-guard" || result.Renames[0].NewRule != "owned.lifecycle-guard" {
		t.Errorf("rename = %#v", result.Renames[0])
	}
}

func TestRatchetRejectsARenameThatDropsAPattern(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.project-metadata", boundary.OwnershipCNPatroniOwned,
		"CLAUDE.md", "NOTICE"))
	current := ratchetManifest(ratchetRule("owned.fork-notice", boundary.OwnershipCNPatroniOwned, "NOTICE"))

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 1 || result.Findings[0].Kind != boundary.RatchetRemoval {
		t.Fatalf("findings = %#v, want one removal", result.Findings)
	}
	if !reflect.DeepEqual(result.Findings[0].MissingPatterns, []string{"CLAUDE.md"}) {
		t.Errorf("missing patterns = %v, want CLAUDE.md", result.Findings[0].MissingPatterns)
	}
}

func TestRatchetAllowsGrowth(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned, "guard/**"))
	current := ratchetManifest(
		ratchetRule("owned.runtime-guard", boundary.OwnershipCNPatroniOwned, "guard/**", "guard2/**"),
		ratchetRule("owned.new-surface", boundary.OwnershipCNPatroniOwned, "new/**"),
	)

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 0 {
		t.Fatalf("growth produced findings: %#v", result.Findings)
	}
}

func TestRatchetProtectsDisabledRules(t *testing.T) {
	base := ratchetManifest(ratchetRule("disable.election-engine", boundary.OwnershipDisabled,
		"internal/controller/primary_lease.go"))

	result := boundary.Ratchet(base, ratchetManifest())
	if len(result.Findings) != 1 || result.Findings[0].Kind != boundary.RatchetRemoval {
		t.Fatalf("findings = %#v, want one disabled-rule removal", result.Findings)
	}
}

func TestRatchetIgnoresUnprotectedBaseRules(t *testing.T) {
	for _, ownership := range []boundary.Ownership{
		boundary.OwnershipAdapted,
		boundary.OwnershipDeleted,
		boundary.OwnershipUpstreamUntouched,
	} {
		t.Run(string(ownership), func(t *testing.T) {
			base := ratchetManifest(ratchetRule("outside.ratchet", ownership, "old/**"))
			result := boundary.Ratchet(base, ratchetManifest())
			if len(result.Findings) != 0 || result.ProtectedRules != 0 {
				t.Fatalf("result = %#v, want unprotected rule ignored", result)
			}
		})
	}
}

func TestRatchetAllowsAnAcknowledgedDisabledPathNarrowing(t *testing.T) {
	base := ratchetManifest(ratchetRule("disable.election-engine", boundary.OwnershipDisabled,
		"internal/controller/primary_lease.go", "internal/controller/replicas.go"))
	current := ratchetManifest(ratchetRule("disable.election-engine", boundary.OwnershipDisabled,
		"internal/controller/replicas.go"))
	current.OwnershipRatchetAllow = []boundary.RatchetAllowance{{
		Rule: "disable.election-engine", Path: "internal/controller/primary_lease.go", Reason: "the controller was removed",
	}}

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 0 || len(result.AppliedAllowances) != 1 {
		t.Fatalf("result = %#v, want one applied disabled allowance", result)
	}
}

func TestRatchetTreatsBroaderReplacementGlobAsLiteralNarrowing(t *testing.T) {
	base := ratchetManifest(ratchetRule("owned.patroni-poc", boundary.OwnershipCNPatroniOwned,
		"poc/oracle/**", "poc/chaos/**"))
	current := ratchetManifest(ratchetRule("owned.patroni-poc", boundary.OwnershipCNPatroniOwned, "poc/**"))

	result := boundary.Ratchet(base, current)
	if len(result.Findings) != 2 {
		t.Fatalf("findings = %#v, want both literal patterns reported", result.Findings)
	}
}

func TestRatchetSortsFindingsByRuleThenPattern(t *testing.T) {
	base := ratchetManifest(
		ratchetRule("owned.z", boundary.OwnershipCNPatroniOwned, "z/two", "z/one"),
		ratchetRule("owned.a", boundary.OwnershipCNPatroniOwned, "a/two", "a/one"),
	)
	current := ratchetManifest(
		ratchetRule("owned.z", boundary.OwnershipCNPatroniOwned),
		ratchetRule("owned.a", boundary.OwnershipCNPatroniOwned),
	)

	result := boundary.Ratchet(base, current)
	got := make([]string, 0, len(result.Findings))
	for _, finding := range result.Findings {
		got = append(got, fmt.Sprintf("%s:%s", finding.Rule, finding.Pattern))
	}
	want := []string{"owned.a:a/one", "owned.a:a/two", "owned.z:z/one", "owned.z:z/two"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("finding order = %v, want %v", got, want)
	}
}

func TestParseAcceptsOwnershipRatchetAllow(t *testing.T) {
	m, err := boundary.Parse([]byte(`schema: cnpatroni.io/boundary/v1
generated_from: fixture
provisional: false
default_ownership: upstream-untouched
audit_scan: {include: [], exclude: []}
audit_terms: []
generated_artifacts: []
required_paths: []
ownership_ratchet_allow:
  - rule: owned.runtime-guard
    path: internal/cnpatroni/guard/**
    reason: the package was intentionally narrowed
rules: []
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.OwnershipRatchetAllow) != 1 || m.OwnershipRatchetAllow[0].Reason == "" {
		t.Fatalf("allowances = %#v", m.OwnershipRatchetAllow)
	}
}
