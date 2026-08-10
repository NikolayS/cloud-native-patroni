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

package verdict

import (
	"slices"
	"testing"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

func attempt(round int64, node string, outcome model.Outcome) model.Attempt {
	return model.Attempt{
		RoundID: round, NodeID: node, Outcome: outcome,
		DispatchAt: model.Instant(round*1_000_000 + 100),
		SettleAt:   model.Instant(round*1_000_000 + 900),
		LineageOK:  true,
	}
}

func detect(t *testing.T, attempts ...model.Attempt) RunVerdict {
	t.Helper()
	got, err := Detect(Input{ExpectedNodes: []string{"pod-a", "pod-b", "pod-c"}, Attempts: attempts})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	return got
}

func TestDualCommitIsV1(t *testing.T) {
	got := detect(t, attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Committed), attempt(1, "pod-c", model.Refused))
	if got.Status != Violation || got.ExitCode != 1 {
		t.Fatalf("verdict = %s exit %d, want Violation exit 1", got.Status, got.ExitCode)
	}
	if len(got.Rounds) != 1 || got.Rounds[0].Class != RoundViolation || !slices.Equal(got.Rounds[0].CommittedAcked, []string{"pod-a", "pod-b"}) {
		t.Fatalf("round verdict = %#v", got.Rounds)
	}
	if !hasViolation(got, "V1") {
		t.Fatalf("violations = %#v, want V1", got.Violations)
	}
}

func TestRawInDoubtIsNotCommit(t *testing.T) {
	a := attempt(1, "pod-a", model.InDoubt)
	a.Resolution = model.ResolutionNone
	got := detect(t, a, attempt(1, "pod-b", model.Refused), attempt(1, "pod-c", model.Refused))
	if got.Rounds[0].Class != RoundCleanNoWriter || len(got.Rounds[0].Committed) != 0 || len(got.Rounds[0].Unresolved) != 0 {
		t.Fatalf("round = %#v, want CleanNoWriter with no effective commit", got.Rounds[0])
	}
}

func TestCommitAndRefusedIsClean(t *testing.T) {
	b := attempt(1, "pod-b", model.Refused)
	b.SQLState = "25006"
	c := attempt(1, "pod-c", model.Refused)
	c.SQLState = "25006"
	got := detect(t, attempt(1, "pod-a", model.Committed), b, c)
	if got.Status != Pass || got.ExitCode != 0 || got.Rounds[0].Class != RoundClean {
		t.Fatalf("verdict = %#v, want pass", got)
	}
}

func TestCommitAndUnresolvedIsInconclusive(t *testing.T) {
	b := attempt(1, "pod-b", model.InDoubt)
	b.Resolution = model.Unresolved
	got := detect(t, attempt(1, "pod-a", model.Committed), b, attempt(1, "pod-c", model.Refused))
	if got.Status != Inconclusive || got.ExitCode != 2 || got.Rounds[0].Class != RoundInconclusive {
		t.Fatalf("verdict = %#v, want inconclusive", got)
	}
}

func TestResolvedCommitCountsAsWriter(t *testing.T) {
	b := attempt(1, "pod-b", model.InDoubt)
	b.Resolution = model.ResolvedCommitted
	got := detect(t, attempt(1, "pod-a", model.Committed), b, attempt(1, "pod-c", model.Refused))
	if got.Status != Violation || !slices.Equal(got.Rounds[0].CommittedUnacked, []string{"pod-b"}) {
		t.Fatalf("verdict = %#v, want violation with pod-b unacked", got)
	}
}

func TestResolvedAbsentWithLineageIsClean(t *testing.T) {
	b := attempt(1, "pod-b", model.InDoubt)
	b.Resolution = model.ResolvedAbsent
	got := detect(t, attempt(1, "pod-a", model.Committed), b, attempt(1, "pod-c", model.Refused))
	if got.Status != Pass || got.Rounds[0].Class != RoundClean {
		t.Fatalf("verdict = %#v, want pass", got)
	}
}

