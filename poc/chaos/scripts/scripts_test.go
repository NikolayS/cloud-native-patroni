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

package scripts

import (
	"os"
	"strings"
	"testing"
)

func TestScriptsHaveRequiredSafetyPreambleAndMain(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sh") {
			continue
		}
		count++
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Name(), err)
		}
		text := string(data)
		for _, required := range []string{"#!/usr/bin/env bash", "Copyright © contributors to CloudNativePG", "set -Eeuo pipefail", "IFS=$'\\n\\t'"} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s lacks %q", entry.Name(), required)
			}
		}
		if !strings.HasSuffix(strings.TrimSpace(text), `main "$@"`) {
			t.Fatalf("%s does not end in main \"$@\"", entry.Name())
		}
		for _, forbidden := range []string{"pg_ctl promote", "pg_ctl start", "pg_ctl stop", "pg_ctl restart", "pg_rewind", "standby.signal", "primary_conninfo", "primary_slot_name", "synchronous_standby_names"} {
			if strings.Contains(strings.ToLower(text), forbidden) {
				t.Fatalf("%s contains forbidden authority operation %q", entry.Name(), forbidden)
			}
		}
	}
	if count != 4 {
		t.Fatalf("script count = %d, want 4", count)
	}
}
