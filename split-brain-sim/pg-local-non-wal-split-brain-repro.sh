#!/usr/bin/env bash
# Reproduce PostgreSQL split-brain-adjacent state that bypasses synchronous
# replication once an old primary remains writable after a standby has been
# promoted.
#
# This is intentionally PostgreSQL-only: no Kubernetes/CNPG machinery.  It
# proves which PostgreSQL state classes are durable/cluster-visible, which are
# local-only, and which are failover-sensitive when normal logged writes are
# correctly blocked by synchronous replication.

set -euo pipefail

PRIMARY_PORT=${PRIMARY_PORT:-55432}
STANDBY_PORT=${STANDBY_PORT:-55433}
WORKDIR=${WORKDIR:-$(mktemp -d /tmp/pg-nonwal-split-brain.XXXXXX)}
KEEP_WORKDIR=${KEEP_WORKDIR:-0}
RESULTS=${RESULTS:-$PWD/split-brain-sim/pg-local-non-wal-split-brain-results.md}
PGDATABASE=${PGDATABASE:-postgres}
CHANNEL=split_brain_chan
LOCK_KEY=7407

PRIMARY=$WORKDIR/primary
STANDBY=$WORKDIR/standby
LOGDIR=$WORKDIR/logs
mkdir -p "$LOGDIR"

