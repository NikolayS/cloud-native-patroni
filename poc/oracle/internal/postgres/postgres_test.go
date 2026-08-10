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

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

func TestBuildConfigForDirectPod(t *testing.T) {
	got, err := BuildConfig("10.244.2.7", 5432, "postgres", "cnpatroni_chaos", "secret", 400*time.Millisecond)
	if err != nil {
		t.Fatalf("BuildConfig() error = %v", err)
	}
	if got.Host != "10.244.2.7" || got.Port != 5432 || got.User != "cnpatroni_chaos" || got.Database != "postgres" {
		t.Fatalf("config target = host=%s port=%d user=%s database=%s", got.Host, got.Port, got.User, got.Database)
	}
	if got.DefaultQueryExecMode != pgx.QueryExecModeExec || got.StatementCacheCapacity != 0 || got.DescriptionCacheCapacity != 0 {
		t.Fatalf("query mode/cache config = %v/%d/%d", got.DefaultQueryExecMode, got.StatementCacheCapacity, got.DescriptionCacheCapacity)
	}
	if got.RuntimeParams["statement_timeout"] != "400ms" {
		t.Fatalf("statement_timeout = %q, want 400ms", got.RuntimeParams["statement_timeout"])
	}
}

func TestBuildConfigRejectsNonPodTarget(t *testing.T) {
	for _, host := range []string{"cluster-rw", "localhost", "127.0.0.1"} {
		if _, err := BuildConfig(host, 5432, "postgres", "cnpatroni_chaos", "secret", 400*time.Millisecond); err == nil {
			t.Fatalf("BuildConfig(%q) error = nil, want rejection", host)
		}
	}
}

type execCall struct {
	sql  string
	args []any
}

type fakeExecer struct {
	calls []execCall
	err   error
}

func (f *fakeExecer) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.calls = append(f.calls, execCall{sql: sql, args: args})
	return pgconn.NewCommandTag("INSERT 0 1"), f.err
}

func TestAttemptUsesCanonicalExtendedProtocolOnce(t *testing.T) {
	execer := &fakeExecer{}
	times := []time.Time{time.Unix(0, 100), time.Unix(0, 900)}
	writer := Writer{NodeID: "pod-a", Exec: execer, Origin: time.Unix(0, 0), Now: func() time.Time {
		result := times[0]
		times = times[1:]
		return result
	}}
	got := writer.Attempt(context.Background(), 7)
	if got.Outcome != model.Committed || got.DispatchAt != 100 || got.SettleAt != 900 {
		t.Fatalf("Attempt() = %#v", got)
	}
	if len(execer.calls) != 1 {
		t.Fatalf("Exec calls = %d, want 1", len(execer.calls))
	}
	call := execer.calls[0]
	if call.sql != CanonicalInsertSQL || len(call.args) != 3 || call.args[0] != pgx.QueryExecModeExec || call.args[1] != int64(7) || call.args[2] != "pod-a" {
		t.Fatalf("Exec call = %#v", call)
	}
}

func TestAttemptClassifiesServerAndTransportErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want model.Outcome
	}{
		{name: "read only", err: &pgconn.PgError{Code: "25006"}, want: model.Refused},
		{name: "privilege", err: &pgconn.PgError{Code: "42501"}, want: model.Misconfigured},
		{name: "transport", err: errors.New("unexpected EOF"), want: model.InDoubt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execer := &fakeExecer{err: tt.err}
			writer := Writer{NodeID: "pod-a", Exec: execer, Origin: time.Now(), Now: time.Now}
			got := writer.Attempt(context.Background(), 8)
			if got.Outcome != tt.want {
				t.Fatalf("outcome = %q, want %q", got.Outcome, tt.want)
			}
		})
	}
}