func TestResolvedAbsentWithChangedLineageIsUnresolved(t *testing.T) {
	b := attempt(1, "pod-b", model.InDoubt)
	b.Resolution = model.ResolvedAbsent
	b.LineageOK = false
	got := detect(t, attempt(1, "pod-a", model.Committed), b, attempt(1, "pod-c", model.Refused))
	if got.Status != Inconclusive || !slices.Equal(got.Rounds[0].Unresolved, []string{"pod-b"}) {
		t.Fatalf("verdict = %#v, want pod-b unresolved", got)
	}
}

func TestCleanNoWriter(t *testing.T) {
	got := detect(t, attempt(1, "pod-a", model.Refused), attempt(1, "pod-b", model.Unreachable), attempt(1, "pod-c", model.NoAttempt))
	if got.Status != Pass || got.Rounds[0].Class != RoundCleanNoWriter {
		t.Fatalf("verdict = %#v, want pass with CleanNoWriter", got)
	}
}

func TestThreeCommittersAreAllNamed(t *testing.T) {
	got := detect(t, attempt(1, "pod-c", model.Committed), attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Committed))
	if got.Status != Violation || !slices.Equal(got.Rounds[0].Committed, []string{"pod-a", "pod-b", "pod-c"}) {
		t.Fatalf("verdict = %#v", got)
	}
}

func TestMisconfigurationTaintsRun(t *testing.T) {
	b := attempt(1, "pod-b", model.Misconfigured)
	b.SQLState = "42501"
	got := detect(t, attempt(1, "pod-a", model.Committed), b, attempt(1, "pod-c", model.Refused))
	if got.Status != Inconclusive || got.ExitCode != 2 || !slices.Contains(got.Taints, "MISCONFIGURED_42501_insufficient_privilege") {
		t.Fatalf("verdict = %#v, want named 42501 taint", got)
	}
}

func TestZeroRoundsIsInconclusive(t *testing.T) {
	got := detect(t)
	if got.Status != Inconclusive || got.ExitCode != 2 || !slices.Contains(got.Taints, "ZERO_ROUNDS") {
		t.Fatalf("verdict = %#v", got)
	}
}

func TestSingleExpectedNodeCannotPass(t *testing.T) {
	got, err := Detect(Input{ExpectedNodes: []string{"pod-a"}, Attempts: []model.Attempt{attempt(1, "pod-a", model.Committed)}})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Inconclusive || got.ExitCode != 2 || !slices.Contains(got.Taints, "EXPECTED_NODE_COUNT_LT_2") {
		t.Fatalf("verdict = %#v", got)
	}
}

func TestDuplicateAttemptIsHarnessError(t *testing.T) {
	a := attempt(1, "pod-a", model.Committed)
	got, err := Detect(Input{ExpectedNodes: []string{"pod-a", "pod-b"}, Attempts: []model.Attempt{a, a}})
	if err == nil {
		t.Fatal("Detect() error = nil, want duplicate error")
	}
	if got.ExitCode != 3 || got.Status != HarnessError {
		t.Fatalf("verdict = %#v, want harness error exit 3", got)
	}
}

func TestObservedAtCannotAffectVerdict(t *testing.T) {
	past := time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC)
	a := attempt(1, "pod-a", model.Committed)
	b := attempt(1, "pod-b", model.Committed)
	a.ObservedAt = &past
	b.ObservedAt = &future
	got := detect(t, a, b, attempt(1, "pod-c", model.Refused))
	if got.Status != Violation || !hasViolation(got, "V1") {
		t.Fatalf("verdict = %#v, node clocks masked violation", got)
	}
}

func TestOverlappingCommitsAcrossRoundsAreV1EXT(t *testing.T) {
	a := attempt(1, "pod-a", model.Committed)
	a.DispatchAt, a.SettleAt = 100, 300
	b := attempt(2, "pod-b", model.Committed)
	b.DispatchAt, b.SettleAt = 250, 400
	got := detect(t, a, b)
	if got.Status != Violation || !hasViolation(got, "V1-EXT") {
		t.Fatalf("verdict = %#v, want V1-EXT", got)
	}
}

