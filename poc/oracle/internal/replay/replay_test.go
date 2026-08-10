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

package replay

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/verdict"
)

func TestGoldenFixtures(t *testing.T) {
	tests := []struct {
		fixture string
		status  verdict.Status
		exit    int
	}{
		{fixture: "dual-commit-run", status: verdict.Violation, exit: 1},
		{fixture: "clean-run", status: verdict.Pass, exit: 0},
		{fixture: "indoubt-run", status: verdict.Inconclusive, exit: 2},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got, err := LoadAndDetect(filepath.Join("..", "..", "testdata", "fixtures", tt.fixture))
			if err != nil {
				t.Fatalf("LoadAndDetect() error = %v", err)
			}
			if got.Status != tt.status || got.ExitCode != tt.exit {
				t.Fatalf("verdict = %s exit %d, want %s exit %d", got.Status, got.ExitCode, tt.status, tt.exit)
			}
		})
	}
}

func writeFixtureFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}

func TestFixtureRejectsUnknownOutcome(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, "manifest.json", `{"expected_nodes":["a","b"]}`)
	writeFixtureFile(t, dir, "rounds.jsonl", `{"round_id":1,"node_id":"a","outcome":"Maybe"}`+"\n")
	got, err := LoadAndDetect(dir)
	if err == nil {
		t.Fatal("LoadAndDetect() error = nil, want validation error")
	}
	if got.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", got.ExitCode)
	}
}
