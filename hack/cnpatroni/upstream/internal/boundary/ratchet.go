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
	"sort"
	"strings"
)

// RatchetFindingKind identifies an ownership ratchet violation.
type RatchetFindingKind string

// Ownership ratchet finding kinds.
const (
	// RatchetReclassification reports a protected rule whose class changed.
	RatchetReclassification RatchetFindingKind = "reclassification"
	// RatchetNarrowing reports one literal path pattern dropped from a rule.
	RatchetNarrowing RatchetFindingKind = "narrowing"
	// RatchetRemoval reports a protected rule removed or renamed without
	// preserving its complete path set under the same ownership class.
	RatchetRemoval RatchetFindingKind = "removal"
	// RatchetInvalidAllowance reports an allowance missing a mandatory field.
	RatchetInvalidAllowance RatchetFindingKind = "invalid-allowance"
)

// RatchetFinding describes one ownership ratchet violation. Findings are
// sorted deterministically by rule id and then literal path pattern.
type RatchetFinding struct {
	Kind               RatchetFindingKind
	Rule               string
	Pattern            string
	RecordedOwnership  Ownership
	CurrentOwnership   Ownership
	MissingPatterns    []string
	MissingAllowFields []string
}

// String renders a ratchet finding for the command-line interface.
func (f RatchetFinding) String() string {
	switch f.Kind {
	case RatchetReclassification:
		return fmt.Sprintf("rule %s was recorded as %s but is now %s (reclassification)",
			f.Rule, f.RecordedOwnership, f.CurrentOwnership)
	case RatchetNarrowing:
		return fmt.Sprintf("rule %s dropped path pattern %q (ownership narrowing)", f.Rule, f.Pattern)
	case RatchetRemoval:
		return fmt.Sprintf(
			"rule %s was removed or renamed without preserving its paths; patterns no longer present under any %s rule: %s",
			f.Rule, f.RecordedOwnership, quotedPatterns(f.MissingPatterns))
	case RatchetInvalidAllowance:
		return fmt.Sprintf(
			"ownership_ratchet_allow entry for rule %q and path %q is invalid; missing mandatory fields: %s",
			f.Rule, f.Pattern, strings.Join(f.MissingAllowFields, ", "))
	default:
		return fmt.Sprintf("rule %s has an unknown ownership ratchet finding for path %q", f.Rule, f.Pattern)
	}
}

// RatchetRename records a protected rule rename that preserved its ownership
// class and every literal path pattern.
type RatchetRename struct {
	OldRule string
	NewRule string
}

// RatchetResult is the complete result of an ownership ratchet comparison.
type RatchetResult struct {
	Findings          []RatchetFinding
	Renames           []RatchetRename
	AppliedAllowances []RatchetAllowance
	StaleAllowances   []RatchetAllowance
	ProtectedRules    int
}

// Ratchet compares a base manifest against the current one and returns the
// findings, the applied allowances and the stale allowances.
func Ratchet(base, current *Manifest) RatchetResult {
	var result RatchetResult
	if current == nil {
		current = &Manifest{}
	}

	validAllowances := make([]bool, len(current.OwnershipRatchetAllow))
	usedAllowances := make([]bool, len(current.OwnershipRatchetAllow))
	for i, allowance := range current.OwnershipRatchetAllow {
		missing := missingAllowanceFields(allowance)
		if len(missing) == 0 {
			validAllowances[i] = true
			continue
		}
		result.Findings = append(result.Findings, RatchetFinding{
			Kind:               RatchetInvalidAllowance,
			Rule:               allowance.Rule,
			Pattern:            allowance.Path,
			MissingAllowFields: missing,
		})
	}

	currentByID := make(map[string]*Rule, len(current.Rules))
	for i := range current.Rules {
		rule := &current.Rules[i]
		if _, exists := currentByID[rule.ID]; !exists {
			currentByID[rule.ID] = rule
		}
	}

	if base != nil {
		for i := range base.Rules {
			baseRule := &base.Rules[i]
			if !ratchetProtected(baseRule.Ownership) {
				continue
			}
			result.ProtectedRules++

			if currentRule := currentByID[baseRule.ID]; currentRule != nil {
				compareSameRule(baseRule, currentRule, current.OwnershipRatchetAllow,
					validAllowances, usedAllowances, &result)
				continue
			}

			if renamedTo := preservedByRename(baseRule, current.Rules); renamedTo != "" {
				result.Renames = append(result.Renames, RatchetRename{OldRule: baseRule.ID, NewRule: renamedTo})
				continue
			}

			missing := missingUnderClass(baseRule, current.Rules)
			pattern := ""
			if len(missing) > 0 {
				pattern = missing[0]
			}
			result.Findings = append(result.Findings, RatchetFinding{
				Kind:              RatchetRemoval,
				Rule:              baseRule.ID,
				Pattern:           pattern,
				RecordedOwnership: baseRule.Ownership,
				MissingPatterns:   missing,
			})
		}
	}

	for i, allowance := range current.OwnershipRatchetAllow {
		if !validAllowances[i] {
			continue
		}
		if usedAllowances[i] {
			result.AppliedAllowances = append(result.AppliedAllowances, allowance)
		} else {
			result.StaleAllowances = append(result.StaleAllowances, allowance)
		}
	}

	sortRatchetResult(&result)

	return result
}

