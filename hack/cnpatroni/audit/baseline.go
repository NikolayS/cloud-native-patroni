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

package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const baselineSchema = "cnpatroni-authority-baseline/v2"

// Baseline is the ratchet. On day one this repository is unmodified
// CloudNativePG and is full of forbidden calls, so a gate that fails on any of
// them is red from the first commit and is switched off within a week. The
// baseline records how many exist today and lets the count fall but never rise.
//
// Buckets are keyed by (rule, enclosing symbol) rather than by file and line.
// Line numbers churn on every upstream merge, and a file-keyed baseline would
// let a merge add a brand new forbidden call inside an already-listed file
// without being noticed. A new function is always a new bucket.
type Baseline struct {
	Schema        string         `yaml:"schema"`
	GeneratedFrom string         `yaml:"generated_from"`
	GeneratedAt   string         `yaml:"generated_at"`
	Total         int            `yaml:"total"`
	AllowedTotal  int            `yaml:"allowed_total"`
	TotalsByRule  map[string]int `yaml:"totals_by_rule"`
	Inputs        Inputs         `yaml:"inputs"`
	Buckets       []Bucket       `yaml:"buckets"`
}

// Inputs is what the totals were measured with. A count without its measurement
// is not a ratchet: deleting a rule, narrowing a matcher or excluding a path all
// lower the total, and without this block the collapse reads as an improvement.
type Inputs struct {
	Scope    ScopeFingerprint  `yaml:"scope"`
	Rules    []RuleFingerprint `yaml:"rules"`
	Packages int               `yaml:"packages"`
	Files    int               `yaml:"files"`
}

type ScopeFingerprint struct {
	Roots            []string `yaml:"roots"`
	ExcludePaths     []string `yaml:"exclude_paths"`
	ExcludeGenerated bool     `yaml:"exclude_generated"`
	IncludeTests     bool     `yaml:"include_tests"`
}

type RuleFingerprint struct {
	ID       string   `yaml:"id"`
	Severity string   `yaml:"severity"`
	Matchers []string `yaml:"matchers"`
}

// Fingerprint records the inputs used for a scan in a deterministic form.
func Fingerprint(rs *RuleSet, res *ScanResult) Inputs {
	scope := ScopeFingerprint{
		Roots:            sortedUnique(rs.Scope.Roots),
		ExcludePaths:     sortedUnique(rs.Scope.ExcludePaths),
		ExcludeGenerated: rs.Scope.ExcludeGenerated,
		IncludeTests:     rs.Scope.IncludeTests,
	}

	rules := make([]RuleFingerprint, 0, len(rs.Rules))
	for _, rule := range rs.Rules {
		matchers := make([]string, 0,
			len(rule.Calls)+len(rule.Literals)+len(rule.Imports)+len(rule.FieldWrites)+len(rule.FieldReads))
		callArgs := sortedUnique(rule.CallArgContains)
		for _, matcher := range rule.Calls {
			identity := "call:" + matcher
			if len(callArgs) > 0 {
				identity += "#arg=" + strings.Join(callArgs, ",")
			}
			matchers = append(matchers, identity)
		}
		for _, matcher := range rule.Literals {
			matchers = append(matchers, "literal:"+matcher)
		}
		for _, matcher := range rule.Imports {
			matchers = append(matchers, "import:"+matcher)
		}
		for _, matcher := range rule.FieldWrites {
			matchers = append(matchers, "field_write:"+matcher)
		}
		for _, matcher := range rule.FieldReads {
			matchers = append(matchers, "field_read:"+matcher)
		}
		rules = append(rules, RuleFingerprint{
			ID: rule.ID, Severity: rule.Severity, Matchers: sortedUnique(matchers),
		})
	}
	slices.SortFunc(rules, func(a, b RuleFingerprint) int { return strings.Compare(a.ID, b.ID) })

	return Inputs{
		Scope: scope, Rules: rules, Packages: res.Packages, Files: res.Files,
	}
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := slices.Clone(values)
	slices.Sort(out)
	return slices.Compact(out)
}

// Bucket is the number of hits of one rule inside one symbol.
type Bucket struct {
	Rule   string `yaml:"rule"`
	Symbol string `yaml:"symbol"`
	Count  int    `yaml:"count"`
}