func TestNonOverlappingCommitsDoNotFireV1EXT(t *testing.T) {
	a := attempt(1, "pod-a", model.Committed)
	a.DispatchAt, a.SettleAt = 100, 200
	b := attempt(2, "pod-b", model.Committed)
	b.DispatchAt, b.SettleAt = 201, 400
	got := detect(t, a, b)
	if got.Status != Pass || hasViolation(got, "V1-EXT") {
		t.Fatalf("verdict = %#v, want pass", got)
	}
}

func TestRoundClassifierExhaustive(t *testing.T) {
	for commits := 0; commits <= 3; commits++ {
		for unresolved := 0; unresolved <= 3; unresolved++ {
			got := ClassifyRound(commits, unresolved)
			if got == "" {
				t.Fatalf("ClassifyRound(%d, %d) returned empty", commits, unresolved)
			}
		}
	}
	if got := ClassifyRound(2, 0); got != RoundViolation {
		t.Fatalf("minimum violation = %q, want %q", got, RoundViolation)
	}
	if got := ClassifyRound(1, 1); got != RoundInconclusive {
		t.Fatalf("minimum inconclusive = %q, want %q", got, RoundInconclusive)
	}
	if got := ClassifyRound(0, 0); got != RoundCleanNoWriter {
		t.Fatalf("empty round = %q, want %q", got, RoundCleanNoWriter)
	}
}

func TestExactlyOneRoundCanPass(t *testing.T) {
	got, err := Detect(Input{ExpectedNodes: []string{"pod-a", "pod-b"}, Attempts: []model.Attempt{attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Refused)}})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Pass || len(got.Rounds) != 1 {
		t.Fatalf("verdict = %#v, want one-round pass", got)
	}
}

func TestDualWritableIsV2(t *testing.T) {
	got, err := Detect(Input{
		ExpectedNodes: []string{"pod-a", "pod-b"},
		Attempts:      []model.Attempt{attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Refused)},
		Invariants: []model.InvariantInstant{{At: 100, Nodes: map[string]model.NodeInvariant{
			"pod-a": {RecoveryOK: true, InRecovery: false},
			"pod-b": {RecoveryOK: true, InRecovery: false},
		}}},
	})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Violation || !hasViolation(got, "V2") {
		t.Fatalf("verdict = %#v, want V2", got)
	}
}

func TestUnknownRecoveryInsideFaultWindowTaints(t *testing.T) {
	got, err := Detect(Input{
		ExpectedNodes: []string{"pod-a", "pod-b"},
		Attempts:      []model.Attempt{attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Refused)},
		Invariants: []model.InvariantInstant{{At: 100, FaultWindow: true, PostgresSample: true, Nodes: map[string]model.NodeInvariant{
			"pod-a": {RecoveryOK: true, InRecovery: false},
			"pod-b": {RecoveryOK: false},
		}}},
	})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Inconclusive || !slices.Contains(got.Taints, "UNKNOWN_POSTGRES_STATE_IN_FAULT_WINDOW") {
		t.Fatalf("verdict = %#v", got)
	}
}

