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

// Package postgres implements direct-Pod writes and read-only evidence queries.
package postgres

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/classify"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/guard"
	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/model"
)

// CanonicalInsertSQL aliases the reviewed statement to keep every issuer tied
// to the guard inventory.
const CanonicalInsertSQL = guard.CanonicalInsert

// BuildConfig constructs a pgx extended-protocol, cache-free direct-Pod
// connection. The returned config must never be logged because it owns the
// password.
func BuildConfig(host string, port uint16, database, user, password string, statementTimeout time.Duration) (*pgx.ConnConfig, error) {
	connectionURL := url.URL{
		Scheme: "postgres", Host: net.JoinHostPort(host, strconv.Itoa(int(port))), Path: "/" + database,
		User: url.UserPassword(user, password),
	}
	query := connectionURL.Query()
	query.Set("sslmode", "disable")
	query.Set("target_session_attrs", "any")
	connectionURL.RawQuery = query.Encode()
	if _, err := guard.ValidateConnectionString(connectionURL.String()); err != nil {
		return nil, err
	}
	config, err := pgx.ParseConfig(connectionURL.String())
	if err != nil {
		return nil, fmt.Errorf("parse direct-Pod Postgres configuration: %w", err)
	}
	config.DefaultQueryExecMode = pgx.QueryExecModeExec
	config.StatementCacheCapacity = 0
	config.DescriptionCacheCapacity = 0
	config.RuntimeParams["statement_timeout"] = statementTimeout.String()
	return config, nil
}

// Execer is the subset of pgx.Conn used for one canonical attempt.
type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

// Writer holds one persistent connection for exactly one node. It performs no
// retry and no reconnect inside an attempt.
type Writer struct {
	NodeID string
	Exec   Execer
	Origin time.Time
	Now    func() time.Time
}

// Attempt sends the canonical statement once, using extended unnamed execution.
func (w Writer) Attempt(ctx context.Context, roundID int64) model.Attempt {
	now := w.Now
	if now == nil {
		now = time.Now
	}
	dispatch := now()
	attempt := model.Attempt{RoundID: roundID, NodeID: w.NodeID, DispatchAt: model.Instant(dispatch.Sub(w.Origin).Nanoseconds()), LineageOK: true}
	if w.Exec == nil {
		attempt.Outcome = model.NoAttempt
		attempt.SettleAt = attempt.DispatchAt
		attempt.Error = "no live direct-Pod connection"
		return attempt
	}
	_, err := w.Exec.Exec(ctx, CanonicalInsertSQL, pgx.QueryExecModeExec, roundID, w.NodeID)
	settle := now()
	attempt.SettleAt = model.Instant(settle.Sub(w.Origin).Nanoseconds())
	attempt.Outcome = classify.Outcome(true, err)
	attempt.SQLState = classify.SQLState(err)
	if err != nil {
		attempt.Error = err.Error()
	}
	return attempt
}
