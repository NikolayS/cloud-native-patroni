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

// Package model contains the oracle's IO-free evidence model.
package model

import "time"

// Instant is nanoseconds since the oracle's monotonic run origin. It is the
// only clock used for ordering and overlap decisions. Database wall clocks are
// deliberately excluded from those decisions.
type Instant int64

// Outcome is the closed set of classifications for a direct-Pod write.
type Outcome string

const (
	Committed     Outcome = "Committed"
	Refused       Outcome = "Refused"
	Unreachable   Outcome = "Unreachable"
	InDoubt       Outcome = "InDoubt"
	NoAttempt     Outcome = "NoAttempt"
	Misconfigured Outcome = "Misconfigured"
)

// Valid reports whether the outcome belongs to the closed outcome lattice.
func (o Outcome) Valid() bool {
	switch o {
	case Committed, Refused, Unreachable, InDoubt, NoAttempt, Misconfigured:
		return true
	default:
		return false
	}
}

// Resolution records what a separate lookup established after an in-doubt
// write. ResolutionNone means the resolver has not produced evidence.
type Resolution string

const (
	ResolutionNone    Resolution = ""
	ResolvedCommitted Resolution = "ResolvedCommitted"
	ResolvedAbsent    Resolution = "ResolvedAbsent"
	Unresolved        Resolution = "Unresolved"
)

// Valid reports whether the resolution is representable.
func (r Resolution) Valid() bool {
	switch r {
	case ResolutionNone, ResolvedCommitted, ResolvedAbsent, Unresolved:
		return true
	default:
		return false
	}
}

// Attempt is the complete client-side record of one node in one round.
type Attempt struct {
	RoundID      int64      `json:"round_id"`
	NodeID       string     `json:"node_id"`
	DispatchAt   Instant    `json:"dispatch_at"`
	SettleAt     Instant    `json:"settle_at"`
	Outcome      Outcome    `json:"outcome"`
	SQLState     string     `json:"sqlstate,omitempty"`
	Error        string     `json:"error,omitempty"`
	Resolution   Resolution `json:"resolution,omitempty"`
	LineageOK    bool       `json:"lineage_ok"`
	ObservedAt   *time.Time `json:"observed_at,omitempty"`
	ResolverNote string     `json:"resolver_evidence,omitempty"`
}

// AuthorityClaim is Patroni's sampled claim, used only to diagnose a V1
// finding. It never gates the fatal write evidence.
type AuthorityClaim string

const (
	ClaimUnknown    AuthorityClaim = "unknown"
	ClaimPrimary    AuthorityClaim = "primary"
	ClaimNotPrimary AuthorityClaim = "not_primary"
)

// AuthoritySample is a direct Patroni observation on the oracle clock.
type AuthoritySample struct {
	NodeID string         `json:"node_id"`
	At     Instant        `json:"at"`
	Claim  AuthorityClaim `json:"claim"`
}

// NodeInvariant is the typed view over raw invariant samples. Every source has
// an explicit OK bit; failed or unrecognised values remain unknown.
type NodeInvariant struct {
	RecoveryOK         bool     `json:"recovery_ok"`
	InRecovery         bool     `json:"in_recovery"`
	DCSOK              bool     `json:"dcs_ok"`
	DCSLeader          string   `json:"dcs_leader,omitempty"`
	FailsafeOK         bool     `json:"failsafe_ok"`
	FailsafeConfirmed  bool     `json:"failsafe_confirmed"`
	PID1OK             bool     `json:"pid1_ok"`
	PID1Comm           string   `json:"pid1_comm,omitempty"`
	EndpointManagersOK bool     `json:"endpoint_managers_ok"`
	EndpointManagers   []string `json:"endpoint_managers,omitempty"`
}

// InvariantInstant groups simultaneous node views on the oracle clock.
type InvariantInstant struct {
	At                Instant                  `json:"at"`
	FaultWindow       bool                     `json:"fault_window"`
	PostgresSample    bool                     `json:"postgres_sample"`
	EndpointOK        bool                     `json:"endpoint_ok"`
	EndpointAddresses []string                 `json:"endpoint_addresses,omitempty"`
	Nodes             map[string]NodeInvariant `json:"nodes"`
}

// FaultMark brackets a driver action on the oracle clock.
type FaultMark struct {
	At    Instant `json:"at"`
	Label string  `json:"label"`
	Phase string  `json:"phase"`
}
