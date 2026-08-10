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

package sample

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cloudnative-pg/cloudnative-pg/poc/oracle/internal/guard"
)

// SQLView is a typed view over direct-Pod introspection. Pointer fields remain
// nil when a query failed; unknown is never represented as false.
type SQLView struct {
	InRecovery          *bool            `json:"pg_is_in_recovery,omitempty"`
	TransactionReadOnly *string          `json:"transaction_read_only,omitempty"`
	CheckpointTimeline  *int64           `json:"checkpoint_timeline,omitempty"`
	PostmasterStart     *time.Time       `json:"postmaster_start_time,omitempty"`
	SystemIdentifier    *string          `json:"system_identifier,omitempty"`
	CurrentWAL          *string          `json:"current_wal_lsn,omitempty"`
	CurrentTimelineHex  *string          `json:"current_timeline_hex,omitempty"`
	StandbyReceiveWAL   *string          `json:"last_wal_receive_lsn,omitempty"`
	StandbyReplayWAL    *string          `json:"last_wal_replay_lsn,omitempty"`
	Replication         []map[string]any `json:"pg_stat_replication,omitempty"`
	WALReceiver         []map[string]any `json:"pg_stat_wal_receiver,omitempty"`
}

// SQLRecord is one independently timed SQL source sample.
type SQLRecord struct {
	Record
	View SQLView `json:"view"`
}

// SQLSampler holds the read-only connection dedicated to one node.
type SQLSampler struct {
	Node   string
	Conn   *pgx.Conn
	Origin time.Time
	Now    func() time.Time
}

// Sample branches on pg_is_in_recovery before issuing primary-only queries.
func (s SQLSampler) Sample(ctx context.Context) SQLRecord {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	record := SQLRecord{Record: Record{TakenAt: started.UTC(), MonoNS: started.Sub(s.Origin).Nanoseconds(), Source: "sql", Node: s.Node}}
	if s.Conn == nil {
		record.Error = "no live read-only direct-Pod connection"
		return record
	}
	queryCtx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()

	var errorsSeen []string
	if err := queryScalar(queryCtx, s.Conn, guard.SelectRecovery, &record.View.InRecovery); err != nil {
		record.Error = err.Error()
		record.Latency = float64(now().Sub(started).Microseconds()) / 1000
		return record
	}
	for statement, destination := range map[string]any{
		guard.SelectTransactionReadOnly: &record.View.TransactionReadOnly,
		guard.SelectCheckpointTimeline:  &record.View.CheckpointTimeline,
		guard.SelectPostmasterStart:     &record.View.PostmasterStart,
		guard.SelectSystemIdentifier:    &record.View.SystemIdentifier,
	} {
		if err := queryScalar(queryCtx, s.Conn, statement, destination); err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		}
	}
	if record.View.InRecovery != nil && !*record.View.InRecovery {
		if err := queryScalar(queryCtx, s.Conn, guard.SelectCurrentWAL, &record.View.CurrentWAL); err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		}
		if err := queryScalar(queryCtx, s.Conn, guard.SelectCurrentTimeline, &record.View.CurrentTimelineHex); err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		}
		rows, err := queryRows(queryCtx, s.Conn, guard.SelectStatReplication)
		if err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		} else {
			record.View.Replication = rows
		}
	} else if record.View.InRecovery != nil {
		var receive, replay *string
		if err := queryTwoScalars(queryCtx, s.Conn, guard.SelectStandbyPositions, &receive, &replay); err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		} else {
			record.View.StandbyReceiveWAL, record.View.StandbyReplayWAL = receive, replay
		}
		rows, err := queryRows(queryCtx, s.Conn, guard.SelectStatWALReceiver)
		if err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		} else {
			record.View.WALReceiver = rows
		}
	}
	raw, _ := json.Marshal(record.View)
	record.Raw = string(raw)
	record.Latency = float64(now().Sub(started).Microseconds()) / 1000
	record.OK = len(errorsSeen) == 0
	record.Error = strings.Join(errorsSeen, "; ")
	return record
}

func queryScalar(ctx context.Context, conn *pgx.Conn, statement string, destination any) error {
	if err := guard.ValidateSQL(statement); err != nil {
		return err
	}
	if err := conn.QueryRow(ctx, statement, pgx.QueryExecModeExec).Scan(destination); err != nil {
		return fmt.Errorf("SQL sample failed: %w", err)
	}
	return nil
}

func queryTwoScalars(ctx context.Context, conn *pgx.Conn, statement string, first, second any) error {
	if err := guard.ValidateSQL(statement); err != nil {
		return err
	}
	if err := conn.QueryRow(ctx, statement, pgx.QueryExecModeExec).Scan(first, second); err != nil {
		return fmt.Errorf("SQL sample failed: %w", err)
	}
	return nil
}

func queryRows(ctx context.Context, conn *pgx.Conn, statement string) ([]map[string]any, error) {
	if err := guard.ValidateSQL(statement); err != nil {
		return nil, err
	}
	rows, err := conn.Query(ctx, statement, pgx.QueryExecModeExec)
	if err != nil {
		return nil, fmt.Errorf("SQL sample failed: %w", err)
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
	var result []map[string]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("read SQL sample row: %w", err)
		}
		row := make(map[string]any, len(values))
		for index, value := range values {
			row[string(fields[index].Name)] = value
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate SQL sample rows: %w", err)
	}
	return result, nil
}
