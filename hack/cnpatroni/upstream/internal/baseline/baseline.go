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

// Package baseline reads the recorded CloudNativePatroni fork baseline: the
// commit this fork was created from and the most recent upstream commit that
// has been integrated into it.
package baseline

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// Schema is the only baseline schema identifier this tool understands.
const Schema = "cnpatroni.io/upstream-baseline/v1"

// DefaultRemote is the git remote name the tooling expects upstream
// CloudNativePG to be configured under.
const DefaultRemote = "upstream"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Upstream identifies the tracked upstream repository.
type Upstream struct {
	URL    string `yaml:"url"`
	Track  string `yaml:"track"`
	Remote string `yaml:"remote,omitempty"`
}

// Deviation records a documented departure from the specification.
type Deviation struct {
	SpecSays   string `yaml:"spec_says"`
	Actual     string `yaml:"actual"`
	Reason     string `yaml:"reason"`
	Cost       string `yaml:"cost"`
	RecordedIn string `yaml:"recorded_in"`
}

// Point is a recorded commit on the upstream history.
type Point struct {
	Commit        string     `yaml:"commit"`
	Describe      string     `yaml:"describe"`
	UpstreamRef   string     `yaml:"upstream_ref,omitempty"`
	Date          string     `yaml:"date"`
	Tag           string     `yaml:"tag,omitempty"`
	IntegrationPR string     `yaml:"integration_pr,omitempty"`
	Report        string     `yaml:"report,omitempty"`
	SpecDeviation *Deviation `yaml:"spec_deviation,omitempty"`
}

// Compatibility records the upstream minor version this fork claims.
type Compatibility struct {
	Minor                    string `yaml:"cloudnative_pg_minor"`
	ReferenceTag             string `yaml:"cloudnative_pg_reference_tag"`
	Claimed                  string `yaml:"claimed"`
	VerifiedAt               string `yaml:"verified_at"`
	UnadoptedUpstreamCommits int    `yaml:"unadopted_upstream_commits"`
}

// Refusal records an upstream commit this fork deliberately does not adopt.
type Refusal struct {
	Commit    string `yaml:"commit"`
	Subject   string `yaml:"subject"`
	Reason    string `yaml:"reason"`
	DecidedBy string `yaml:"decided_by"`
	Date      string `yaml:"date"`
}

// Baseline is a parsed baseline record.
type Baseline struct {
	Schema         string        `yaml:"schema"`
	Upstream       Upstream      `yaml:"upstream"`
	ForkBase       Point         `yaml:"fork_base"`
	LastIntegrated Point         `yaml:"last_integrated"`
	Compatibility  Compatibility `yaml:"compatibility"`
	NotAdopted     []Refusal     `yaml:"not_adopted"`
}

// Load reads and parses a baseline file.
func Load(path string) (*Baseline, error) {
	body, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied by design
	if err != nil {
		return nil, fmt.Errorf("cannot read the upstream baseline: %w", err)
	}

	b, err := Parse(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// Parse parses baseline bytes and checks the fields the rest of the tooling
// relies on. Unknown fields are rejected, so a typo cannot silently reset the
// integration high-water mark to the zero value.
func Parse(body []byte) (*Baseline, error) {
	var b Baseline

	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&b); err != nil {
		return nil, fmt.Errorf("cannot parse the upstream baseline: %w", err)
	}

	if b.Schema != Schema {
		return nil, fmt.Errorf("schema is %q, want %q", b.Schema, Schema)
	}
	if !commitPattern.MatchString(b.ForkBase.Commit) {
		return nil, fmt.Errorf("fork_base.commit %q is not a full 40-character commit identifier",
			b.ForkBase.Commit)
	}
	if !commitPattern.MatchString(b.LastIntegrated.Commit) {
		return nil, fmt.Errorf("last_integrated.commit %q is not a full 40-character commit identifier",
			b.LastIntegrated.Commit)
	}
	if b.Upstream.URL == "" {
		return nil, fmt.Errorf("upstream.url is required")
	}
	if b.Upstream.Track == "" {
		return nil, fmt.Errorf("upstream.track is required")
	}
	if b.Upstream.Remote == "" {
		b.Upstream.Remote = DefaultRemote
	}

	return &b, nil
}

// TrackingRef is the remote-tracking ref of the upstream branch this fork
// follows, for example `upstream/main`.
func (b *Baseline) TrackingRef() string {
	return b.Upstream.Remote + "/" + b.Upstream.Track
}