func TestInvariantSamplerGapBoundary(t *testing.T) {
	base := Input{
		ExpectedNodes: []string{"pod-a", "pod-b"},
		Attempts:      []model.Attempt{attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Refused)},
		Invariants: []model.InvariantInstant{
			{At: 1, FaultWindow: true, PostgresSample: true, Nodes: map[string]model.NodeInvariant{"pod-a": {RecoveryOK: true}, "pod-b": {RecoveryOK: true, InRecovery: true}}},
			{At: model.Instant(3*time.Second) + 1, FaultWindow: true, PostgresSample: true, Nodes: map[string]model.NodeInvariant{"pod-a": {RecoveryOK: true}, "pod-b": {RecoveryOK: true, InRecovery: true}}},
		},
	}
	got, err := Detect(base)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Pass || slices.Contains(got.Taints, "INVARIANT_SAMPLER_GAP_GT_3S") {
		t.Fatalf("exact 3s verdict = %#v", got)
	}
	base.Invariants[1].At++
	got, err = Detect(base)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Inconclusive || !slices.Contains(got.Taints, "INVARIANT_SAMPLER_GAP_GT_3S") {
		t.Fatalf("over 3s verdict = %#v", got)
	}
}

func TestPid1NotPatroniIsV4(t *testing.T) {
	got, err := Detect(Input{
		ExpectedNodes: []string{"pod-a", "pod-b"},
		Attempts:      []model.Attempt{attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Refused)},
		Invariants: []model.InvariantInstant{{At: 100, Nodes: map[string]model.NodeInvariant{
			"pod-a": {PID1OK: true, PID1Comm: "sh"},
			"pod-b": {PID1OK: true, PID1Comm: "patroni"},
		}}},
	})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Violation || !hasViolation(got, "V4") {
		t.Fatalf("verdict = %#v, want V4", got)
	}
}

func TestV3ThresholdAndFailsafe(t *testing.T) {
	base := Input{
		ExpectedNodes: []string{"pod-a", "pod-b"},
		Attempts:      []model.Attempt{attempt(1, "pod-a", model.Committed), attempt(1, "pod-b", model.Refused)},
		TTL:           30 * time.Second, LoopWait: 10 * time.Second,
	}
	base.Invariants = []model.InvariantInstant{
		{At: 0, Nodes: map[string]model.NodeInvariant{"pod-a": {RecoveryOK: true, InRecovery: false, DCSOK: true, DCSLeader: "pod-b"}}},
		{At: model.Instant(40 * time.Second), Nodes: map[string]model.NodeInvariant{"pod-a": {RecoveryOK: true, InRecovery: false, DCSOK: true, DCSLeader: "pod-b"}}},
	}
	got, err := Detect(base)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if hasViolation(got, "V3") {
		t.Fatalf("exact 40s fired V3: %#v", got)
	}
	base.Invariants[1].At++
	got, err = Detect(base)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !hasViolation(got, "V3") {
		t.Fatalf("over 40s did not fire V3: %#v", got)
	}
	state := base.Invariants[1].Nodes["pod-a"]
	state.FailsafeOK, state.FailsafeConfirmed = true, true
	base.Invariants[1].Nodes["pod-a"] = state
	state = base.Invariants[0].Nodes["pod-a"]
	state.FailsafeOK, state.FailsafeConfirmed = true, true
	base.Invariants[0].Nodes["pod-a"] = state
	got, err = Detect(base)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if hasViolation(got, "V3") {
		t.Fatalf("confirmed failsafe fired V3: %#v", got)
	}
}

func TestAuthorityJoinOnlyAffectsV1Subclass(t *testing.T) {
	a := attempt(1, "pod-a", model.Committed)
	b := attempt(1, "pod-b", model.Committed)
	a.DispatchAt, b.DispatchAt = model.Instant(5*time.Second), model.Instant(5*time.Second)
	got, err := Detect(Input{
		ExpectedNodes: []string{"pod-a", "pod-b"}, Attempts: []model.Attempt{a, b},
		Authority: []model.AuthoritySample{
			{NodeID: "pod-a", At: model.Instant(4 * time.Second), Claim: model.ClaimPrimary},
			{NodeID: "pod-b", At: model.Instant(1 * time.Second), Claim: model.ClaimPrimary},
		},
	})
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Violation || violationSubclass(got, "V1") != "V1?" {
		t.Fatalf("verdict = %#v, want fatal V1?", got)
	}
}

