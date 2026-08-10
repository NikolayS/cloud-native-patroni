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

// Package verdict is the oracle's pure detector. It performs no IO and reads no
// clock; fixture replay and live runs call the same Detect function.
package verdict

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/resolve"
)

// Status is the overall run outcome.
type Status string

const (
	Pass         Status = "Pass"
	Violation    Status = "Violation"
	Inconclusive Status = "Inconclusive"
	HarnessError Status = "HarnessError"
)

// RoundClass is the exhaustive classification of effective commits and
// unresolved attempts.
type RoundClass string

const (
	RoundViolation     RoundClass = "Violation"
	RoundInconclusive  RoundClass = "Inconclusive"
	RoundClean         RoundClass = "Clean"
	RoundCleanNoWriter RoundClass = "CleanNoWriter"
)

// Input contains all evidence and explicit harness taints.
type Input struct {
	Scenario          string
	ExpectedNodes     []string
	Attempts          []model.Attempt
	Invariants        []model.InvariantInstant
	Authority         []model.AuthoritySample
	Marks             []model.FaultMark
	PodIPs            map[string]string
	Taints            []string
	SamplerGaps       []time.Duration
	TTL               time.Duration
	LoopWait          time.Duration
	BundleWriteFailed bool
	LostAcknowledged  []LostWrite
	LostWriteFatal    bool
}

// LostWrite is a durability finding, never implicit safety evidence.
type LostWrite struct {
	RoundID int64  `json:"round_id"`
	NodeID  string `json:"node_id"`
}

// RoundVerdict records both strict acknowledgements and durable unacknowledged
// commits so deviation D2 remains auditable.
type RoundVerdict struct {
	RoundID          int64      `json:"round_id"`
	Class            RoundClass `json:"class"`
	Committed        []string   `json:"committed"`
	CommittedAcked   []string   `json:"committed_acked"`
	CommittedUnacked []string   `json:"committed_unacked"`
	Unresolved       []string   `json:"unresolved"`
}

// ViolationRecord identifies a detector class and its exact node evidence.
type ViolationRecord struct {
	Class    string        `json:"class"`
	Subclass string        `json:"subclass,omitempty"`
	RoundID  int64         `json:"round_id,omitempty"`
	At       model.Instant `json:"at,omitempty"`
	Nodes    []string      `json:"nodes"`
	Detail   string        `json:"detail,omitempty"`
	Fatal    bool          `json:"fatal"`
}

// RunVerdict is the fully derived output and exit status.
type RunVerdict struct {
	Status           Status            `json:"status"`
	ExitCode         int               `json:"exit_code"`
	Rounds           []RoundVerdict    `json:"rounds"`
	Violations       []ViolationRecord `json:"violations,omitempty"`
	Taints           []string          `json:"taints,omitempty"`
	LostAcknowledged []LostWrite       `json:"acked_write_lost,omitempty"`
	FenceMargin      *FenceMargin      `json:"fence_before_promote_margin,omitempty"`
}

// FenceMargin reports both the strict known-commit bound and the pessimistic
// bound that assumes unresolved boundary attempts committed.
type FenceMargin struct {
	OldPrimary    string `json:"old_primary"`
	NewPrimary    string `json:"new_primary"`
	PessimisticNS int64  `json:"pessimistic_ns"`
	OptimisticNS  int64  `json:"optimistic_ns"`
}

// ClassifyRound is structurally exhaustive for all non-negative C and U sizes.
func ClassifyRound(commits, unresolved int) RoundClass {
	switch {
	case commits >= 2:
		return RoundViolation
	case commits == 1 && unresolved >= 1:
		return RoundInconclusive
	case commits == 0 && unresolved >= 2:
		return RoundInconclusive
	case commits == 1:
		return RoundClean
	case commits == 0 && unresolved == 1:
		return RoundClean
	default:
		return RoundCleanNoWriter
	}
}

