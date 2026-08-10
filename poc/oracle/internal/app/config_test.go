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

package app

import (
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		Context: "kind-demo", Namespace: "database", Scope: "cluster-rw",
		ExpectedNodes: []string{"cluster-1", "cluster-2", "cluster-3"},
		RoundPeriod:   500 * time.Millisecond, Duration: time.Minute,
		BundleDir: "/bundle", ControlPort: 9099, PatroniPort: 8008, PGPort: 5432,
		PreflightRounds: 20, LostWriteSeverity: "warn",
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "valid"},
		{name: "one second boundary", mutate: func(c *Config) { c.RoundPeriod = time.Second }},
		{name: "missing context", mutate: func(c *Config) { c.Context = "" }, wantErr: true},
		{name: "gke context", mutate: func(c *Config) { c.Context = "gke_project_zone_cluster" }, wantErr: true},
		{name: "period over one second", mutate: func(c *Config) { c.RoundPeriod = time.Second + time.Nanosecond }, wantErr: true},
		{name: "zero period", mutate: func(c *Config) { c.RoundPeriod = 0 }, wantErr: true},
		{name: "zero duration", mutate: func(c *Config) { c.Duration = 0 }, wantErr: true},
		{name: "duplicate expected node", mutate: func(c *Config) { c.ExpectedNodes[2] = c.ExpectedNodes[1] }, wantErr: true},
		{name: "empty namespace", mutate: func(c *Config) { c.Namespace = "" }, wantErr: true},
		{name: "invalid severity", mutate: func(c *Config) { c.LostWriteSeverity = "ignore" }, wantErr: true},
		{name: "attempt deadline not below period", mutate: func(c *Config) { c.AttemptTimeout = c.RoundPeriod }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := validConfig()
			if tt.mutate != nil {
				tt.mutate(&config)
			}
			err := config.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAttemptTimeoutDefaultIsBelowRoundPeriod(t *testing.T) {
	config := validConfig()
	if got := config.EffectiveAttemptTimeout(); got != 400*time.Millisecond {
		t.Fatalf("EffectiveAttemptTimeout() = %s, want 400ms", got)
	}
	config.RoundPeriod = 100 * time.Millisecond
	if got := config.EffectiveAttemptTimeout(); got != 80*time.Millisecond {
		t.Fatalf("EffectiveAttemptTimeout() = %s, want 80ms", got)
	}
}
