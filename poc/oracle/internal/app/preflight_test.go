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

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

func steadyRound(roundID int64) RoundEvidence {
	return RoundEvidence{Attempts: []model.Attempt{
		{RoundID: roundID, NodeID: "a", Outcome: model.Committed},
		{RoundID: roundID, NodeID: "b", Outcome: model.Refused, SQLState: "25006"},
		{RoundID: roundID, NodeID: "c", Outcome: model.Refused, SQLState: "25006"},
	}, DispatchSkew: 5 * time.Millisecond}
}

func TestValidateSteadyState(t *testing.T) {
	if err := ValidateSteadyState([]string{"a", "b", "c"}, []RoundEvidence{steadyRound(1), steadyRound(2)}, 2); err != nil {
		t.Fatalf("ValidateSteadyState() error = %v", err)
	}
}

func TestValidateSteadyStateRejectsRubberStamp(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RoundEvidence)
	}{
		{name: "zero writer", mutate: func(round *RoundEvidence) {
			round.Attempts[0].Outcome = model.Refused
			round.Attempts[0].SQLState = "25006"
		}},
		{name: "wrong refusal", mutate: func(round *RoundEvidence) { round.Attempts[1].SQLState = "42501" }},
		{name: "no attempt", mutate: func(round *RoundEvidence) { round.Attempts[2].Outcome = model.NoAttempt }},
		{name: "skew over boundary", mutate: func(round *RoundEvidence) { round.DispatchSkew = 5*time.Millisecond + time.Nanosecond }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			round := steadyRound(1)
			tt.mutate(&round)
			if err := ValidateSteadyState([]string{"a", "b", "c"}, []RoundEvidence{round}, 1); err == nil {
				t.Fatal("ValidateSteadyState() error = nil, want rejection")
			}
		})
	}
}

func TestValidateSteadyStateRequiresExactRoundCount(t *testing.T) {
	if err := ValidateSteadyState([]string{"a", "b", "c"}, []RoundEvidence{steadyRound(1)}, 2); err == nil {
		t.Fatal("ValidateSteadyState() error = nil, want missing-round rejection")
	}
}
