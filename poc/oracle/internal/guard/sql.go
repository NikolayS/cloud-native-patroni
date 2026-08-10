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

import "fmt"

// SQLKind is a closed review category for oracle statements.
type SQLKind string

const (
	SQLCanonicalInsert SQLKind = "canonical_insert"
	SQLResolver        SQLKind = "resolver"
	SQLReadOnly        SQLKind = "read_only"
	SQLBootstrap       SQLKind = "bootstrap"
)

const (
	// CanonicalInsert is deliberately a single implicit transaction. The
	// pg_control timeline can lag immediately after promotion and is forensic
	// data only; it is never detector input. observed_at is a database-node wall
	// clock and is likewise forbidden from ordering decisions.
	CanonicalInsert = `insert into cnpatroni_chaos.commits(round_id, node_id, timeline, observed_at)
values ($1, $2, pg_control_checkpoint().timeline_id, clock_timestamp());`
	ResolverSelect = `select 1 from cnpatroni_chaos.commits where round_id = $1 and node_id = $2;`

	SelectRecovery            = `select pg_is_in_recovery();`
	SelectTransactionReadOnly = `select current_setting('transaction_read_only');`
	SelectCheckpointTimeline  = `select timeline_id from pg_control_checkpoint();`
	SelectPostmasterStart     = `select pg_postmaster_start_time();`
	SelectSystemIdentifier    = `select system_identifier from pg_control_system();`
	SelectCurrentWAL          = `select pg_current_wal_lsn();`
	SelectCurrentTimeline     = `select substr(pg_walfile_name(pg_current_wal_lsn()), 1, 8);`
	SelectStatReplication     = `select application_name, client_addr, state, sync_state, sent_lsn, write_lsn, flush_lsn, replay_lsn from pg_stat_replication order by application_name;`
	SelectStandbyPositions    = `select pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn();`
	SelectStatWALReceiver     = `select status, sender_host, sender_port, slot_name, written_lsn, flushed_lsn, received_tli from pg_stat_wal_receiver;`
	SelectCommitSurvey        = `select round_id, node_id, timeline, observed_at from cnpatroni_chaos.commits order by round_id, node_id;`

	// BootstrapRoleTemplate is the sole dynamic-SQL exemption. pgx's literal
	// sanitizer replaces $1. The interpolated statement is never logged.
	BootstrapRoleTemplate = `create role cnpatroni_chaos login password $1 noinherit;`
	BootstrapSchema       = `create schema if not exists cnpatroni_chaos authorization cnpatroni_chaos;`
	BootstrapTable        = `create table if not exists cnpatroni_chaos.commits (
    round_id    bigint      not null,
    node_id     text        not null,
    timeline    bigint      not null,
    observed_at timestamptz not null,
    primary key (round_id, node_id)
);`
	BootstrapGrantSchema  = `grant usage on schema cnpatroni_chaos to cnpatroni_chaos;`
	BootstrapGrantTable   = `grant select, insert on cnpatroni_chaos.commits to cnpatroni_chaos;`
	BootstrapGrantControl = `grant execute on function pg_control_checkpoint() to cnpatroni_chaos;`
)

// Statement is a reviewed query and its authority category.
type Statement struct {
	SQL  string
	Kind SQLKind
}

var statements = map[string]Statement{
	"canonical_insert":             {SQL: CanonicalInsert, Kind: SQLCanonicalInsert},
	"resolver_select":              {SQL: ResolverSelect, Kind: SQLResolver},
	"select_recovery":              {SQL: SelectRecovery, Kind: SQLReadOnly},
	"select_transaction_read_only": {SQL: SelectTransactionReadOnly, Kind: SQLReadOnly},
	"select_checkpoint_timeline":   {SQL: SelectCheckpointTimeline, Kind: SQLReadOnly},
	"select_postmaster_start":      {SQL: SelectPostmasterStart, Kind: SQLReadOnly},
	"select_system_identifier":     {SQL: SelectSystemIdentifier, Kind: SQLReadOnly},
	"select_current_wal":           {SQL: SelectCurrentWAL, Kind: SQLReadOnly},
	"select_current_timeline":      {SQL: SelectCurrentTimeline, Kind: SQLReadOnly},
	"select_stat_replication":      {SQL: SelectStatReplication, Kind: SQLReadOnly},
	"select_standby_positions":     {SQL: SelectStandbyPositions, Kind: SQLReadOnly},
	"select_stat_wal_receiver":     {SQL: SelectStatWALReceiver, Kind: SQLReadOnly},
	"select_commit_survey":         {SQL: SelectCommitSurvey, Kind: SQLReadOnly},
	"bootstrap_role_template":      {SQL: BootstrapRoleTemplate, Kind: SQLBootstrap},
	"bootstrap_schema":             {SQL: BootstrapSchema, Kind: SQLBootstrap},
	"bootstrap_table":              {SQL: BootstrapTable, Kind: SQLBootstrap},
	"bootstrap_grant_schema":       {SQL: BootstrapGrantSchema, Kind: SQLBootstrap},
	"bootstrap_grant_table":        {SQL: BootstrapGrantTable, Kind: SQLBootstrap},
	"bootstrap_grant_control":      {SQL: BootstrapGrantControl, Kind: SQLBootstrap},
}

var sqlSet = func() map[string]struct{} {
	result := make(map[string]struct{}, len(statements))
	for _, statement := range statements {
		result[statement.SQL] = struct{}{}
	}
	return result
}()

// ValidateSQL rejects any statement not present byte-for-byte in the inventory.
func ValidateSQL(sql string) error {
	if _, ok := sqlSet[sql]; !ok {
		return fmt.Errorf("SQL statement is not allowlisted")
	}
	return nil
}
