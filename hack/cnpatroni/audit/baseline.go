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

const baselineSchema = "cnpatroni-authority-baseline/v1"

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
	TotalsByRule  map[string]int `yaml:"totals_by_rule"`
	Buckets       []Bucket       `yaml:"buckets"`
}

// Bucket is the number of hits of one rule inside one symbol.
type Bucket struct {
	Rule   string `yaml:"rule"`
	Symbol string `yaml:"symbol"`
	Count  int    `yaml:"count"`
}

// BuildBaseline counts the forbidden findings. Inventory and observe findings
// are deliberately excluded: they are triage material, not debt to burn down.
func BuildBaseline(findings []Finding, commit, generatedAt string) *Baseline {
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
		TotalsByRule:  byRule,
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

	return nil
}

// Write renders the baseline.
func (b *Baseline) Write(path string) error {
	out, err := yaml.Marshal(b)
	if err != nil {
		return fmt.Errorf("rendering baseline: %w", err)
	}
	header := "# Generated by hack/cnpatroni/audit; run `go run . baseline --write`.\n" +
		"# The totals may fall and must never rise. See docs/cnpatroni/authority-audit.md.\n"
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
func CompareBaselines(base, head *Baseline) (grew bool, lines []string) {
	delta := head.Total - base.Total
	lines = append(lines, fmt.Sprintf("authority baseline: %d -> %d (%+d)", base.Total, head.Total, delta))
	if delta > 0 {
		grew = true
	}

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

	for _, rule := range names {
		before, after := base.TotalsByRule[rule], head.TotalsByRule[rule]
		if after == before {
			continue
		}
		lines = append(lines, fmt.Sprintf("  %-28s %d -> %d (%+d)", rule, before, after, after-before))
		if after > before {
			grew = true
		}
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
		lines = append(lines, fmt.Sprintf("  new or enlarged bucket: %s in %s %d -> %d",
			bucket.Rule, bucket.Symbol, before, after))
		grew = true
	}

	return grew, lines
}
