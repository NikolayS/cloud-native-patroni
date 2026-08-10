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

package resolve

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

// Lookup performs a resolver SELECT on a separate connection and returns the
// contemporaneous lineage comparison.
type Lookup interface {
	Lookup(context.Context, model.Attempt) (present bool, lineageOK bool, err error)
}

// SleepFunc makes bounded retry timing testable.
type SleepFunc func(context.Context, time.Duration) error

// Resolver is the IO adapter around the pure Decide function.
type Resolver struct {
	Lookup Lookup
	Sleep  SleepFunc
}

// Resolve retries lookup errors until its context ends. A successful absent
// lookup resolves immediately only when the adapter proves lineage intact.
func (r Resolver) Resolve(ctx context.Context, attempt model.Attempt) model.Attempt {
	if r.Lookup == nil {
		attempt.Resolution = model.Unresolved
		attempt.ResolverNote = "resolver lookup is unavailable"
		return attempt
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	for retry := 0; ; retry++ {
		present, lineageOK, err := r.Lookup.Lookup(ctx, attempt)
		if err == nil {
			attempt.LineageOK = lineageOK
			attempt.Resolution = Decide(present, lineageOK)
			attempt.ResolverNote = fmt.Sprintf("row_present=%t lineage_ok=%t", present, lineageOK)
			return Normalize(attempt)
		}
		attempt.ResolverNote = err.Error()
		if sleep(ctx, Backoff(retry)) != nil {
			attempt.Resolution = model.Unresolved
			return attempt
		}
	}
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
