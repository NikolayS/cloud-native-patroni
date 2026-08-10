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
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
)

// destinationProse maps the machine vocabulary back to the words specification
// section 9.1 uses, so that the data file stays machine-clean while the document
// reads in the specification's own terms.
var destinationProse = map[string]string{
	"patroni-container": "Patroni container",
	"finite-init":       "finite init",
	"cnpatroni-agent":   "cnpatroni-agent",
	"operator":          "operator",
	"disabled":          "disabled",
}

// classOrder and destOrder fix the row order of the summary tables, so that a
// regenerated document differs only where the code differs.
var (
	classOrder = []string{"disable", "adapt", "move", "keep", "delete-later"}
	destOrder  = []string{"disabled", "operator", "cnpatroni-agent", "patroni-container", "finite-init"}
)

type countRow struct {
	Name  string
	Count int
}

type ruleRow struct {
	ID       string
	Severity string
	Spec     string
	Hits     int
	Symbols  int
	Files    int
}

type entryRow struct {
	Path      string
	Symbol    string
	Short     string
	Class     string
	Dest      string
	DestProse string
	Rationale string
	Rules     string
}

type packageSection struct {
	Package string
	Entries []entryRow
}

// docData is everything both generated documents are rendered from. Every number
// in the prose is a field here, which is what stops the document drifting from
// the code.
type docData struct {
	Rules string

	Findings int
	Packages int
	Files    int

	Entries          int
	Responsibilities int
	AllowEntries     int

	ByClass    []countRow
	ByDest     []countRow
	RespByDest []countRow
	ByRule     []ruleRow
	ByPackage  []packageSection

	Baseline *Baseline

	// Fork-feasibility measurements, required by owner directive D-5.
	ForbiddenHits     int
	ForbiddenSymbols  int
	ForbiddenFiles    int
	ForbiddenPackages int

	ClassifiedFiles    int
	ClassifiedPackages int

	RelocatedEntries  int
	RelocatedFiles    int
	UntouchedEntries  int
	DisabledEntries   int
	AdaptedEntries    int
	HighestRulePkg    string
	HighestRulePkgPct int
	TopThreePkgPct    int

	ResponsibilityRows []Responsibility
}

func runDoc(o *options) int {
	rules, res, err := o.load()
	if err != nil {
		return toolError(err)
	}
	cls, err := LoadClassification(o.classification)
	if err != nil {
		return toolError(err)
	}
	baseline, err := LoadBaseline(o.baseline)
	if err != nil {
		return toolError(err)
	}

	data := buildDocData(rules, res, cls, baseline, o.rules)

	audit, err := renderAudit(o.narrative, data)
	if err != nil {
		return toolError(err)
	}
	if err := writeDoc(filepath.Join(o.root, filepath.FromSlash(o.auditOut)), audit); err != nil {
		return toolError(err)
	}

	responsibilityMap, err := renderResponsibilityMap(data)
	if err != nil {
		return toolError(err)
	}
	if err := writeDoc(filepath.Join(o.root, filepath.FromSlash(o.mapOut)), responsibilityMap); err != nil {
		return toolError(err)
	}

	fmt.Printf("wrote %s and %s\n", o.auditOut, o.mapOut)
	return exitClean
}

func writeDoc(target, content string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}
	return nil
}

