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
	"fmt"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

// RoundEvidence carries the preflight barrier output.
type RoundEvidence struct {
	Attempts     []model.Attempt
	DispatchSkew time.Duration
	Lineages     map[string]lineageToken
}

// ValidateSteadyState prevents a zero-writer or misconfigured cluster from
// becoming a silent safety rubber stamp.
func ValidateSteadyState(expected []string, rounds []RoundEvidence, required int) error {
	if len(rounds) != required {
		return fmt.Errorf("steady-state rounds=%d, want exactly %d", len(rounds), required)
	}
	for index, round := range rounds {
		if round.DispatchSkew > 5*time.Millisecond {
			return fmt.Errorf("steady-state round %d dispatch skew %s exceeds 5ms", index+1, round.DispatchSkew)
		}
		if len(round.Attempts) != len(expected) {
			return fmt.Errorf("steady-state round %d attempts=%d, want %d", index+1, len(round.Attempts), len(expected))
		}
		commits, refusals := 0, 0
		seen := make(map[string]struct{}, len(expected))
		for _, attempt := range round.Attempts {
			if _, duplicate := seen[attempt.NodeID]; duplicate {
				return fmt.Errorf("steady-state round %d duplicates node %q", index+1, attempt.NodeID)
			}
			seen[attempt.NodeID] = struct{}{}
			switch attempt.Outcome {
			case model.Committed:
				commits++
			case model.Refused:
				if attempt.SQLState != "25006" {
					return fmt.Errorf("steady-state round %d node %q refused with SQLSTATE %q, want 25006", index+1, attempt.NodeID, attempt.SQLState)
				}
				refusals++
			default:
				return fmt.Errorf("steady-state round %d node %q outcome=%s", index+1, attempt.NodeID, attempt.Outcome)
			}
		}
		if commits != 1 || refusals != len(expected)-1 {
			return fmt.Errorf("steady-state round %d commits=%d refusals=%d, want 1 and %d", index+1, commits, refusals, len(expected)-1)
		}
	}
	return nil
}