// Detect derives a verdict solely from its input evidence.
func Detect(input Input) (RunVerdict, error) {
	result := RunVerdict{Status: Pass, ExitCode: 0, LostAcknowledged: append([]LostWrite(nil), input.LostAcknowledged...)}
	if input.BundleWriteFailed {
		result.Status, result.ExitCode = HarnessError, 3
		return result, fmt.Errorf("bundle write failed")
	}

	expected, err := validateExpectedNodes(input.ExpectedNodes)
	if err != nil {
		result.Status, result.ExitCode = HarnessError, 3
		return result, err
	}
	if len(expected) < 2 {
		addTaint(&result, "EXPECTED_NODE_COUNT_LT_2")
	}
	for _, taint := range input.Taints {
		addTaint(&result, taint)
	}
	for _, gap := range input.SamplerGaps {
		if gap > 3*time.Second {
			addTaint(&result, "INVARIANT_SAMPLER_GAP_GT_3S")
		}
	}

	byRound := make(map[int64][]model.Attempt)
	seen := make(map[string]struct{}, len(input.Attempts))
	normalized := make([]model.Attempt, 0, len(input.Attempts))
	for _, rawAttempt := range input.Attempts {
		attempt := resolve.Normalize(rawAttempt)
		if attempt.RoundID <= 0 || attempt.NodeID == "" {
			result.Status, result.ExitCode = HarnessError, 3
			return result, fmt.Errorf("invalid attempt identity round=%d node=%q", attempt.RoundID, attempt.NodeID)
		}
		if !attempt.Outcome.Valid() || !attempt.Resolution.Valid() {
			result.Status, result.ExitCode = HarnessError, 3
			return result, fmt.Errorf("invalid attempt outcome or resolution for round=%d node=%q", attempt.RoundID, attempt.NodeID)
		}
		if _, ok := expected[attempt.NodeID]; !ok {
			result.Status, result.ExitCode = HarnessError, 3
			return result, fmt.Errorf("attempt node %q is outside expected set", attempt.NodeID)
		}
		key := fmt.Sprintf("%d\x00%s", attempt.RoundID, attempt.NodeID)
		if _, duplicate := seen[key]; duplicate {
			result.Status, result.ExitCode = HarnessError, 3
			return result, fmt.Errorf("duplicate attempt round=%d node=%q", attempt.RoundID, attempt.NodeID)
		}
		seen[key] = struct{}{}
		byRound[attempt.RoundID] = append(byRound[attempt.RoundID], attempt)
		normalized = append(normalized, attempt)
		if attempt.Outcome == model.Misconfigured {
			addTaint(&result, misconfigurationTaint(attempt.SQLState))
		}
	}
	if len(byRound) == 0 {
		addTaint(&result, "ZERO_ROUNDS")
	}

	roundIDs := make([]int64, 0, len(byRound))
	for roundID := range byRound {
		roundIDs = append(roundIDs, roundID)
	}
	sort.Slice(roundIDs, func(i, j int) bool { return roundIDs[i] < roundIDs[j] })
	for _, roundID := range roundIDs {
		roundVerdict := classifyAttempts(roundID, byRound[roundID])
		result.Rounds = append(result.Rounds, roundVerdict)
		switch roundVerdict.Class {
		case RoundViolation:
			result.Violations = append(result.Violations, ViolationRecord{
				Class: "V1", Subclass: v1Subclass(roundVerdict.Committed, byRound[roundID], input.Authority),
				RoundID: roundID, Nodes: append([]string(nil), roundVerdict.Committed...), Fatal: true,
			})
		case RoundInconclusive:
			addTaint(&result, fmt.Sprintf("ROUND_%d_INCONCLUSIVE", roundID))
		}
	}

	detectOverlaps(&result, normalized)
	detectInvariants(&result, input)
	detectScenario(&result, input, normalized)
	if input.LostWriteFatal && len(input.LostAcknowledged) > 0 {
		addTaint(&result, "ACKED_WRITE_LOST_FATAL")
	}

	finalize(&result)
	return result, nil
}

