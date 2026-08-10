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
	"strings"
	"testing"
)

func TestSQLAllowlist(t *testing.T) {
	if len(statements) != 19 {
		t.Fatalf("the SQL inventory has %d entries, want 19", len(statements))
	}
	for name, statement := range statements {
		if err := ValidateSQL(statement.SQL); err != nil {
			t.Fatalf("ValidateSQL(%s) error = %v", name, err)
		}
		switch statement.Kind {
		case SQLCanonicalInsert, SQLResolver, SQLReadOnly, SQLBootstrap:
		default:
			t.Fatalf("statement %s has unreviewed kind %q", name, statement.Kind)
		}
		if statement.Kind == SQLReadOnly {
			trimmed := strings.ToLower(strings.TrimSpace(statement.SQL))
			if !strings.HasPrefix(trimmed, "select") {
				t.Fatalf("read-only statement %s starts with %q", name, trimmed)
			}
		}
	}
	if got := statements["canonical_insert"].SQL; got != CanonicalInsert {
		t.Fatalf("canonical statement changed:\n%s", got)
	}
	if err := ValidateSQL("select now()"); err == nil {
		t.Fatal("ValidateSQL(dynamic statement) error = nil, want rejection")
	}
	if strings.Contains(BootstrapRoleTemplate, "%s") {
		t.Fatal("BootstrapRoleTemplate contains bare fmt placeholder")
	}
}

func TestForbiddenAuthorityStatementsAbsent(t *testing.T) {
	for name, statement := range statements {
		lower := strings.ToLower(statement.SQL)
		for _, forbidden := range []string{"pg_ctl", "pg_rewind", "standby.signal", "primary_conninfo", "primary_slot_name", "synchronous_standby_names", "promote"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("statement %s contains forbidden authority operation %q", name, forbidden)
			}
		}
	}
}
