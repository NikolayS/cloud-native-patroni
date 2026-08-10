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

// Package boundary reads, matches and validates the CloudNativePatroni
// boundary manifest: the machine-readable declaration of which paths of this
// fork are taken from upstream CloudNativePG verbatim and which ones
// CloudNativePatroni adapts, disables, deletes or owns outright.
package boundary

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"

	"go.yaml.in/yaml/v3"
)

// Schema is the only manifest schema identifier this tool understands.
const Schema = "cnpatroni.io/boundary/v1"

// ClassificationState records whether the ownership classes describe the code
// as it is, or the end state the authority audit decided on.
type ClassificationState string

// The classification states.
const (
	// ClassificationApplied means the classes describe the code as it stands.
	ClassificationApplied ClassificationState = "applied"
	// ClassificationTarget means the classes record the intended end state.
	// Paths marked adapted or disabled may still hold upstream code verbatim.
	ClassificationTarget ClassificationState = "target"
)

// Ownership is the CloudNativePatroni ownership class of a path.
type Ownership string

// The ownership classes. Their merge consequences are documented in
// docs/cnpatroni/fork-maintenance.md.
const (
	// OwnershipUpstreamUntouched marks a path taken from upstream verbatim.
	OwnershipUpstreamUntouched Ownership = "upstream-untouched"
	// OwnershipAdapted marks a path that exists upstream and that
	// CloudNativePatroni edits in place.
	OwnershipAdapted Ownership = "adapted"
	// OwnershipCNPatroniOwned marks a path created by CloudNativePatroni.
	OwnershipCNPatroniOwned Ownership = "cnpatroni-owned"
	// OwnershipDisabled marks a path that is present but severed from the
	// live call graph.
	OwnershipDisabled Ownership = "disabled"
	// OwnershipDeleted marks a path removed in CloudNativePatroni.
	OwnershipDeleted Ownership = "deleted"
)

// NeedsReview reports whether an upstream change to a path of this class
// obliges a human to read the upstream hunks before absorbing them.
func (o Ownership) NeedsReview() bool {
	switch o {
	case OwnershipAdapted, OwnershipDisabled, OwnershipDeleted:
		return true
	case OwnershipUpstreamUntouched, OwnershipCNPatroniOwned:
		return false
	default:
		return false
	}
}

// Valid reports whether the ownership class is one this tool knows.
func (o Ownership) Valid() bool {
	switch o {
	case OwnershipUpstreamUntouched, OwnershipAdapted, OwnershipCNPatroniOwned,
		OwnershipDisabled, OwnershipDeleted:
		return true
	default:
		return false
	}
}

// State records whether a declared path is expected to exist yet.
type State string

// The declaration states.
const (
	// StatePresent means the path must exist in the worktree.
	StatePresent State = "present"
	// StatePlanned means CloudNativePatroni will create the path in a later
	// milestone, so its absence is not a violation.
	StatePlanned State = "planned"
)

// Mechanism records how a disabled path was severed.
type Mechanism string

// The disabling mechanisms.
const (
	// MechanismGuarded means the code is still reachable but returns early.
	MechanismGuarded Mechanism = "guarded"
	// MechanismUnreferenced means nothing calls the code any more.
	MechanismUnreferenced Mechanism = "unreferenced"
	// MechanismMovedAside means the file was moved out of the way.
	MechanismMovedAside Mechanism = "moved-aside"
)

// Scan restricts the HA-vocabulary term scan to a set of paths.
type Scan struct {
	Include []string `yaml:"include"`
	Exclude []string `yaml:"exclude"`
}

// Rule declares the ownership class of a set of paths.
type Rule struct {
	ID            string    `yaml:"id"`
	Ownership     Ownership `yaml:"ownership"`
	Mechanism     Mechanism `yaml:"mechanism,omitempty"`
	MovedTo       string    `yaml:"moved_to,omitempty"`
	State         State     `yaml:"state,omitempty"`
	Gate          []string  `yaml:"gate,omitempty"`
	AuditClass    string    `yaml:"audit_class,omitempty"`
	Destination   string    `yaml:"destination,omitempty"`
	SpecRefs      []string  `yaml:"spec_refs,omitempty"`
	AuditReviewed bool      `yaml:"audit_reviewed,omitempty"`
	Paths         []string  `yaml:"paths"`
	Note          string    `yaml:"note,omitempty"`

	globs []*Glob
}

// EffectiveState returns the declared state, defaulting to present.
func (r *Rule) EffectiveState() State {
	if r.State == "" {
		return StatePresent
	}

	return r.State
}

