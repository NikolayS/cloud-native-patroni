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

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayExitCodesUseProductionDetector(t *testing.T) {
	tests := []struct {
		fixture string
		exit    int
		status  string
	}{
		{fixture: "dual-commit-run", exit: 1, status: `"status":"Violation"`},
		{fixture: "clean-run", exit: 0, status: `"status":"Pass"`},
		{fixture: "indoubt-run", exit: 2, status: `"status":"Inconclusive"`},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := runCLI([]string{"replay", "--fixture", filepath.Join("..", "..", "testdata", "fixtures", tt.fixture)}, &stdout, &stderr)
			if exit != tt.exit || !strings.Contains(stdout.String(), tt.status) || stderr.Len() != 0 {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			}
		})
	}
}

func TestMandatoryContextHasNoDefault(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"run", "--namespace", "database", "--scope", "cluster-rw", "--expected-nodes", "a,b,c", "--duration", "1m", "--bundle-dir", "/bundle"}, &stdout, &stderr)
	if exit != 3 || !strings.Contains(stderr.String(), "--context") {
		t.Fatalf("exit=%d stderr=%q, want context usage error", exit, stderr.String())
	}
}

func TestPasswordFlagDoesNotExist(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := runCLI([]string{"run", "--password", "secret"}, &stdout, &stderr)
	if exit != 3 || !strings.Contains(stderr.String(), "flag provided but not defined") {
		t.Fatalf("exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestUnknownSubcommandIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if exit := runCLI([]string{"promote"}, &stdout, &stderr); exit != 3 {
		t.Fatalf("exit=%d, want 3", exit)
	}
}