func compareSameRule(
	baseRule, currentRule *Rule,
	allowances []RatchetAllowance,
	validAllowances, usedAllowances []bool,
	result *RatchetResult,
) {
	if currentRule.Ownership != baseRule.Ownership {
		result.Findings = append(result.Findings, RatchetFinding{
			Kind:              RatchetReclassification,
			Rule:              baseRule.ID,
			RecordedOwnership: baseRule.Ownership,
			CurrentOwnership:  currentRule.Ownership,
		})
	}

	currentPaths := patternSet(currentRule.Paths)
	for _, pattern := range baseRule.Paths {
		if currentPaths[pattern] {
			continue
		}
		if applyAllowance(baseRule.ID, pattern, allowances, validAllowances, usedAllowances) {
			continue
		}
		result.Findings = append(result.Findings, RatchetFinding{
			Kind:              RatchetNarrowing,
			Rule:              baseRule.ID,
			Pattern:           pattern,
			RecordedOwnership: baseRule.Ownership,
		})
	}
}

func applyAllowance(
	rule, pattern string,
	allowances []RatchetAllowance,
	validAllowances, usedAllowances []bool,
) bool {
	for i, allowance := range allowances {
		if validAllowances[i] && !usedAllowances[i] && allowance.Rule == rule && allowance.Path == pattern {
			usedAllowances[i] = true

			return true
		}
	}

	return false
}

func preservedByRename(baseRule *Rule, currentRules []Rule) string {
	var candidates []string
	for i := range currentRules {
		currentRule := &currentRules[i]
		if currentRule.Ownership == baseRule.Ownership && containsPatterns(currentRule.Paths, baseRule.Paths) {
			candidates = append(candidates, currentRule.ID)
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	sort.Strings(candidates)

	return candidates[0]
}

func missingUnderClass(baseRule *Rule, currentRules []Rule) []string {
	present := make(map[string]bool)
	for i := range currentRules {
		currentRule := &currentRules[i]
		if currentRule.Ownership != baseRule.Ownership {
			continue
		}
		for _, pattern := range currentRule.Paths {
			present[pattern] = true
		}
	}

	missing := make([]string, 0, len(baseRule.Paths))
	for _, pattern := range baseRule.Paths {
		if !present[pattern] {
			missing = append(missing, pattern)
		}
	}
	sort.Strings(missing)

	return missing
}

func missingAllowanceFields(allowance RatchetAllowance) []string {
	var missing []string
	if allowance.Rule == "" {
		missing = append(missing, "rule")
	}
	if allowance.Path == "" {
		missing = append(missing, "path")
	}
	if allowance.Reason == "" {
		missing = append(missing, "reason")
	}

	return missing
}

func ratchetProtected(ownership Ownership) bool {
	return ownership == OwnershipCNPatroniOwned || ownership == OwnershipDisabled
}

func containsPatterns(haystack, needles []string) bool {
	patterns := patternSet(haystack)
	for _, pattern := range needles {
		if !patterns[pattern] {
			return false
		}
	}

	return true
}

func patternSet(patterns []string) map[string]bool {
	set := make(map[string]bool, len(patterns))
	for _, pattern := range patterns {
		set[pattern] = true
	}

	return set
}

func quotedPatterns(patterns []string) string {
	quoted := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		quoted = append(quoted, fmt.Sprintf("%q", pattern))
	}

	return strings.Join(quoted, ", ")
}

func sortRatchetResult(result *RatchetResult) {
	sort.SliceStable(result.Findings, func(i, j int) bool {
		left, right := result.Findings[i], result.Findings[j]
		if left.Rule != right.Rule {
			return left.Rule < right.Rule
		}
		if left.Pattern != right.Pattern {
			return left.Pattern < right.Pattern
		}

		return left.Kind < right.Kind
	})
	sort.SliceStable(result.Renames, func(i, j int) bool {
		if result.Renames[i].OldRule != result.Renames[j].OldRule {
			return result.Renames[i].OldRule < result.Renames[j].OldRule
		}

		return result.Renames[i].NewRule < result.Renames[j].NewRule
	})
	sortAllowances(result.AppliedAllowances)
	sortAllowances(result.StaleAllowances)
}

func sortAllowances(allowances []RatchetAllowance) {
	sort.SliceStable(allowances, func(i, j int) bool {
		if allowances[i].Rule != allowances[j].Rule {
			return allowances[i].Rule < allowances[j].Rule
		}
		if allowances[i].Path != allowances[j].Path {
			return allowances[i].Path < allowances[j].Path
		}

		return allowances[i].Reason < allowances[j].Reason
	})
}