func s3Input() Input {
	oldAck := attempt(1, "pod-a", model.Committed)
	oldAck.DispatchAt, oldAck.SettleAt = 100, 150
	oldRefused := attempt(2, "pod-a", model.Refused)
	oldRefused.DispatchAt, oldRefused.SettleAt, oldRefused.SQLState = 160, 170, "25006"
	newAck := attempt(2, "pod-b", model.Committed)
	newAck.DispatchAt, newAck.SettleAt = 180, 250
	return Input{
		Scenario: "s3-partition", ExpectedNodes: []string{"pod-a", "pod-b", "pod-c"},
		Attempts: []model.Attempt{oldAck, oldRefused, newAck, attempt(1, "pod-b", model.Refused), attempt(1, "pod-c", model.Refused), attempt(2, "pod-c", model.Refused)},
		Marks:    []model.FaultMark{{At: 90, Label: "partition-pod-a", Phase: "before"}, {At: 300, Label: "heal-partition-pod-a", Phase: "after"}},
		PodIPs:   map[string]string{"pod-a": "10.244.1.2", "pod-b": "10.244.2.2", "pod-c": "10.244.3.2"},
		Invariants: []model.InvariantInstant{
			{At: 250, EndpointOK: true, EndpointAddresses: []string{"10.244.2.2"}, Nodes: map[string]model.NodeInvariant{"pod-a": {RecoveryOK: true, InRecovery: false}}},
			{At: 350, EndpointOK: true, EndpointAddresses: []string{"10.244.2.2"}, Nodes: map[string]model.NodeInvariant{"pod-a": {RecoveryOK: true, InRecovery: true}}},
		},
	}
}

func TestS3PositivePessimisticMarginPasses(t *testing.T) {
	input := s3Input()
	got, err := Detect(input)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Pass || got.FenceMargin == nil || got.FenceMargin.PessimisticNS != 100 || got.FenceMargin.OptimisticNS != 100 {
		t.Fatalf("verdict = %#v, want 100ns positive margin", got)
	}
}

func TestS3BoundaryUnknownMakesMarginInconclusive(t *testing.T) {
	input := s3Input()
	unknown := attempt(3, "pod-a", model.InDoubt)
	unknown.DispatchAt, unknown.SettleAt, unknown.Resolution = 220, 275, model.Unresolved
	input.Attempts = append(input.Attempts, unknown)
	got, err := Detect(input)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Inconclusive || got.FenceMargin == nil || got.FenceMargin.PessimisticNS != -25 || got.FenceMargin.OptimisticNS != 100 || !slices.Contains(got.Taints, "S3_FENCE_MARGIN_INCONCLUSIVE") {
		t.Fatalf("verdict = %#v", got)
	}
}

func TestS3NonPositiveAcknowledgedMarginIsViolation(t *testing.T) {
	input := s3Input()
	for index := range input.Attempts {
		if input.Attempts[index].NodeID == "pod-a" && input.Attempts[index].Outcome == model.Committed {
			input.Attempts[index].SettleAt = 260
		}
	}
	got, err := Detect(input)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Violation || got.FenceMargin == nil || got.FenceMargin.OptimisticNS != -10 || !hasViolation(got, "S3-FENCE") {
		t.Fatalf("verdict = %#v", got)
	}
}

func TestS3EndpointStillRoutesOldPrimaryIsViolation(t *testing.T) {
	input := s3Input()
	input.Invariants[0].EndpointAddresses = []string{"10.244.1.2", "10.244.2.2"}
	got, err := Detect(input)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if got.Status != Violation || !hasViolation(got, "S3-ENDPOINT") {
		t.Fatalf("verdict = %#v", got)
	}
}

func hasViolation(got RunVerdict, class string) bool {
	return slices.ContainsFunc(got.Violations, func(v ViolationRecord) bool { return v.Class == class })
}

func violationSubclass(got RunVerdict, class string) string {
	for _, violation := range got.Violations {
		if violation.Class == class {
			return violation.Subclass
		}
	}
	return ""
}
