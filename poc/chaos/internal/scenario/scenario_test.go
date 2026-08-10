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

package scenario

import (
	"regexp"
	"testing"
	"time"
)

func TestValidatePartitionDuration(t *testing.T) {
	tests := []struct {
		name       string
		duration   time.Duration
		toleration *int64
		wantErr    bool
	}{
		{name: "default", duration: 120 * time.Second},
		{name: "hard boundary", duration: 240 * time.Second},
		{name: "over cap without toleration", duration: 241 * time.Second, wantErr: true},
		{name: "non-positive", duration: 0, wantErr: true},
		{name: "explicit longer toleration", duration: 250 * time.Second, toleration: int64Pointer(600)},
		{name: "insufficient explicit toleration", duration: 250 * time.Second, toleration: int64Pointer(250), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidatePartitionDuration(tt.duration, tt.toleration); (err != nil) != tt.wantErr {
				t.Fatalf("ValidatePartitionDuration() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunID(t *testing.T) {
	got, err := RunID("s3-partition", time.Date(2026, 8, 9, 12, 34, 56, 0, time.FixedZone("offset", -7*60*60)), []byte{0xab, 0xcd, 0xef})
	if err != nil {
		t.Fatalf("RunID() error = %v", err)
	}
	if got != "s3-partition-20260809T193456Z-abcdef" {
		t.Fatalf("RunID() = %q", got)
	}
	if !regexp.MustCompile(`^[a-z0-9-]+-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$`).MatchString(got) {
		t.Fatalf("RunID() = %q, wrong format", got)
	}
}

func TestRunIDRejectsUnsafeScenario(t *testing.T) {
	if _, err := RunID("../escape", time.Now(), []byte{1, 2, 3}); err == nil {
		t.Fatal("RunID() error = nil, want rejection")
	}
}

func int64Pointer(value int64) *int64 { return &value }