// BuildBaseline counts the forbidden findings. Inventory and observe findings
// are deliberately excluded: they are triage material, not debt to burn down.
func BuildBaseline(rules *RuleSet, res *ScanResult, findings []Finding, allowedTotal int,
	commit, generatedAt string,
) *Baseline {
	counts := map[Bucket]int{}
	byRule := map[string]int{}
	total := 0

	for _, f := range findings {
		if f.Severity != SeverityForbidden {
			continue
		}
		counts[Bucket{Rule: f.Rule, Symbol: f.Symbol}]++
		byRule[f.Rule]++
		total++
	}

	buckets := make([]Bucket, 0, len(counts))
	for b, n := range counts {
		b.Count = n
		buckets = append(buckets, b)
	}
	slices.SortFunc(buckets, func(a, b Bucket) int {
		return compareAll(strings.Compare(a.Rule, b.Rule), strings.Compare(a.Symbol, b.Symbol))
	})

	return &Baseline{
		Schema:        baselineSchema,
		GeneratedFrom: commit,
		GeneratedAt:   generatedAt,
		Total:         total,
		AllowedTotal:  allowedTotal,
		TotalsByRule:  byRule,
		Inputs:        Fingerprint(rules, res),
		Buckets:       buckets,
	}
}

// LoadBaseline reads a recorded baseline.
func LoadBaseline(path string) (*Baseline, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied configuration
	if err != nil {
		return nil, fmt.Errorf("reading baseline: %w", err)
	}

	var b Baseline
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&b); err != nil {
		return nil, fmt.Errorf("parsing baseline yaml: %w", err)
	}
	if b.Schema != baselineSchema {
		return nil, fmt.Errorf("baseline schema is %q, want %q", b.Schema, baselineSchema)
	}
	if b.TotalsByRule == nil {
		b.TotalsByRule = map[string]int{}
	}
	if err := b.validate(); err != nil {
		return nil, err
	}

	return &b, nil
}

func (b *Baseline) validate() error {
	seen := map[Bucket]bool{}
	byRule := map[string]int{}
	total := 0
	for i, bucket := range b.Buckets {
		switch {
		case bucket.Rule == "":
			return fmt.Errorf("baseline bucket %d has no rule", i)
		case bucket.Symbol == "":
			return fmt.Errorf("baseline bucket %d has no symbol", i)
		case bucket.Count <= 0:
			return fmt.Errorf("baseline bucket %s in %s count must be positive, got %d",
				bucket.Rule, bucket.Symbol, bucket.Count)
		}

		key := Bucket{Rule: bucket.Rule, Symbol: bucket.Symbol}
		if seen[key] {
			return fmt.Errorf("duplicate bucket for %s in %s", bucket.Rule, bucket.Symbol)
		}
		seen[key] = true
		byRule[bucket.Rule] += bucket.Count
		total += bucket.Count
	}

	if b.Total != total {
		return fmt.Errorf("baseline total is %d, but buckets sum to %d", b.Total, total)
	}
	rules := map[string]bool{}
	for rule := range byRule {
		rules[rule] = true
	}
	for rule := range b.TotalsByRule {
		rules[rule] = true
	}
	names := make([]string, 0, len(rules))
	for rule := range rules {
		names = append(names, rule)
	}
	slices.Sort(names)
	for _, rule := range names {
		if b.TotalsByRule[rule] != byRule[rule] {
			return fmt.Errorf("baseline totals_by_rule for %s is %d, but buckets sum to %d",
				rule, b.TotalsByRule[rule], byRule[rule])
		}
	}

	inputRules := make(map[string]bool, len(b.Inputs.Rules))
	for _, rule := range b.Inputs.Rules {
		inputRules[rule.ID] = true
	}
	for rule := range b.TotalsByRule {
		if !inputRules[rule] {
			return fmt.Errorf("baseline totals_by_rule names rule %s, which is absent from inputs.rules", rule)
		}
	}

	return nil
}

