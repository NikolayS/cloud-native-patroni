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

// Package scenario validates scenario boundaries and names run bundles.
package scenario

import (
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

const partitionHardCap = 240 * time.Second

var safeScenario = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidatePartitionDuration enforces the 240 s cap unless a Pod explicitly
// tolerates unreachable longer than the requested partition.
func ValidatePartitionDuration(duration time.Duration, tolerationSeconds *int64) error {
	if duration <= 0 {
		return fmt.Errorf("partition duration must be positive")
	}
	if duration <= partitionHardCap {
		return nil
	}
	if tolerationSeconds == nil {
		return fmt.Errorf("partition duration %s exceeds the 240s hard cap without an explicit toleration", duration)
	}
	if time.Duration(*tolerationSeconds)*time.Second <= duration {
		return fmt.Errorf("unreachable toleration %ds is not longer than partition duration %s", *tolerationSeconds, duration)
	}
	return nil
}

// RunID returns <scenario>-<UTC basic timestamp>-<6 hex>.
func RunID(scenarioName string, started time.Time, random []byte) (string, error) {
	if !safeScenario.MatchString(scenarioName) {
		return "", fmt.Errorf("scenario %q is not a safe run-id component", scenarioName)
	}
	if len(random) != 3 {
		return "", fmt.Errorf("run ID requires exactly 3 random bytes, got %d", len(random))
	}
	return fmt.Sprintf("%s-%s-%s", scenarioName, started.UTC().Format("20060102T150405Z"), hex.EncodeToString(random)), nil
}
