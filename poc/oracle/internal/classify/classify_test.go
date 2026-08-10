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

package classify

import (
	"errors"
	"io"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

func TestOutcome(t *testing.T) {
	tests := []struct {
		name       string
		dispatched bool
		err        error
		want       model.Outcome
	}{
		{name: "acknowledged", dispatched: true, want: model.Committed},
		{name: "connect failure", err: errors.New("connection refused"), want: model.Unreachable},
		{name: "read only", dispatched: true, err: &pgconn.PgError{Code: "25006"}, want: model.Refused},
		{name: "server connection exception", dispatched: true, err: &pgconn.PgError{Code: "08006"}, want: model.Refused},
		{name: "privilege failure", dispatched: true, err: &pgconn.PgError{Code: "42501"}, want: model.Misconfigured},
		{name: "duplicate", dispatched: true, err: &pgconn.PgError{Code: "23505"}, want: model.Misconfigured},
		{name: "unexpected eof", dispatched: true, err: io.ErrUnexpectedEOF, want: model.InDoubt},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Outcome(tt.dispatched, tt.err); got != tt.want {
				t.Fatalf("Outcome() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTooManyConnectionsRequiresBudgetEvidence(t *testing.T) {
	err := &pgconn.PgError{Code: "53300"}
	if got := Outcome(true, err); got != model.Misconfigured {
		t.Fatalf("Outcome() = %q, want %q", got, model.Misconfigured)
	}
	if got := OutcomeWithPolicy(true, err, Policy{ConnectionBudgetExceeded: true}); got != model.Refused {
		t.Fatalf("OutcomeWithPolicy() = %q, want %q", got, model.Refused)
	}
}

func TestSQLState(t *testing.T) {
	if got := SQLState(&pgconn.PgError{Code: "42P01"}); got != "42P01" {
		t.Fatalf("SQLState() = %q, want 42P01", got)
	}
	if got := SQLState(io.EOF); got != "" {
		t.Fatalf("SQLState() = %q, want empty", got)
	}
}
