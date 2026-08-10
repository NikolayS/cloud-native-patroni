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

package sample

import (
	"testing"
)

func TestParsePatroniPreservesRawAndUnknownFields(t *testing.T) {
	raw := []byte(`{"state":"running","role":"primary","timeline":7,"new_field":{"nested":true}}`)
	got := ParsePatroni(raw)
	if !got.OK || got.State != "running" || got.Role != "primary" || got.Timeline == nil || *got.Timeline != 7 {
		t.Fatalf("ParsePatroni() = %#v", got)
	}
	if string(got.Raw) != string(raw) {
		t.Fatalf("raw = %q, want %q", got.Raw, raw)
	}
	if _, ok := got.Fields["new_field"]; !ok {
		t.Fatalf("fields = %#v, want new_field", got.Fields)
	}
}

func TestParsePatroniMissingRoleIsUnknown(t *testing.T) {
	got := ParsePatroni([]byte(`{"state":"running"}`))
	if !got.OK || got.Role != "" || got.RoleKnown {
		t.Fatalf("ParsePatroni() = %#v, want unknown role", got)
	}
}

func TestParsePatroniInvalidJSON(t *testing.T) {
	got := ParsePatroni([]byte(`not-json`))
	if got.OK || got.Error == "" || string(got.Raw) != "not-json" {
		t.Fatalf("ParsePatroni() = %#v", got)
	}
}
