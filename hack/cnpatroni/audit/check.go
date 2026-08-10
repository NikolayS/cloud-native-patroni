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
	"slices"
	"strings"
)

// Report separates the two kinds of failure on purpose. A policy violation and
// a piece of hygiene must not share an exit code, or engineers stop reading
// either of them.
type Report struct {
	// Violations fail the build with exit code 1.
	Violations []string
	// Hygiene fails the build with exit code 3 and never masquerades as a policy
	// breach.
	Hygiene []string
}

// milestoneOrder gives the "until" field on an allow entry a meaning.
var milestoneOrder = map[string]int{"M0": 0, "M1": 1, "M2": 2, "M3": 3, "M4": 4}

// Check is the gate. It answers four questions:
//
//  1. is every authority hit classified, or explicitly allowed;
//  2. has the forbidden-call baseline grown;
//  3. is every classification marked "guard: required" actually wired;
//  4. does every classification and responsibility still describe real code.
func Check(res *ScanResult, cls *Classification, baseline *Baseline, milestone string) Report {
	var report Report

	entries := cls.bySymbol()
	report.checkClassificationCoverage(res, cls, entries)
	report.checkBaseline(FilterAllowed(res.Findings, cls), baseline)
	report.checkGuardWiring(res, cls)
	report.checkStaleness(res, cls, entries)
	report.checkAllowExpiry(cls, milestone)

	return report
}

func (r *Report) checkClassificationCoverage(res *ScanResult, cls *Classification, entries map[string]*Entry) {
	reported := map[string]bool{}

	for _, f := range res.Findings {
		if f.Severity == SeverityObserve {
			continue
		}
		if entries[f.Symbol] != nil || cls.allows(f) {
			continue
		}
		key := f.Rule + "\x00" + f.Symbol
		if reported[key] {
			continue
		}
		reported[key] = true

		r.Violations = append(r.Violations, unclassifiedMessage(f))
	}
}

func unclassifiedMessage(f Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:%d: unclassified authority hit\n", f.Path, f.Line, f.Col)
	fmt.Fprintf(&b, "    symbol:  %s\n", f.Symbol)
	fmt.Fprintf(&b, "    rule:    %s (severity: %s", f.Rule, f.Severity)
	if f.Spec != "" {
		fmt.Fprintf(&b, ", specification section %s", f.Spec)
	}
	fmt.Fprintf(&b, ")\n")
	fmt.Fprintf(&b, "    matched: %s\n\n", f.Detail)
	b.WriteString("    Every code path that can influence PostgreSQL role or process lifecycle must\n")
	b.WriteString("    be classified (specification section 9.1). Add an entry to\n")
	b.WriteString("    hack/cnpatroni/audit/policy/authority-classification.yaml:\n\n")
	b.WriteString(stubFor(f))
	return b.String()
}

// stubFor prints a paste-ready classification entry, so that an engineer meeting
// this message during an upstream merge has everything needed in the terminal.
func stubFor(f Finding) string {
	return fmt.Sprintf(`      - path: %s
        symbol: %s
        class: ""              # keep | move | adapt | disable | delete-later
        dest: ""               # patroni-container | finite-init | cnpatroni-agent | operator | disabled
        rationale: ""          # required, at least %d characters, specific to this symbol
        reviewed-at: ""        # YYYY-MM-DD HH:MM:SS UTC
`, f.Path, f.Symbol, minimumRationale)
}

// FilterAllowed drops the findings an allow entry covers, so that an exception
// is recorded once, in the classification file, rather than once there and once
// in the baseline.
func FilterAllowed(findings []Finding, cls *Classification) []Finding {
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if !cls.allows(f) {
			out = append(out, f)
		}
	}
	return out
}

