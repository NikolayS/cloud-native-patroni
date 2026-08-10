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

// Package app coordinates the oracle's guarded IO around its pure detector.
package app

import (
	"fmt"
	"strings"
	"time"
)

// Config is the complete oracle run configuration. Passwords are deliberately
// absent because they may only be loaded from a file or environment variable.
type Config struct {
	Context           string
	Namespace         string
	Scope             string
	ExpectedNodes     []string
	RoundPeriod       time.Duration
	AttemptTimeout    time.Duration
	Duration          time.Duration
	BundleDir         string
	ControlPort       int
	PatroniPort       int
	PGPort            int
	PreflightRounds   int
	LostWriteSeverity string
}

// EffectiveAttemptTimeout defaults to 80 percent of the period, capped at
// 400 ms, so both the client deadline and server statement timeout remain below
// the round period.
func (c Config) EffectiveAttemptTimeout() time.Duration {
	if c.AttemptTimeout > 0 {
		return c.AttemptTimeout
	}
	result := c.RoundPeriod * 4 / 5
	if result > 400*time.Millisecond {
		return 400 * time.Millisecond
	}
	return result
}

// Validate checks the non-network configuration boundaries.
func (c Config) Validate() error {
	if !strings.HasPrefix(c.Context, "kind-") || len(c.Context) == len("kind-") {
		return fmt.Errorf("--context is mandatory and must match ^kind-")
	}
	if c.Namespace == "" || c.Scope == "" {
		return fmt.Errorf("--namespace and --scope are mandatory")
	}
	if len(c.ExpectedNodes) == 0 {
		return fmt.Errorf("--expected-nodes is mandatory")
	}
	seen := make(map[string]struct{}, len(c.ExpectedNodes))
	for _, node := range c.ExpectedNodes {
		if node == "" {
			return fmt.Errorf("expected node name is empty")
		}
		if _, duplicate := seen[node]; duplicate {
			return fmt.Errorf("expected node %q is duplicated", node)
		}
		seen[node] = struct{}{}
	}
	if c.RoundPeriod <= 0 || c.RoundPeriod > time.Second {
		return fmt.Errorf("--round-period must be greater than zero and at most 1s")
	}
	if c.Duration <= 0 {
		return fmt.Errorf("--duration must be greater than zero")
	}
	if timeout := c.EffectiveAttemptTimeout(); timeout <= 0 || timeout >= c.RoundPeriod {
		return fmt.Errorf("attempt timeout %s must be below round period %s", timeout, c.RoundPeriod)
	}
	if c.BundleDir == "" {
		return fmt.Errorf("--bundle-dir is mandatory")
	}
	for name, port := range map[string]int{"control": c.ControlPort, "Patroni": c.PatroniPort, "Postgres": c.PGPort} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("%s port %d is outside 1..65535", name, port)
		}
	}
	if c.PreflightRounds < 1 {
		return fmt.Errorf("--preflight-rounds must be positive")
	}
	if c.LostWriteSeverity != "warn" && c.LostWriteSeverity != "fatal" {
		return fmt.Errorf("--lost-write-severity must be warn or fatal")
	}
	return nil
}
