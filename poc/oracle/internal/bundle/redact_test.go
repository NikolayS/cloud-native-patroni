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

package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	input := []byte(`postgresql:
  authentication:
    superuser:
      password: "swordfish"
  parameters:
    primary_conninfo: "host=10.0.0.2 user=repl password=secret sslmode=disable"
log: "request https://alice:hunter2@patroni.example/config"
url: postgresql://bob:open-sesame@10.0.0.3/postgres
safe: visible`)
	got := string(Redact(input))
	for _, secret := range []string{"swordfish", "secret", "hunter2", "open-sesame"} {
		if strings.Contains(got, secret) {
			t.Fatalf("Redact() retained secret %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "safe: visible") {
		t.Fatalf("Redact() removed safe data: %q", got)
	}
	if count := strings.Count(got, "[REDACTED]"); count != 4 {
		t.Fatalf("Redact() marker count = %d, want 4: %q", count, got)
	}
}

func TestWriterUsesRestrictedModesAndIncrementalJSONL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	w, err := NewWriter(dir, []string{"extra-secret"})
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	if err := w.AppendJSONL("rounds.jsonl", map[string]string{"value": "extra-secret", "safe": "kept"}); err != nil {
		t.Fatalf("AppendJSONL() error = %v", err)
	}
	if err := w.AppendJSONL("rounds.jsonl", map[string]int{"round": 2}); err != nil {
		t.Fatalf("AppendJSONL() second error = %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "rounds.jsonl"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode = %#o, want 0600", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "rounds.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), "extra-secret") || !strings.Contains(string(data), "\"safe\":\"kept\"") {
		t.Fatalf("bundle content = %q", data)
	}
	if lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1; lines != 2 {
		t.Fatalf("JSONL line count = %d, want 2", lines)
	}
}

func TestWriterRejectsTraversal(t *testing.T) {
	w, err := NewWriter(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	if err := w.Write("../escape", []byte("bad")); err == nil {
		t.Fatal("Write(../escape) error = nil, want rejection")
	}
}
