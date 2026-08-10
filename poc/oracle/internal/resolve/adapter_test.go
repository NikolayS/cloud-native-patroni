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
	"errors"
	"testing"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

type lookupResult struct {
	present   bool
	lineageOK bool
	err       error
}

type fakeLookup struct {
	results []lookupResult
	calls   int
}

func (f *fakeLookup) Lookup(context.Context, model.Attempt) (bool, bool, error) {
	index := f.calls
	f.calls++
	if index >= len(f.results) {
		index = len(f.results) - 1
	}
	result := f.results[index]
	return result.present, result.lineageOK, result.err
}

func TestResolverRetriesLookupErrorThenFindsRow(t *testing.T) {
	lookup := &fakeLookup{results: []lookupResult{{err: errors.New("reset")}, {present: true, lineageOK: true}}}
	var sleeps []time.Duration
	resolver := Resolver{Lookup: lookup, Sleep: func(_ context.Context, duration time.Duration) error {
		sleeps = append(sleeps, duration)
		return nil
	}}
	attempt := model.Attempt{RoundID: 7, NodeID: "pod-a", Outcome: model.InDoubt}
	got := resolver.Resolve(context.Background(), attempt)
	if got.Resolution != model.ResolvedCommitted || lookup.calls != 2 {
		t.Fatalf("Resolve() = %#v, calls=%d", got, lookup.calls)
	}
	if len(sleeps) != 1 || sleeps[0] != 100*time.Millisecond {
		t.Fatalf("sleeps = %v, want [100ms]", sleeps)
	}
}

func TestResolverStopsOnDestroyedLineage(t *testing.T) {
	lookup := &fakeLookup{results: []lookupResult{{present: false, lineageOK: false}}}
	resolver := Resolver{Lookup: lookup}
	got := resolver.Resolve(context.Background(), model.Attempt{RoundID: 8, NodeID: "pod-b", Outcome: model.InDoubt})
	if got.Resolution != model.Unresolved || got.LineageOK {
		t.Fatalf("Resolve() = %#v, want unresolved", got)
	}
}

func TestResolverDeadlineLeavesUnknown(t *testing.T) {
	lookup := &fakeLookup{results: []lookupResult{{err: errors.New("timeout")}}}
	ctx, cancel := context.WithCancel(context.Background())
	resolver := Resolver{Lookup: lookup, Sleep: func(context.Context, time.Duration) error { cancel(); return context.Canceled }}
	got := resolver.Resolve(ctx, model.Attempt{RoundID: 9, NodeID: "pod-c", Outcome: model.InDoubt})
	if got.Resolution != model.Unresolved || got.ResolverNote == "" {
		t.Fatalf("Resolve() = %#v, want unresolved with evidence", got)
	}
}