// Write renders the baseline.
func (b *Baseline) Write(path string) error {
	out, err := yaml.Marshal(b)
	if err != nil {
		return fmt.Errorf("rendering baseline: %w", err)
	}
	// This header is inside the blob that the authority-audit workflow pins
	// during bootstrap, so prose that changes with unrelated work does not
	// belong here. Direct readers to the generator instead.
	header := "# Generated by hack/cnpatroni/audit; run `go run . baseline --write`.\n" +
		"# The totals may fall and must never rise. Run `go run . doc` for the audit document.\n"
	if err := os.WriteFile(path, append([]byte(header), out...), 0o600); err != nil {
		return fmt.Errorf("writing baseline: %w", err)
	}
	return nil
}

func (b *Baseline) index() map[Bucket]int {
	out := make(map[Bucket]int, len(b.Buckets))
	for _, bucket := range b.Buckets {
		out[Bucket{Rule: bucket.Rule, Symbol: bucket.Symbol}] = bucket.Count
	}
	return out
}

// CompareBaselines is the second layer of the ratchet. The per-bucket check
// catches an author who added a violation; this catches the author who
// regenerated the baseline to make the check green, because the regenerated file
// is compared against the one on the merge base.
//
// Weakening the rule set or scan scope has no in-band approval. A genuine
// narrowing must change this audit tool itself, where a human can review why the
// ratchet should cover less code. Tightening is attributable, with a residual
// risk: an author could add a bogus matcher to rule R and hide a new real hit of
// R under the attribution. checkClassificationCoverage still requires every new
// hit to have a reviewed classification, and the rules change is visible in the
// policy diff, so every attribution tells the reviewer to read the rules diff.
func CompareBaselines(base, head *Baseline) (grew bool, lines []string) {
	delta := head.Total - base.Total
	lines = append(lines, fmt.Sprintf("authority baseline: %d -> %d (%+d)", base.Total, head.Total, delta))

	baseRules := indexRuleFingerprints(base.Inputs.Rules)
	headRules := indexRuleFingerprints(head.Inputs.Rules)
	strengthened := map[string]bool{}
	weakened := false

	baseRuleNames := sortedKeys(baseRules)
	for _, id := range baseRuleNames {
		before := baseRules[id]
		after, present := headRules[id]
		if !present {
			lines = append(lines, fmt.Sprintf("  rule %s was removed from the rule set", id))
			grew = true
			weakened = true
			continue
		}
		if severityRank(after.Severity) < severityRank(before.Severity) {
			lines = append(lines, fmt.Sprintf("  rule %s severity %s -> %s", id, before.Severity, after.Severity))
			grew = true
			weakened = true
		}
		if severityRank(after.Severity) > severityRank(before.Severity) {
			strengthened[id] = true
		}

		beforeMatchers := stringSet(before.Matchers)
		afterMatchers := stringSet(after.Matchers)
		for _, matcher := range sortedSetDifference(beforeMatchers, afterMatchers) {
			lines = append(lines, fmt.Sprintf("  rule %s no longer matches %s", id, matcher))
			grew = true
			weakened = true
		}
		if strictSetGrowth(beforeMatchers, afterMatchers) {
			strengthened[id] = true
		}
	}
	for _, id := range sortedKeys(headRules) {
		if _, present := baseRules[id]; !present {
			strengthened[id] = true
		}
	}

	baseRoots := stringSet(base.Inputs.Scope.Roots)
	headRoots := stringSet(head.Inputs.Scope.Roots)
	for _, root := range sortedSetDifference(baseRoots, headRoots) {
		lines = append(lines, fmt.Sprintf("  scan root %s was removed", root))
		grew = true
		weakened = true
	}
	baseExcludes := stringSet(base.Inputs.Scope.ExcludePaths)
	headExcludes := stringSet(head.Inputs.Scope.ExcludePaths)
	for _, exclude := range sortedSetDifference(headExcludes, baseExcludes) {
		lines = append(lines, fmt.Sprintf("  exclude path %s was added", exclude))
		grew = true
		weakened = true
	}
	if !base.Inputs.Scope.ExcludeGenerated && head.Inputs.Scope.ExcludeGenerated {
		lines = append(lines, "  generated files are now excluded")
		grew = true
		weakened = true
	}
	if base.Inputs.Scope.IncludeTests && !head.Inputs.Scope.IncludeTests {
		lines = append(lines, "  test files are now excluded")
		grew = true
		weakened = true
	}
	if head.AllowedTotal > base.AllowedTotal {
		lines = append(lines, fmt.Sprintf(
			"  %d more findings are suppressed by the allow list; debt removed by exemption is not debt fixed",
			head.AllowedTotal-base.AllowedTotal))
		grew = true
		weakened = true
	}
	if weakened {
		lines = append(lines,
			"  audit input weakening has no in-band approval; narrow the audit only by changing the audit tool itself, and review the rules diff")
	}

	scopeBroadened := len(sortedSetDifference(headRoots, baseRoots)) > 0 ||
		len(sortedSetDifference(baseExcludes, headExcludes)) > 0 ||
		(!base.Inputs.Scope.IncludeTests && head.Inputs.Scope.IncludeTests) ||
		(base.Inputs.Scope.ExcludeGenerated && !head.Inputs.Scope.ExcludeGenerated)

	rules := map[string]bool{}
	for rule := range base.TotalsByRule {
		rules[rule] = true
	}
	for rule := range head.TotalsByRule {
		rules[rule] = true
	}

	names := make([]string, 0, len(rules))
	for rule := range rules {
		names = append(names, rule)
	}
	slices.Sort(names)

	unattributableRuleRise := false
	for _, rule := range names {
		before, after := base.TotalsByRule[rule], head.TotalsByRule[rule]
		if after == before {
			continue
		}
		if after > before {
			if strengthened[rule] || scopeBroadened {
				lines = append(lines, attributableRuleGrowth(rule, before, after, strengthened[rule]))
				continue
			}
			unattributableRuleRise = true
			grew = true
		}
		lines = append(lines, fmt.Sprintf("  %-28s %d -> %d (%+d)", rule, before, after, after-before))
	}
	if delta > 0 && unattributableRuleRise {
		grew = true
	}

	baseBuckets := base.index()
	headBuckets := head.index()
	keys := make([]Bucket, 0, len(headBuckets))
	for bucket := range headBuckets {
		keys = append(keys, bucket)
	}
	slices.SortFunc(keys, func(a, b Bucket) int {
		return compareAll(strings.Compare(a.Rule, b.Rule), strings.Compare(a.Symbol, b.Symbol))
	})
	for _, bucket := range keys {
		before, after := baseBuckets[bucket], headBuckets[bucket]
		if after <= before {
			continue
		}
		if strengthened[bucket.Rule] || scopeBroadened {
			lines = append(lines, attributableBucketGrowth(bucket, before, after, strengthened[bucket.Rule]))
			continue
		}
		lines = append(lines, fmt.Sprintf("  new or enlarged bucket: %s in %s %d -> %d",
			bucket.Rule, bucket.Symbol, before, after))
		grew = true
	}

	return grew, lines
}

