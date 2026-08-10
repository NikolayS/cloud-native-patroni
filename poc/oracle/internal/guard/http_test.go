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

package guard

import (
	"net/http"
	"testing"
)

func TestPatroniHTTPAllowlist(t *testing.T) {
	wantPaths := []string{"/patroni", "/primary", "/replica", "/liveness", "/readiness", "/health", "/cluster", "/config", "/metrics", "/failsafe"}
	got := PatroniPaths()
	if len(got) != len(wantPaths) {
		t.Fatalf("PatroniPaths() has %d entries, want %d", len(got), len(wantPaths))
	}
	for i, path := range wantPaths {
		if got[i] != path {
			t.Fatalf("PatroniPaths()[%d] = %q, want %q", i, got[i], path)
		}
		request, err := NewPatroniRequest(http.MethodGet, path)
		if err != nil {
			t.Fatalf("NewPatroniRequest(GET, %q) error = %v", path, err)
		}
		if request.Method != http.MethodGet || request.Path != path {
			t.Fatalf("request = %#v, want GET %s", request, path)
		}
	}
}

func TestPatroniHTTPRejectsMutation(t *testing.T) {
	if _, err := NewPatroniRequest(http.MethodPost, "/switchover"); err == nil {
		t.Fatal("NewPatroniRequest(POST, /switchover) error = nil, want rejection")
	}
	if _, err := NewPatroniRequest(http.MethodDelete, "/cluster"); err == nil {
		t.Fatal("NewPatroniRequest(DELETE, /cluster) error = nil, want rejection")
	}
	if _, err := NewPatroniRequest(http.MethodGet, "/reload"); err == nil {
		t.Fatal("NewPatroniRequest(GET, /reload) error = nil, want rejection")
	}
}