func (r *Report) checkBaseline(findings []Finding, baseline *Baseline) {
	recorded := baseline.index()
	observed := map[Bucket]Finding{}
	counts := map[Bucket]int{}

	for _, f := range findings {
		if f.Severity != SeverityForbidden {
			continue
		}
		key := Bucket{Rule: f.Rule, Symbol: f.Symbol}
		counts[key]++
		if _, seen := observed[key]; !seen {
			observed[key] = f
		}
	}

	keys := make([]Bucket, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Bucket) int {
		return compareAll(strings.Compare(a.Rule, b.Rule), strings.Compare(a.Symbol, b.Symbol))
	})

	for _, key := range keys {
		before, known := recorded[key]
		f := observed[key]
		switch {
		case !known:
			r.Violations = append(r.Violations, fmt.Sprintf(
				"%s:%d:%d: new forbidden call: %s in %s (%s)",
				f.Path, f.Line, f.Col, key.Rule, key.Symbol, f.Detail))
		case counts[key] > before:
			r.Violations = append(r.Violations, fmt.Sprintf(
				"%s:%d:%d: %s in %s grew %d -> %d",
				f.Path, f.Line, f.Col, key.Rule, key.Symbol, before, counts[key]))
		case counts[key] < before:
			r.Hygiene = append(r.Hygiene, fmt.Sprintf(
				"baseline can be tightened: %s in %s %d -> %d; run `go run . baseline --write`",
				key.Rule, key.Symbol, before, counts[key]))
		}
	}

	for _, bucket := range baseline.Buckets {
		key := Bucket{Rule: bucket.Rule, Symbol: bucket.Symbol}
		if counts[key] == 0 {
			r.Hygiene = append(r.Hygiene, fmt.Sprintf(
				"stale baseline entry: %s in %s; run `go run . baseline --write`",
				bucket.Rule, bucket.Symbol))
		}
	}
}

func (r *Report) checkGuardWiring(res *ScanResult, cls *Classification) {
	for i := range cls.Entries {
		e := cls.Entries[i]
		if e.Guard != "required" {
			continue
		}
		ops, called := res.GuardOps[e.Symbol]
		if !called || len(ops) == 0 {
			r.Violations = append(r.Violations, fmt.Sprintf(
				"%s: %s is classified guard: required but does not call guard.Forbidden",
				e.Path, e.Symbol))
			continue
		}
		if !slices.Contains(ops, e.Symbol) {
			r.Violations = append(r.Violations, fmt.Sprintf(
				"%s: guard.Forbidden(%q) in %s: the op literal must equal the enclosing symbol",
				e.Path, ops[0], e.Symbol))
		}
	}
}

func (r *Report) checkStaleness(res *ScanResult, cls *Classification, entries map[string]*Entry) {
	symbols := make([]string, 0, len(entries))
	for symbol := range entries {
		symbols = append(symbols, symbol)
	}
	slices.Sort(symbols)

	for _, symbol := range symbols {
		if !res.Symbols[symbol] {
			r.Hygiene = append(r.Hygiene, fmt.Sprintf(
				"stale classification: %s no longer exists in the tree; confirm it was removed"+
					" rather than renamed, then delete or update the entry", symbol))
		}
	}

	for _, resp := range cls.Responsibilities {
		for _, anchor := range resp.Anchors {
			if !res.Symbols[anchor] {
				r.Hygiene = append(r.Hygiene, fmt.Sprintf(
					"stale responsibility anchor: %q anchors on %s, which no longer exists;"+
						" confirm the responsibility moved rather than vanished",
					resp.Responsibility, anchor))
			}
		}
	}
}

func (r *Report) checkAllowExpiry(cls *Classification, milestone string) {
	current, known := milestoneOrder[milestone]
	if !known {
		return
	}
	for _, a := range cls.Allow {
		if a.Until == "" {
			continue
		}
		until, ok := milestoneOrder[a.Until]
		if !ok {
			r.Violations = append(r.Violations, fmt.Sprintf(
				"allow entry for rule %q has until %q, which is not a milestone", a.Rule, a.Until))
			continue
		}
		if current > until {
			r.Violations = append(r.Violations, fmt.Sprintf(
				"allow entry expired: rule %q in %s was allowed until %s and the current milestone is %s",
				a.Rule, a.Package, a.Until, milestone))
		}
	}
}