func attributableRuleGrowth(rule string, before, after int, ruleStrengthened bool) string {
	reason := "the scan scope was broadened in this change"
	if ruleStrengthened {
		reason = "the rule was tightened in this change"
	}
	return fmt.Sprintf("  attributable growth: %s %d -> %d (%s; reviewer must read the rules diff)",
		rule, before, after, reason)
}

func attributableBucketGrowth(bucket Bucket, before, after int, ruleStrengthened bool) string {
	reason := "the scan scope was broadened in this change"
	if ruleStrengthened {
		reason = "the rule was tightened in this change"
	}
	return fmt.Sprintf("  attributable growth: new or enlarged bucket: %s in %s %d -> %d"+
		" (%s; reviewer must read the rules diff)",
		bucket.Rule, bucket.Symbol, before, after, reason)
}

func severityRank(severity string) int {
	return map[string]int{
		SeverityForbidden: 3,
		SeverityInventory: 2,
		SeverityObserve:   1,
	}[severity]
}

func indexRuleFingerprints(rules []RuleFingerprint) map[string]RuleFingerprint {
	index := make(map[string]RuleFingerprint, len(rules))
	for _, rule := range rules {
		index[rule.ID] = rule
	}
	return index
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func sortedSetDifference(left, right map[string]bool) []string {
	out := make([]string, 0)
	for value := range left {
		if !right[value] {
			out = append(out, value)
		}
	}
	slices.Sort(out)
	return out
}

func strictSetGrowth(before, after map[string]bool) bool {
	return len(after) > len(before) && len(sortedSetDifference(before, after)) == 0
}
