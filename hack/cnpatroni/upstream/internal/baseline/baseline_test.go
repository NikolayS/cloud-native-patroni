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

package baseline_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudnative-pg/cloudnative-pg/hack/cnpatroni/upstream/internal/baseline"
)

const goodBaseline = `
schema: cnpatroni.io/upstream-baseline/v1
upstream:
  url: https://github.com/cloudnative-pg/cloudnative-pg
  track: main
  remote: upstream
fork_base:
  commit: 1111111111111111111111111111111111111111
  describe: v1.30.0-69-gb22682115
  upstream_ref: refs/heads/main
  date: "2026-08-06"
  tag: cnpatroni-fork-base
last_integrated:
  commit: 1111111111111111111111111111111111111111
  describe: v1.30.0-69-gb22682115
  date: "2026-08-06"
compatibility:
  cloudnative_pg_minor: "1.30"
  cloudnative_pg_reference_tag: v1.30.0
  claimed: "1.30 pre-release trunk"
  verified_at: "2026-08-09"
  unadopted_upstream_commits: 0
not_adopted: []
`

func writeTemp(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "upstream-baseline.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	return path
}

func TestLoadReadsTheRecordedBaseline(t *testing.T) {
	b, err := baseline.Load(writeTemp(t, goodBaseline))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if b.ForkBase.Commit != strings.Repeat("1", 40) {
		t.Errorf("fork base = %q", b.ForkBase.Commit)
	}
	if b.Upstream.Remote != "upstream" {
		t.Errorf("remote = %q, want %q", b.Upstream.Remote, "upstream")
	}
	if b.LastIntegrated.Commit != b.ForkBase.Commit {
		t.Errorf("a fresh fork must record last_integrated equal to fork_base")
	}
}

func TestLoadRejectsAnUnknownSchema(t *testing.T) {
	_, err := baseline.Load(writeTemp(t,
		strings.Replace(goodBaseline, "upstream-baseline/v1", "upstream-baseline/v2", 1)))
	if err == nil {
		t.Fatal("Load must reject an unknown schema identifier")
	}
}

func TestLoadRejectsAnUnknownField(t *testing.T) {
	_, err := baseline.Load(writeTemp(t, goodBaseline+"\ntypo_field: true\n"))
	if err == nil {
		t.Fatal("Load must reject an unknown top-level field")
	}
}

func TestLoadRejectsAMalformedCommit(t *testing.T) {
	_, err := baseline.Load(writeTemp(t,
		strings.Replace(goodBaseline, strings.Repeat("1", 40), "b226821", 1)))
	if err == nil {
		t.Fatal("Load must require a full 40-character commit identifier")
	}
}

func TestLoadRejectsAMissingFile(t *testing.T) {
	if _, err := baseline.Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("Load must fail when the baseline file is absent")
	}
}

func TestDefaultRemoteIsUpstream(t *testing.T) {
	body := strings.Replace(goodBaseline, "  remote: upstream\n", "", 1)
	b, err := baseline.Load(writeTemp(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if b.Upstream.Remote != "upstream" {
		t.Fatalf("remote = %q, want the default %q", b.Upstream.Remote, "upstream")
	}
}