func detectScenario(result *RunVerdict, input Input, attempts []model.Attempt) {
	if input.Scenario != "s3-partition" {
		return
	}
	var partition model.FaultMark
	foundPartition := false
	for _, mark := range input.Marks {
		if mark.Phase == "before" && strings.HasPrefix(mark.Label, "partition-") && !strings.HasPrefix(mark.Label, "partition-heal-") {
			if !foundPartition || mark.At < partition.At {
				partition, foundPartition = mark, true
			}
		}
	}
	if !foundPartition {
		addTaint(result, "S3_PARTITION_MARK_MISSING")
		return
	}
	oldPrimary := strings.TrimPrefix(partition.Label, "partition-")
	healAt := model.Instant(1<<63 - 1)
	for _, mark := range input.Marks {
		if mark.Phase == "after" && mark.Label == "heal-partition-"+oldPrimary && mark.At >= partition.At && mark.At < healAt {
			healAt = mark.At
		}
	}

	var (
		lastOldKnown, lastOldPess   model.Instant
		firstNewKnown, firstNewPess model.Instant
		newPrimary                  string
		haveOldKnown, haveOldPess   bool
		haveNewKnown, haveNewPess   bool
	)
	for _, attempt := range attempts {
		if attempt.DispatchAt > healAt {
			continue
		}
		knownCommit := effectiveCommit(attempt)
		couldCommit := attempt.Outcome == model.InDoubt && attempt.Resolution == model.Unresolved
		if attempt.NodeID == oldPrimary {
			if knownCommit && (!haveOldKnown || attempt.SettleAt > lastOldKnown) {
				lastOldKnown, haveOldKnown = attempt.SettleAt, true
			}
			if (knownCommit || couldCommit) && (!haveOldPess || attempt.SettleAt > lastOldPess) {
				lastOldPess, haveOldPess = attempt.SettleAt, true
			}
			continue
		}
		if attempt.SettleAt < partition.At {
			continue
		}
		if knownCommit && (!haveNewKnown || attempt.SettleAt < firstNewKnown) {
			firstNewKnown, haveNewKnown, newPrimary = attempt.SettleAt, true, attempt.NodeID
		}
		if (knownCommit || couldCommit) && (!haveNewPess || attempt.SettleAt < firstNewPess) {
			firstNewPess, haveNewPess = attempt.SettleAt, true
		}
	}
	if !haveOldKnown || !haveNewKnown || !haveOldPess || !haveNewPess {
		addTaint(result, "S3_FENCE_MARGIN_UNMEASURABLE")
		return
	}
	result.FenceMargin = &FenceMargin{
		OldPrimary: oldPrimary, NewPrimary: newPrimary,
		OptimisticNS:  int64(firstNewKnown - lastOldKnown),
		PessimisticNS: int64(firstNewPess - lastOldPess),
	}
	if result.FenceMargin.OptimisticNS <= 0 {
		result.Violations = append(result.Violations, ViolationRecord{Class: "S3-FENCE", Nodes: []string{oldPrimary, newPrimary}, Fatal: true, Detail: "acknowledged fence-before-promote margin is not strictly positive"})
	} else if result.FenceMargin.PessimisticNS <= 0 {
		addTaint(result, "S3_FENCE_MARGIN_INCONCLUSIVE")
	}

	definitiveOldNegative := false
	for _, attempt := range attempts {
		if attempt.NodeID != oldPrimary || attempt.SettleAt < partition.At || attempt.SettleAt > firstNewKnown {
			continue
		}
		if attempt.Outcome == model.Unreachable || (attempt.Outcome == model.Refused && attempt.SQLState == "25006") {
			definitiveOldNegative = true
			break
		}
	}
	if !definitiveOldNegative {
		addTaint(result, "S3_OLD_PRIMARY_NONWRITABLE_NOT_PROVEN_BEFORE_NEW_ACK")
	}

	oldIP := input.PodIPs[oldPrimary]
	endpointKnown := false
	for _, invariant := range input.Invariants {
		if invariant.At < firstNewKnown || !invariant.EndpointOK {
			continue
		}
		endpointKnown = true
		if oldIP != "" && slices.Contains(invariant.EndpointAddresses, oldIP) {
			result.Violations = append(result.Violations, ViolationRecord{Class: "S3-ENDPOINT", At: invariant.At, Nodes: []string{oldPrimary}, Fatal: true, Detail: "-rw Endpoints still routes the old primary at or after the new acknowledgement"})
			break
		}
	}
	if !endpointKnown || oldIP == "" {
		addTaint(result, "S3_ENDPOINT_ROUTING_UNPROVEN")
	}

	rejoined := false
	if healAt != model.Instant(1<<63-1) {
		for _, invariant := range input.Invariants {
			state, ok := invariant.Nodes[oldPrimary]
			if invariant.At >= healAt && ok && state.RecoveryOK && state.InRecovery {
				rejoined = true
			}
		}
	}
	if !rejoined {
		addTaint(result, "S3_OLD_PRIMARY_REJOIN_NOT_PROVEN")
	}
}