//nolint:gocognit // the function is a straight-line aggregation of one data set
func buildDocData(rules *RuleSet, res *ScanResult, cls *Classification, baseline *Baseline,
	rulesPath string,
) *docData {
	data := &docData{
		Rules:              filepath.ToSlash(rulesPath),
		Findings:           len(res.Findings),
		Packages:           res.Packages,
		Files:              res.Files,
		Entries:            len(cls.Entries),
		Responsibilities:   len(cls.Responsibilities),
		AllowEntries:       len(cls.Allow),
		Baseline:           baseline,
		ResponsibilityRows: cls.Responsibilities,
	}

	rulesByID := map[string]Rule{}
	for _, r := range rules.Rules {
		rulesByID[r.ID] = r
	}

	hitsByRule := map[string]int{}
	symbolsByRule := map[string]map[string]bool{}
	filesByRule := map[string]map[string]bool{}
	rulesBySymbol := map[string]map[string]bool{}

	forbiddenSymbols := map[string]bool{}
	forbiddenFiles := map[string]bool{}
	forbiddenPackages := map[string]bool{}

	for _, f := range res.Findings {
		hitsByRule[f.Rule]++
		addTo(symbolsByRule, f.Rule, f.Symbol)
		addTo(filesByRule, f.Rule, f.Path)
		addTo(rulesBySymbol, f.Symbol, f.Rule)

		if f.Severity == SeverityForbidden {
			data.ForbiddenHits++
			forbiddenSymbols[f.Symbol] = true
			forbiddenFiles[f.Path] = true
			forbiddenPackages[f.Package] = true
		}
	}
	data.ForbiddenSymbols = len(forbiddenSymbols)
	data.ForbiddenFiles = len(forbiddenFiles)
	data.ForbiddenPackages = len(forbiddenPackages)

	for _, id := range sortedKeys(rulesByID) {
		r := rulesByID[id]
		data.ByRule = append(data.ByRule, ruleRow{
			ID: r.ID, Severity: r.Severity, Spec: r.Spec,
			Hits:    hitsByRule[r.ID],
			Symbols: len(symbolsByRule[r.ID]),
			Files:   len(filesByRule[r.ID]),
		})
	}

	byClass := map[string]int{}
	byDest := map[string]int{}
	classifiedFiles := map[string]bool{}
	classifiedPackages := map[string]bool{}
	relocatedFiles := map[string]bool{}
	sections := map[string][]entryRow{}

	for _, e := range cls.Entries {
		byClass[e.Class]++
		byDest[e.Dest]++
		classifiedFiles[e.Path] = true

		pkg := path.Dir(e.Path)
		classifiedPackages[pkg] = true

		switch e.Class {
		case "disable":
			data.DisabledEntries++
		case "adapt":
			data.AdaptedEntries++
		case "move":
			data.RelocatedEntries++
			relocatedFiles[e.Path] = true
		case "keep":
			data.UntouchedEntries++
		}

		rules := sortedKeys(rulesBySymbol[e.Symbol])
		sections[pkg] = append(sections[pkg], entryRow{
			Path:      e.Path,
			Symbol:    e.Symbol,
			Short:     shortSymbol(e.Symbol),
			Class:     e.Class,
			Dest:      e.Dest,
			DestProse: destinationProse[e.Dest],
			Rationale: foldRationale(e.Rationale),
			Rules:     strings.Join(rules, ", "),
		})
	}
	data.ClassifiedFiles = len(classifiedFiles)
	data.ClassifiedPackages = len(classifiedPackages)
	data.RelocatedFiles = len(relocatedFiles)

	for _, class := range classOrder {
		data.ByClass = append(data.ByClass, countRow{Name: class, Count: byClass[class]})
	}
	for _, dest := range destOrder {
		data.ByDest = append(data.ByDest, countRow{Name: destinationProse[dest], Count: byDest[dest]})
	}

	respByDest := map[string]int{}
	for _, r := range cls.Responsibilities {
		respByDest[r.Dest]++
	}
	for _, dest := range destOrder {
		data.RespByDest = append(data.RespByDest, countRow{Name: destinationProse[dest], Count: respByDest[dest]})
	}

	for _, pkg := range sortedKeys(sections) {
		rows := sections[pkg]
		slices.SortFunc(rows, func(a, b entryRow) int {
			return compareAll(strings.Compare(a.Path, b.Path), strings.Compare(a.Symbol, b.Symbol))
		})
		data.ByPackage = append(data.ByPackage, packageSection{Package: pkg, Entries: rows})
	}

	data.HighestRulePkg, data.HighestRulePkgPct, data.TopThreePkgPct = concentration(res)

	return data
}

// concentration reports the package holding the largest share of forbidden hits,
// which is the number specification section 4.4 cares about: a diff that
// concentrates is a diff that survives upstream merges.
func concentration(res *ScanResult) (top string, topPct, topThreePct int) {
	counts := map[string]int{}
	total := 0
	for _, f := range res.Findings {
		if f.Severity != SeverityForbidden {
			continue
		}
		counts[f.Package]++
		total++
	}
	if total == 0 {
		return "", 0, 0
	}

	ranked := sortedKeys(counts)
	slices.SortStableFunc(ranked, func(a, b string) int { return counts[b] - counts[a] })

	inTopThree := 0
	for i, pkg := range ranked {
		if i >= 3 {
			break
		}
		inTopThree += counts[pkg]
	}
	return ranked[0], counts[ranked[0]] * 100 / total, inTopThree * 100 / total
}

func addTo(index map[string]map[string]bool, key, value string) {
	if index[key] == nil {
		index[key] = map[string]bool{}
	}
	index[key][value] = true
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// shortSymbol drops the module prefix so that the tables stay readable. The full
// symbol remains in the classification file, which is the authoritative copy.
func shortSymbol(symbol string) string {
	if i := strings.LastIndex(symbol, "/"); i >= 0 {
		return symbol[i+1:]
	}
	return symbol
}

func foldRationale(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func renderAudit(narrativePath string, data *docData) (string, error) {
	narrative, err := os.ReadFile(narrativePath) //nolint:gosec // operator-supplied configuration
	if err != nil {
		return "", fmt.Errorf("reading narrative: %w", err)
	}

	tmpl, err := template.New("audit").Parse(auditTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing the audit template: %w", err)
	}
	if _, err := tmpl.New("narrative").Parse(string(narrative)); err != nil {
		return "", fmt.Errorf("parsing the narrative fragment: %w", err)
	}

	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("rendering the audit document: %w", err)
	}
	return out.String(), nil
}

func renderResponsibilityMap(data *docData) (string, error) {
	tmpl, err := template.New("map").
		Funcs(template.FuncMap{"dest": func(d string) string { return destinationProse[d] }}).
		Parse(responsibilityTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing the responsibility template: %w", err)
	}

	var out strings.Builder
	if err := tmpl.Execute(&out, data); err != nil {
		return "", fmt.Errorf("rendering the responsibility map: %w", err)
	}
	return out.String(), nil
}
