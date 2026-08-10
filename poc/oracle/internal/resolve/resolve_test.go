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
	"testing"
	"time"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

func TestDecide(t *testing.T) {
	tests := []struct {
		name      string
		present   bool
		lineageOK bool
		want      model.Resolution
	}{
		{name: "present survives lineage uncertainty", present: true, lineageOK: false, want: model.ResolvedCommitted},
		{name: "absent with intact lineage", lineageOK: true, want: model.ResolvedAbsent},
		{name: "absent after rewind", lineageOK: false, want: model.Unresolved},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decide(tt.present, tt.lineageOK); got != tt.want {
				t.Fatalf("Decide() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBackoffBoundaries(t *testing.T) {
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 800 * time.Millisecond, 1600 * time.Millisecond, 3200 * time.Millisecond, 5 * time.Second, 5 * time.Second}
	for i, expected := range want {
		if got := Backoff(i); got != expected {
			t.Fatalf("Backoff(%d) = %s, want %s", i, got, expected)
		}
	}
}

func TestNormalizeAbsentOnChangedLineage(t *testing.T) {
	attempt := model.Attempt{Outcome: model.InDoubt, Resolution: model.ResolvedAbsent, LineageOK: false}
	got := Normalize(attempt)
	if got.Resolution != model.Unresolved {
		t.Fatalf("Normalize().Resolution = %q, want %q", got.Resolution, model.Unresolved)
	}
}