func validateExpectedNodes(nodes []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if node == "" {
			return nil, fmt.Errorf("expected node name is empty")
		}
		if _, duplicate := result[node]; duplicate {
			return nil, fmt.Errorf("duplicate expected node %q", node)
		}
		result[node] = struct{}{}
	}
	return result, nil
}

func classifyAttempts(roundID int64, attempts []model.Attempt) RoundVerdict {
	result := RoundVerdict{RoundID: roundID}
	for _, attempt := range attempts {
		switch {
		case attempt.Outcome == model.Committed:
			result.CommittedAcked = append(result.CommittedAcked, attempt.NodeID)
		case attempt.Outcome == model.InDoubt && attempt.Resolution == model.ResolvedCommitted:
			result.CommittedUnacked = append(result.CommittedUnacked, attempt.NodeID)
		case attempt.Outcome == model.InDoubt && attempt.Resolution == model.Unresolved:
			result.Unresolved = append(result.Unresolved, attempt.NodeID)
		}
	}
	sort.Strings(result.CommittedAcked)
	sort.Strings(result.CommittedUnacked)
	sort.Strings(result.Unresolved)
	result.Committed = append(result.Committed, result.CommittedAcked...)
	result.Committed = append(result.Committed, result.CommittedUnacked...)
	sort.Strings(result.Committed)
	result.Class = ClassifyRound(len(result.Committed), len(result.Unresolved))
	return result
}

func v1Subclass(committers []string, attempts []model.Attempt, samples []model.AuthoritySample) string {
	claimed := 0
	for _, node := range committers {
		dispatch := model.Instant(0)
		for _, attempt := range attempts {
			if attempt.NodeID == node {
				dispatch = attempt.DispatchAt
				break
			}
		}
		claim, known := joinedClaim(node, dispatch, samples)
		if !known {
			return "V1?"
		}
		if claim == model.ClaimPrimary {
			claimed++
		}
	}
	switch {
	case claimed == len(committers):
		return "V1a"
	case claimed == 1:
		return "V1b"
	default:
		return "V1c"
	}
}

func joinedClaim(node string, dispatch model.Instant, samples []model.AuthoritySample) (model.AuthorityClaim, bool) {
	var latest model.AuthoritySample
	found := false
	for _, sample := range samples {
		if sample.NodeID != node || sample.At > dispatch || (found && sample.At <= latest.At) {
			continue
		}
		latest, found = sample, true
	}
	if !found || dispatch-latest.At > model.Instant(2*time.Second) || latest.Claim == model.ClaimUnknown {
		return model.ClaimUnknown, false
	}
	return latest.Claim, true
}

func effectiveCommit(attempt model.Attempt) bool {
	return attempt.Outcome == model.Committed || (attempt.Outcome == model.InDoubt && attempt.Resolution == model.ResolvedCommitted)
}

func detectOverlaps(result *RunVerdict, attempts []model.Attempt) {
	for leftIndex, left := range attempts {
		if !effectiveCommit(left) {
			continue
		}
		for _, right := range attempts[leftIndex+1:] {
			if left.NodeID == right.NodeID || left.RoundID == right.RoundID || !effectiveCommit(right) {
				continue
			}
			if left.DispatchAt <= right.SettleAt && right.DispatchAt <= left.SettleAt {
				nodes := []string{left.NodeID, right.NodeID}
				sort.Strings(nodes)
				result.Violations = append(result.Violations, ViolationRecord{
					Class: "V1-EXT", Nodes: nodes, Fatal: true,
					Detail: fmt.Sprintf("overlapping committed attempts in rounds %d and %d", left.RoundID, right.RoundID),
				})
			}
		}
	}
}