// IsCatchAll reports whether the rule is the trailing `**` rule that absorbs
// everything not declared elsewhere.
func (r *Rule) IsCatchAll() bool {
	return len(r.Paths) == 1 && r.Paths[0] == "**"
}

// Globs returns the rule's compiled patterns.
func (r *Rule) Globs() []*Glob { return r.globs }

// Manifest is a parsed boundary manifest.
type Manifest struct {
	Schema              string              `yaml:"schema"`
	GeneratedFrom       string              `yaml:"generated_from"`
	ClassificationState ClassificationState `yaml:"classification_state,omitempty"`
	Provisional         bool                `yaml:"provisional"`
	DefaultOwnership    string              `yaml:"default_ownership"`
	AuditScan           Scan                `yaml:"audit_scan"`
	AuditTerms          []string            `yaml:"audit_terms"`
	GeneratedArtifacts  []string            `yaml:"generated_artifacts"`
	RequiredPaths       []string            `yaml:"required_paths"`
	Rules               []Rule              `yaml:"rules"`

	path   string
	sha256 string
	order  []candidate
}

type candidate struct {
	glob  *Glob
	rule  *Rule
	index int
}

// Path returns the file the manifest was loaded from, if any.
func (m *Manifest) Path() string { return m.path }

// SHA256 returns the hex digest of the manifest bytes, so that a report can
// name the exact manifest that produced it.
func (m *Manifest) SHA256() string { return m.sha256 }

// Load reads and parses a manifest file.
func Load(path string) (*Manifest, error) {
	body, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied by design
	if err != nil {
		return nil, fmt.Errorf("cannot read the boundary manifest: %w", err)
	}

	m, err := Parse(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.path = path

	return m, nil
}

// Parse parses manifest bytes. Unknown fields are rejected so that a typo in a
// rule cannot silently downgrade a path to the catch-all class.
func Parse(body []byte) (*Manifest, error) {
	var m Manifest

	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("cannot parse the boundary manifest: %w", err)
	}

	digest := sha256.Sum256(body)
	m.sha256 = hex.EncodeToString(digest[:])

	m.compile()

	return &m, nil
}

// compile builds the ordered candidate list used by Match. Patterns that do not
// compile are reported by Validate, not here, so that `validate` can list every
// problem at once instead of stopping at the first one.
func (m *Manifest) compile() {
	m.order = nil
	for i := range m.Rules {
		rule := &m.Rules[i]
		rule.globs = nil
		for _, pattern := range rule.Paths {
			g, err := CompileGlob(pattern)
			if err != nil {
				continue
			}
			rule.globs = append(rule.globs, g)
			m.order = append(m.order, candidate{glob: g, rule: rule, index: len(m.order)})
		}
	}

	// Sort by decreasing specificity: literal patterns first, then the longest
	// literal prefix, then the longest pattern, then the latest declaration.
	sort.SliceStable(m.order, func(i, j int) bool {
		a, b := m.order[i], m.order[j]
		if a.glob.IsLiteral() != b.glob.IsLiteral() {
			return a.glob.IsLiteral()
		}
		if la, lb := len(a.glob.LiteralPrefix()), len(b.glob.LiteralPrefix()); la != lb {
			return la > lb
		}
		if la, lb := len(a.glob.Pattern()), len(b.glob.Pattern()); la != lb {
			return la > lb
		}

		return a.index > b.index
	})
}

// Match returns the rule that classifies a repository-relative path, or nil
// when no rule matches it at all.
func (m *Manifest) Match(path string) *Rule {
	for _, c := range m.order {
		if c.glob.Match(path) {
			return c.rule
		}
	}

	return nil
}

// MatchGlob returns the pattern that classified a path, for reporting.
func (m *Manifest) MatchGlob(path string) string {
	for _, c := range m.order {
		if c.glob.Match(path) {
			return c.glob.Pattern()
		}
	}

	return ""
}

// EffectiveClassificationState defaults to applied when the field is absent.
func (m *Manifest) EffectiveClassificationState() ClassificationState {
	if m.ClassificationState == "" {
		return ClassificationApplied
	}

	return m.ClassificationState
}

// IsGeneratedArtifact reports whether a path is regenerated by the repository's
// code-generation targets, so that touching it forces a regeneration step.
func (m *Manifest) IsGeneratedArtifact(path string) bool {
	for _, pattern := range m.GeneratedArtifacts {
		g, err := CompileGlob(pattern)
		if err != nil {
			continue
		}
		if g.Match(path) {
			return true
		}
	}

	return false
}
