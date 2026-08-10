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

// Package resolve owns the pure in-doubt resolution decision and its bounded
// retry schedule.
package resolve

import (
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

const maxBackoff = 5 * time.Second

// Decide converts row-presence and lineage evidence into a resolution. Row
// presence is positive evidence even if later lineage evidence is uncertain;
// absence is definitive only while lineage is intact.
func Decide(present, lineageOK bool) model.Resolution {
	if present {
		return model.ResolvedCommitted
	}
	if lineageOK {
		return model.ResolvedAbsent
	}
	return model.Unresolved
}

// Normalize enforces the lineage rule in the pure package so no IO adapter can
// accidentally treat an erased row as proof of non-commit.
func Normalize(attempt model.Attempt) model.Attempt {
	if attempt.Outcome == model.InDoubt && attempt.Resolution == model.ResolvedAbsent && !attempt.LineageOK {
		attempt.Resolution = model.Unresolved
	}
	return attempt
}

// Backoff returns 100 ms doubled per retry and capped at 5 s.
func Backoff(retry int) time.Duration {
	if retry <= 0 {
		return 100 * time.Millisecond
	}
	delay := 100 * time.Millisecond
	for range retry {
		if delay >= maxBackoff/2 {
			return maxBackoff
		}
		delay *= 2
	}
	if delay > maxBackoff {
		return maxBackoff
	}
	return delay
}