func detectInvariants(result *RunVerdict, input Input) {
	invariants := append([]model.InvariantInstant(nil), input.Invariants...)
	sort.Slice(invariants, func(i, j int) bool { return invariants[i].At < invariants[j].At })
	threshold := input.TTL + input.LoopWait
	if threshold <= 0 {
		threshold = 40 * time.Second
	}
	unfencedSince := make(map[string]model.Instant)
	unfencedActive := make(map[string]bool)
	v3Fired := make(map[string]bool)
	v5Seen := make(map[string]bool)
	var previousFaultPostgres model.Instant
	havePreviousFaultPostgres := false

	for _, instant := range invariants {
		if instant.PostgresSample && instant.FaultWindow {
			if havePreviousFaultPostgres && time.Duration(instant.At-previousFaultPostgres) > 3*time.Second {
				addTaint(result, "INVARIANT_SAMPLER_GAP_GT_3S")
			}
			previousFaultPostgres, havePreviousFaultPostgres = instant.At, true
		} else if instant.PostgresSample {
			havePreviousFaultPostgres = false
		}
		writable := make([]string, 0, len(instant.Nodes))
		unknownInFault := false
		for node, state := range instant.Nodes {
			if state.RecoveryOK {
				if !state.InRecovery {
					writable = append(writable, node)
				}
			} else if instant.FaultWindow && instant.PostgresSample {
				unknownInFault = true
			}
			if state.PID1OK && state.PID1Comm != "patroni" {
				result.Violations = appendUniqueViolation(result.Violations, ViolationRecord{Class: "V4", At: instant.At, Nodes: []string{node}, Fatal: true, Detail: "database container PID 1 is " + state.PID1Comm})
			}
			if state.EndpointManagersOK {
				for _, manager := range state.EndpointManagers {
					if !strings.Contains(strings.ToLower(manager), "patroni") && !v5Seen[manager] {
						v5Seen[manager] = true
						result.Violations = append(result.Violations, ViolationRecord{Class: "V5", At: instant.At, Nodes: []string{node}, Fatal: false, Detail: "foreign Endpoints manager: " + manager})
					}
				}
			}

			if !state.RecoveryOK || !state.DCSOK {
				continue
			}
			unfenced := !state.InRecovery && state.DCSLeader != node
			failsafe := state.FailsafeOK && state.FailsafeConfirmed
			if !unfenced || failsafe {
				unfencedActive[node] = false
				continue
			}
			if !unfencedActive[node] {
				unfencedSince[node], unfencedActive[node] = instant.At, true
				continue
			}
			if !v3Fired[node] && time.Duration(instant.At-unfencedSince[node]) > threshold {
				v3Fired[node] = true
				result.Violations = append(result.Violations, ViolationRecord{Class: "V3", At: instant.At, Nodes: []string{node}, Fatal: true, Detail: "writable without matching DCS leader beyond ttl + loop_wait"})
			}
		}
		if unknownInFault {
			addTaint(result, "UNKNOWN_POSTGRES_STATE_IN_FAULT_WINDOW")
		}
		if len(writable) >= 2 {
			sort.Strings(writable)
			result.Violations = append(result.Violations, ViolationRecord{Class: "V2", At: instant.At, Nodes: writable, Fatal: true})
		}
	}
}

func appendUniqueViolation(records []ViolationRecord, candidate ViolationRecord) []ViolationRecord {
	for _, record := range records {
		if record.Class == candidate.Class && slices.Equal(record.Nodes, candidate.Nodes) {
			return records
		}
	}
	return append(records, candidate)
}

func addTaint(result *RunVerdict, taint string) {
	if taint != "" && !slices.Contains(result.Taints, taint) {
		result.Taints = append(result.Taints, taint)
	}
}

func misconfigurationTaint(sqlState string) string {
	names := map[string]string{
		"42501": "insufficient_privilege",
		"42P01": "undefined_table",
		"3D000": "invalid_catalog_name",
		"28P01": "invalid_password",
		"23505": "unique_violation",
	}
	if name, ok := names[sqlState]; ok {
		return "MISCONFIGURED_" + sqlState + "_" + name
	}
	if sqlState == "" {
		return "MISCONFIGURED_NO_SQLSTATE"
	}
	return "MISCONFIGURED_" + sqlState
}

func finalize(result *RunVerdict) {
	fatal := slices.ContainsFunc(result.Violations, func(record ViolationRecord) bool { return record.Fatal })
	sort.Strings(result.Taints)
	switch {
	case fatal:
		result.Status, result.ExitCode = Violation, 1
	case len(result.Taints) > 0:
		result.Status, result.ExitCode = Inconclusive, 2
	default:
		result.Status, result.ExitCode = Pass, 0
	}
}
