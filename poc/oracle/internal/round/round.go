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

// Package round provides drift-free scheduling and per-node serialization.
package round

import (
	"sync/atomic"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

// DispatchInstant computes T0+r*period without reference to earlier rounds.
func DispatchInstant(start model.Instant, roundID int64, period time.Duration) model.Instant {
	return start + model.Instant(roundID*int64(period))
}

// DispatchSkew returns max minus min dispatch time.
func DispatchSkew(instants []model.Instant) time.Duration {
	if len(instants) < 2 {
		return 0
	}
	minimum, maximum := instants[0], instants[0]
	for _, instant := range instants[1:] {
		if instant < minimum {
			minimum = instant
		}
		if instant > maximum {
			maximum = instant
		}
	}
	return time.Duration(maximum - minimum)
}

// NodeGate prevents a second attempt while the prior one is in flight.
type NodeGate struct{ active atomic.Bool }

// TryStart acquires the gate without waiting.
func (g *NodeGate) TryStart() bool { return g.active.CompareAndSwap(false, true) }

// Done releases the gate after an attempt settles.
func (g *NodeGate) Done() { g.active.Store(false) }