log() { printf '\n==> %s\n' "$*" | tee -a "$LOGDIR/run.log"; }
run() { printf '+ %s\n' "$*" | tee -a "$LOGDIR/run.log"; "$@"; }
psql_primary() { psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$PRIMARY_PORT" -d "$PGDATABASE" "$@"; }
psql_new() { psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$STANDBY_PORT" -d "$PGDATABASE" "$@"; }
wait_sql() {
  local port=$1 sql=$2
  for _ in $(seq 1 120); do
    if psql -X -qAt -h 127.0.0.1 -p "$port" -d "$PGDATABASE" -c "$sql" >/tmp/pg-wait-sql.$$ 2>/dev/null; then
      cat /tmp/pg-wait-sql.$$; rm -f /tmp/pg-wait-sql.$$; return 0
    fi
    sleep 0.25
  done
  echo "timed out waiting for SQL on port $port: $sql" >&2
  return 1
}
cleanup() {
  set +e
  pg_ctl -D "$STANDBY" -m immediate stop >/dev/null 2>&1
  pg_ctl -D "$PRIMARY" -m immediate stop >/dev/null 2>&1
  if [[ "$KEEP_WORKDIR" != "1" ]]; then rm -rf "$WORKDIR"; else echo "Kept $WORKDIR"; fi
}
trap cleanup EXIT

cat >"$RESULTS" <<EOF
# PostgreSQL non-WAL / split-brain-adjacent reproduction

Generated: $(date -u '+%Y-%m-%d %H:%M:%S UTC')

This local PostgreSQL reproduction promotes a synchronous standby while leaving
old primary running.  It first proves a normal logged write on the old primary
blocks in \`SyncRep\`, then tests state classes that can still change or diverge.

EOF

log "Initialize primary"
run initdb -D "$PRIMARY" -A trust --no-locale --encoding=UTF8 >/dev/null
cat >>"$PRIMARY/postgresql.conf" <<EOF
listen_addresses = '127.0.0.1'
port = $PRIMARY_PORT
wal_level = logical
max_wal_senders = 10
max_replication_slots = 10
hot_standby = on
synchronous_commit = on
# Set after the standby is created/started; otherwise bootstrap DDL waits for
# a synchronous standby that does not exist yet.
logging_collector = on
log_directory = '$LOGDIR'
log_filename = 'primary.log'
EOF
cat >>"$PRIMARY/pg_hba.conf" <<EOF
host all all 127.0.0.1/32 trust
host replication all 127.0.0.1/32 trust
EOF
run pg_ctl -D "$PRIMARY" -l "$LOGDIR/primary-start.log" -w start >/dev/null
psql_primary -qAt -c "CREATE ROLE repl WITH REPLICATION LOGIN;"

log "Take base backup and start synchronous standby"
run pg_basebackup -h 127.0.0.1 -p "$PRIMARY_PORT" -D "$STANDBY" -U repl -R -X stream >/dev/null
cat >>"$STANDBY/postgresql.conf" <<EOF
port = $STANDBY_PORT
primary_conninfo = 'host=127.0.0.1 port=$PRIMARY_PORT user=repl application_name=standby1'
hot_standby = on
logging_collector = on
log_directory = '$LOGDIR'
log_filename = 'standby.log'
EOF
run pg_ctl -D "$STANDBY" -l "$LOGDIR/standby-start.log" -w start >/dev/null

log "Enable and wait for synchronous replication"
psql_primary -qAt -c "ALTER SYSTEM SET synchronous_standby_names = 'FIRST 1 (walreceiver)';" >/dev/null
psql_primary -qAt -c "SELECT pg_reload_conf();" >/dev/null
for _ in $(seq 1 120); do
  state=$(psql_primary -qAt -c "SELECT coalesce((SELECT sync_state FROM pg_stat_replication WHERE application_name='walreceiver'),'')")
  [[ "$state" == "sync" ]] && break
  sleep 0.25
done
psql_primary -x -c "SELECT application_name, state, sync_state, write_lsn, flush_lsn, replay_lsn FROM pg_stat_replication;" | tee "$LOGDIR/sync-state.txt"

log "Create baseline objects"
psql_primary <<'SQL'
CREATE TABLE logged_probe(id bigserial primary key, origin text, note text, created_at timestamptz default clock_timestamp());
CREATE UNLOGGED TABLE unlogged_probe(origin text, note text, created_at timestamptz default clock_timestamp());
CREATE SEQUENCE split_seq CACHE 1;
INSERT INTO logged_probe(origin, note) VALUES ('primary', 'baseline logged row');
INSERT INTO unlogged_probe(origin, note) VALUES ('primary', 'baseline unlogged row before promotion');
-- First nextval emits a sequence WAL record that pre-logs future values.
SELECT nextval('split_seq') AS first_nextval_prelogs_future_values;
SQL
# Create these after basebackup so they prove user slot state is not WAL-replayed.
psql_primary -qAt -c "SELECT * FROM pg_create_physical_replication_slot('user_physical_slot');" | tee "$LOGDIR/create-physical-slot.txt"
psql_primary -qAt -c "SELECT * FROM pg_create_logical_replication_slot('user_logical_slot', 'pgoutput');" | tee "$LOGDIR/create-logical-slot.txt"
psql_primary -qAt -c "INSERT INTO logged_probe(origin, note) VALUES ('primary', 'flush before promote'); SELECT pg_current_wal_lsn();" >/dev/null
sleep 1

log "Promote standby, leaving old primary running and unfenced"
run pg_ctl -D "$STANDBY" promote -w >/dev/null
wait_sql "$STANDBY_PORT" "SELECT NOT pg_is_in_recovery();" | tee "$LOGDIR/promoted.txt"

log "Control: normal logged write on old primary blocks in SyncRep"
set +e
timeout 3s psql -X -v ON_ERROR_STOP=1 -h 127.0.0.1 -p "$PRIMARY_PORT" -d "$PGDATABASE" -qAt -c "INSERT INTO logged_probe(origin, note) VALUES ('old-primary', 'logged write should block');" >"$LOGDIR/logged-write.out" 2>"$LOGDIR/logged-write.err"
logged_rc=$?
set -e
cat "$LOGDIR/logged-write.err" | tee -a "$LOGDIR/run.log"
psql_primary -x -c "SELECT pid, wait_event_type, wait_event, state, left(query,120) AS query FROM pg_stat_activity WHERE wait_event = 'SyncRep' OR query LIKE '%logged write should block%';" | tee "$LOGDIR/logged-waiters.txt"

log "Test 1: unlogged relation diverges"
psql_primary -qAt -c "SET statement_timeout = '2500ms'; INSERT INTO unlogged_probe(origin, note) SELECT 'old-primary', 'unlogged row ' || g FROM generate_series(1,3) g; SELECT origin, count(*) FROM unlogged_probe GROUP BY origin ORDER BY origin;" | tee "$LOGDIR/unlogged-old.txt"
psql_new -qAt -c "INSERT INTO unlogged_probe(origin, note) VALUES ('promoted-primary', 'new primary unlogged row'); SELECT origin, count(*) FROM unlogged_probe GROUP BY origin ORDER BY origin;" | tee "$LOGDIR/unlogged-new.txt"

log "Test 2: cached/prelogged sequence values advance on old primary without SyncRep"
psql_primary -qAt -c "SET statement_timeout = '2500ms'; SELECT nextval('split_seq') FROM generate_series(1,5);" | tee "$LOGDIR/sequence-old.txt"
psql_new -qAt -c "SELECT nextval('split_seq') FROM generate_series(1,5);" | tee "$LOGDIR/sequence-new.txt"

log "Test 3: advisory lock can be held independently on both primaries"
psql_primary -qAt -c "SELECT pg_try_advisory_lock($LOCK_KEY);" | tee "$LOGDIR/advisory-old.txt"
psql_new -qAt -c "SELECT pg_try_advisory_lock($LOCK_KEY);" | tee "$LOGDIR/advisory-new.txt"

log "Test 4: LISTEN/NOTIFY event streams are local to each side"
( psql_new -X -h 127.0.0.1 -p "$STANDBY_PORT" -d "$PGDATABASE" >"$LOGDIR/listener-new-during-old-notify.out" 2>&1 <<SQL
LISTEN $CHANNEL;
SELECT pg_sleep(3);
SQL
) &
listener1=$!
sleep 0.5
psql_primary -qAt -c "SET statement_timeout = '2500ms'; NOTIFY $CHANNEL, 'from old primary';" | tee "$LOGDIR/notify-old-send.txt"
wait "$listener1"

( psql_new -X -h 127.0.0.1 -p "$STANDBY_PORT" -d "$PGDATABASE" >"$LOGDIR/listener-new-during-new-notify.out" 2>&1 <<SQL
LISTEN $CHANNEL;
SELECT pg_sleep(3);
SQL
) &
listener2=$!
sleep 0.5
psql_new -qAt -c "NOTIFY $CHANNEL, 'from promoted primary';" | tee "$LOGDIR/notify-new-send.txt"
wait "$listener2"
cat "$LOGDIR/listener-new-during-old-notify.out" | tee -a "$LOGDIR/run.log"
cat "$LOGDIR/listener-new-during-new-notify.out" | tee -a "$LOGDIR/run.log"

log "Test 5: user-created physical and logical replication slots exist only on old primary"
psql_primary -qAt -c "SELECT slot_name, slot_type, active FROM pg_replication_slots ORDER BY slot_name;" | tee "$LOGDIR/slots-old.txt"
psql_new -qAt -c "SELECT coalesce(string_agg(slot_name || ':' || slot_type, ',' ORDER BY slot_name), '<no slots>') FROM pg_replication_slots;" | tee "$LOGDIR/slots-new.txt"

log "Write markdown results"
{
  echo "## Control: logged writes block"
  echo
  echo '```text'
  echo "logged write exit code: $logged_rc"
  cat "$LOGDIR/logged-write.err"
  echo '```'
  echo
  echo "Expected: non-zero exit from the client-side timeout while the old primary has no synchronous standby; pg_stat_activity shows the backend waiting in SyncRep."
  echo
  echo "## 1. Unlogged relation divergence"
  echo
  echo "Old primary view:"
  echo '```text'; cat "$LOGDIR/unlogged-old.txt"; echo '```'
  echo "Promoted primary view:"
  echo '```text'; cat "$LOGDIR/unlogged-new.txt"; echo '```'
  echo
  echo "## 2. Sequence cached/prelogged values"
  echo
  echo "Old primary nextvals after promotion:"
  echo '```text'; cat "$LOGDIR/sequence-old.txt"; echo '```'
  echo "Promoted primary nextvals:"
  echo '```text'; cat "$LOGDIR/sequence-new.txt"; echo '```'
  echo
  echo "Interpretation: sequence values are not regular transactional table state. PostgreSQL pre-logs future sequence state, then later nextval calls can complete on the old primary without waiting for SyncRep until the prelogged window is exhausted."
  echo
  echo "## 3. Advisory locks"
  echo
  echo '```text'
  echo -n "old primary pg_try_advisory_lock($LOCK_KEY): "; cat "$LOGDIR/advisory-old.txt"
  echo -n "promoted primary pg_try_advisory_lock($LOCK_KEY): "; cat "$LOGDIR/advisory-new.txt"
  echo '```'
  echo
  echo "Both return true: advisory locks are shared-memory state, not replicated cluster state."
  echo
  echo "## 4. LISTEN/NOTIFY"
  echo
  echo "Promoted-primary listener while old primary NOTIFYs:"
  echo '```text'; cat "$LOGDIR/listener-new-during-old-notify.out"; echo '```'
  echo "Promoted-primary listener while promoted primary NOTIFYs:"
  echo '```text'; cat "$LOGDIR/listener-new-during-new-notify.out"; echo '```'
  echo
  echo "Expected: the listener sees only the local promoted-primary notification, not the old-primary notification."
  echo
  echo "## 5. Physical and logical replication slots"
  echo
  echo "Old primary slots:"
  echo '```text'; cat "$LOGDIR/slots-old.txt"; echo '```'
  echo "Promoted primary slots:"
  echo '```text'; cat "$LOGDIR/slots-new.txt"; echo '```'
  echo
  echo "User-created physical and logical replication slots are not ordinary WAL-replayed catalog rows; slots created after basebackup remain only on the old primary. PostgreSQL newer than this local test has explicit failover logical slots, but ordinary logical slots still need that failover/synchronization path to survive promotion."
  echo
  echo "## Explicitly excluded: temporary relations"
  echo
  echo "Temporary relation data can bypass WAL/SyncRep, but it is session-local and not visible on either standby/promoted primary. It is not durable cluster state and is therefore excluded from the main split-brain list."
  echo
  echo "## Raw logs"
  echo
  echo "Run logs were captured under: \`$LOGDIR\`"
} >>"$RESULTS"

log "Completed"
echo "Results: $RESULTS"
