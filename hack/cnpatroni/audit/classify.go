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
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

const classificationSchema = "cnpatroni-authority-classification/v1"

// minimumRationale is long enough to exclude "n/a" and "see above" without
// forcing an essay onto an obvious entry.
const minimumRationale = 20

// classes is the specification section 9.1 classification vocabulary.
var classes = map[string]bool{
	"keep": true, "move": true, "adapt": true, "disable": true, "delete-later": true,
}

// destinations is the specification section 9.1 destination vocabulary, in
// kebab case. render.go maps them back to the specification's prose form.
var destinations = map[string]bool{
	"patroni-container": true, "finite-init": true, "cnpatroni-agent": true,
	"operator": true, "disabled": true,
}

// timestampFormat is this project's absolute timestamp format. A bare date is
// rejected: "reviewed on the ninth" is not a reviewable statement.
var timestampFormat = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} UTC$`)

// Classification is the curated human judgement the scanner cannot produce.
type Classification struct {
	Schema           string           `yaml:"schema"`
	Defaults         Defaults         `yaml:"defaults"`
	Entries          []Entry          `yaml:"entries"`
	Responsibilities []Responsibility `yaml:"responsibilities"`
	Allow            []AllowEntry     `yaml:"allow"`
}

// Defaults fills in fields that are the same on almost every entry.
type Defaults struct {
	Owner string `yaml:"owner"`
}

// Entry is the decision taken about one symbol.
type Entry struct {
	Path       string `yaml:"path"`
	Symbol     string `yaml:"symbol"`
	Class      string `yaml:"class"`
	Dest       string `yaml:"dest"`
	Rationale  string `yaml:"rationale"`
	Owner      string `yaml:"owner,omitempty"`
	ReviewedAt string `yaml:"reviewed-at"`
	SpecRef    string `yaml:"spec-ref,omitempty"`
	// Guard is "required" when the M1 body of this symbol must call the runtime
	// guard. Check verifies the call site and the op literal.
	Guard string `yaml:"guard,omitempty"`
}

// Responsibility maps one instance-manager responsibility to its destination.
// Responsibilities are statements about behaviour rather than about symbols, so
// they cannot be generated; anchors keep them honest.
type Responsibility struct {
	Responsibility string   `yaml:"responsibility"`
	Dest           string   `yaml:"dest"`
	Rationale      string   `yaml:"rationale"`
	Owner          string   `yaml:"owner,omitempty"`
	ReviewedAt     string   `yaml:"reviewed-at"`
	Anchors        []string `yaml:"anchors"`
}

// AllowEntry exempts a rule in a package. There is no inline suppression
// comment on purpose: an escape hatch inside the code is invisible in review.
type AllowEntry struct {
	Rule    string `yaml:"rule"`
	Package string `yaml:"package"`
	File    string `yaml:"file,omitempty"`
	// Symbol narrows the entry to one function, which is what a rule matching both
	// a genuine write and a denylist key in the same file needs.
	Symbol     string `yaml:"symbol,omitempty"`
	Reason     string `yaml:"reason"`
	Owner      string `yaml:"owner,omitempty"`
	ReviewedAt string `yaml:"reviewed-at"`
	Until      string `yaml:"until,omitempty"`
}

// LoadClassification reads and validates the curated decisions.
func LoadClassification(path string) (*Classification, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied configuration
	if err != nil {
		return nil, fmt.Errorf("reading classification: %w", err)
	}

	var cls Classification
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cls); err != nil {
		return nil, fmt.Errorf("parsing classification yaml: %w", err)
	}
	if cls.Schema != classificationSchema {
		return nil, fmt.Errorf("classification schema is %q, want %q", cls.Schema, classificationSchema)
	}
	if err := cls.validate(); err != nil {
		return nil, err
	}

	return &cls, nil
}

func (c *Classification) validate() error {
	seen := map[string]bool{}
	rationales := map[string]string{}

	for i := range c.Entries {
		e := &c.Entries[i]
		if e.Owner == "" {
			e.Owner = c.Defaults.Owner
		}
		switch {
		case e.Symbol == "":
			return fmt.Errorf("entry %d has no symbol", i)
		case seen[e.Symbol]:
			return fmt.Errorf("duplicate entry for symbol %q", e.Symbol)
		case e.Path == "":
			return fmt.Errorf("entry %q has no path", e.Symbol)
		case !classes[e.Class]:
			return fmt.Errorf("entry %q has class %q, want one of keep, move, adapt, disable, delete-later",
				e.Symbol, e.Class)
		case !destinations[e.Dest]:
			return fmt.Errorf("entry %q has dest %q, want one of patroni-container, finite-init,"+
				" cnpatroni-agent, operator, disabled", e.Symbol, e.Dest)
		case len(strings.Join(strings.Fields(e.Rationale), " ")) < minimumRationale:
			return fmt.Errorf("entry %q has a rationale shorter than %d characters",
				e.Symbol, minimumRationale)
		case e.Owner == "":
			return fmt.Errorf("entry %q has no owner and defaults.owner is unset", e.Symbol)
		case !timestampFormat.MatchString(e.ReviewedAt):
			return fmt.Errorf("entry %q has reviewed-at %q, want the format 2006-01-02 15:04:05 UTC",
				e.Symbol, e.ReviewedAt)
		case e.Class == "disable" && e.Dest != "disabled":
			return fmt.Errorf("entry %q contradicts itself: class disable with dest %q", e.Symbol, e.Dest)
		case e.Class == "keep" && e.Dest == "disabled":
			return fmt.Errorf("entry %q contradicts itself: class keep with dest disabled", e.Symbol)
		case e.Guard != "" && e.Guard != "required":
			return fmt.Errorf("entry %q has guard %q, want \"required\" or nothing", e.Symbol, e.Guard)
		}
		seen[e.Symbol] = true

		folded := strings.ToLower(strings.Join(strings.Fields(e.Rationale), " "))
		if other, dup := rationales[folded]; dup {
			return fmt.Errorf("entry %q repeats the rationale of %q verbatim; say what is specific"+
				" about this symbol", e.Symbol, other)
		}
		rationales[folded] = e.Symbol
	}

	if err := c.validateResponsibilities(); err != nil {
		return err
	}
	return c.validateAllow()
}

func (c *Classification) validateResponsibilities() error {
	for i := range c.Responsibilities {
		r := &c.Responsibilities[i]
		if r.Owner == "" {
			r.Owner = c.Defaults.Owner
		}
		switch {
		case r.Responsibility == "":
			return fmt.Errorf("responsibility %d has no description", i)
		case !destinations[r.Dest]:
			return fmt.Errorf("responsibility %q has dest %q, which is not in the destination vocabulary",
				r.Responsibility, r.Dest)
		case len(strings.Join(strings.Fields(r.Rationale), " ")) < minimumRationale:
			return fmt.Errorf("responsibility %q has a rationale shorter than %d characters",
				r.Responsibility, minimumRationale)
		case r.Owner == "":
			return fmt.Errorf("responsibility %q has no owner and defaults.owner is unset",
				r.Responsibility)
		case !timestampFormat.MatchString(r.ReviewedAt):
			return fmt.Errorf("responsibility %q has reviewed-at %q, want the format 2006-01-02 15:04:05 UTC",
				r.Responsibility, r.ReviewedAt)
		case len(r.Anchors) == 0:
			return fmt.Errorf("responsibility %q has no anchors, so nothing keeps it honest",
				r.Responsibility)
		}
	}
	return nil
}

func (c *Classification) validateAllow() error {
	for i := range c.Allow {
		a := &c.Allow[i]
		if a.Owner == "" {
			a.Owner = c.Defaults.Owner
		}
		switch {
		case a.Rule == "":
			return fmt.Errorf("allow entry %d has no rule", i)
		case a.Package == "":
			return fmt.Errorf("allow entry for rule %q has no package", a.Rule)
		case len(strings.Join(strings.Fields(a.Reason), " ")) < minimumRationale:
			return fmt.Errorf("allow entry for rule %q has a reason shorter than %d characters",
				a.Rule, minimumRationale)
		case a.Owner == "":
			return fmt.Errorf("allow entry for rule %q has no owner", a.Rule)
		case !timestampFormat.MatchString(a.ReviewedAt):
			return fmt.Errorf("allow entry for rule %q has reviewed-at %q, want the format"+
				" 2006-01-02 15:04:05 UTC", a.Rule, a.ReviewedAt)
		}
	}
	return nil
}

// bySymbol indexes the entries.
func (c *Classification) bySymbol() map[string]*Entry {
	out := make(map[string]*Entry, len(c.Entries))
	for i := range c.Entries {
		out[c.Entries[i].Symbol] = &c.Entries[i]
	}
	return out
}

// allows reports whether an allow entry covers the finding.
func (c *Classification) allows(f Finding) bool {
	for i := range c.Allow {
		a := c.Allow[i]
		if a.Rule != "*" && a.Rule != f.Rule {
			continue
		}
		if !packageMatches(a.Package, f.Package) {
			continue
		}
		if a.File != "" && a.File != f.Path {
			continue
		}
		if a.Symbol != "" && a.Symbol != f.Symbol {
			continue
		}
		return true
	}
	return false
}

func packageMatches(pattern, pkg string) bool {
	if strings.HasSuffix(pattern, "/...") {
		return strings.HasPrefix(pkg, strings.TrimSuffix(pattern, "..."))
	}
	return pattern == pkg
}
