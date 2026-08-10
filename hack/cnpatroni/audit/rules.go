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
	"strings"

	"go.yaml.in/yaml/v3"
)

const rulesSchema = "cnpatroni-authority-rules/v1"

// Severity levels, in decreasing strictness.
const (
	// SeverityForbidden marks an operation specification section 7.4 forbids.
	// Every hit must be classified and must appear in the baseline; a hit that is
	// not in the baseline, or a bucket that grew, fails the check.
	SeverityForbidden = "forbidden"

	// SeverityInventory marks a code path that is not itself forbidden but must be
	// triaged. Every hit must be classified. Hits do not enter the baseline.
	SeverityInventory = "inventory"

	// SeverityObserve marks a search term whose hits are counted for the audit
	// document's coverage table. Hits are never gated.
	SeverityObserve = "observe"
)

var severities = map[string]bool{
	SeverityForbidden: true,
	SeverityInventory: true,
	SeverityObserve:   true,
}

// RuleSet is the parsed .../policy/authority-rules.yaml.
type RuleSet struct {
	Schema string `yaml:"schema"`
	// Module is the import path that the "..." prefix in a symbol expands to.
	Module string `yaml:"module"`
	// GuardSymbol is the fully qualified symbol of the runtime guard entry point.
	// Check uses it to verify entries classified with "guard: required".
	GuardSymbol string `yaml:"guard_symbol"`
	Scope       Scope  `yaml:"scope"`
	Rules       []Rule `yaml:"rules"`
}

// Scope limits what the scanner reads.
type Scope struct {
	// Roots are directories, relative to the repository root, each holding a Go
	// module that is loaded with the pattern ./... .
	Roots            []string `yaml:"roots"`
	ExcludePaths     []string `yaml:"exclude_paths"`
	ExcludeGenerated bool     `yaml:"exclude_generated"`
	IncludeTests     bool     `yaml:"include_tests"`
}

// Rule is one authority pattern.
type Rule struct {
	ID       string `yaml:"id"`
	Severity string `yaml:"severity"`
	// Spec is the specification section this rule enforces, for the message.
	Spec    string `yaml:"spec"`
	Message string `yaml:"message"`

	// Calls are fully qualified function or method symbols.
	Calls []string `yaml:"calls"`
	// CallArgContains narrows Calls: the call matches only when one of its
	// arguments is a constant string containing one of these substrings. It is how
	// exec.Command is limited to the PostgreSQL binaries.
	CallArgContains []string `yaml:"call_arg_contains"`
	// Literals match string literals containing the given text.
	Literals []string `yaml:"literals"`
	// Imports match import paths.
	Imports []string `yaml:"imports"`
	// FieldWrites match assignments to a named struct field, written as
	// <package path>.<Type>.<Field>.
	FieldWrites []string `yaml:"field_writes"`
	// FieldReads match any other selection of a named struct field.
	FieldReads []string `yaml:"field_reads"`
}

func (r Rule) hasMatcher() bool {
	return len(r.Calls)+len(r.Literals)+len(r.Imports)+len(r.FieldWrites)+len(r.FieldReads) > 0
}

// LoadRules reads and validates the rule set.
func LoadRules(path string) (*RuleSet, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied configuration
	if err != nil {
		return nil, fmt.Errorf("reading rules: %w", err)
	}

	var rs RuleSet
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&rs); err != nil {
		return nil, fmt.Errorf("parsing rules yaml: %w", err)
	}

	if rs.Schema != rulesSchema {
		return nil, fmt.Errorf("rules schema is %q, want %q", rs.Schema, rulesSchema)
	}
	if len(rs.Scope.Roots) == 0 {
		rs.Scope.Roots = []string{"."}
	}

	seen := map[string]bool{}
	for i := range rs.Rules {
		rule := &rs.Rules[i]
		switch {
		case rule.ID == "":
			return nil, fmt.Errorf("rule %d has no id", i)
		case seen[rule.ID]:
			return nil, fmt.Errorf("duplicate rule id %q", rule.ID)
		case !severities[rule.Severity]:
			return nil, fmt.Errorf("rule %q has severity %q, want one of forbidden, inventory, observe",
				rule.ID, rule.Severity)
		case !rule.hasMatcher():
			return nil, fmt.Errorf("rule %q has no matcher: set calls, literals, imports,"+
				" field_writes or field_reads", rule.ID)
		}
		seen[rule.ID] = true

		rule.Calls = expandAll(rule.Calls, rs.Module)
		rule.Imports = expandAll(rule.Imports, rs.Module)
		rule.FieldWrites = expandAll(rule.FieldWrites, rs.Module)
		rule.FieldReads = expandAll(rule.FieldReads, rs.Module)
	}
	rs.GuardSymbol = expand(rs.GuardSymbol, rs.Module)

	return &rs, nil
}

// expand turns the "..." shorthand into the module path, so that rules stay
// readable without losing the fully qualified form the scanner compares against.
func expand(symbol, module string) string {
	if module == "" || !strings.HasPrefix(symbol, ".../") {
		return symbol
	}
	return module + "/" + strings.TrimPrefix(symbol, ".../")
}

func expandAll(symbols []string, module string) []string {
	if len(symbols) == 0 {
		return symbols
	}
	out := make([]string, 0, len(symbols))
	for _, s := range symbols {
		out = append(out, expand(s, module))
	}
	return out
}
