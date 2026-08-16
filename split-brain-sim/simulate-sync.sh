#!/usr/bin/env bash
#
# Split-Brain Simulation — SYNCHRONOUS REPLICATION variant
#
# Demonstrates two things:
#   1. With sync replication, normal writes on the isolated primary HANG
#      (the counterargument is correct for this case)
#   2. Session-level SET synchronous_commit = local BYPASSES the protection
#      and those writes are silently lost after pg_rewind
#
# Prerequisites: Docker, Docker Compose
# Usage: ./simulate-sync.sh
# Cleanup: docker compose down -v
#
set -euo pipefail
cd "$(dirname "$0")"

PG_PASS="postgres"
REPL_USER="replicator"
REPL_PASS="repl_secret"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

banner()  { echo -e "\n${BOLD}${CYAN}════════════════════════════════════════════════════${NC}"; echo -e "${BOLD}${CYAN}  $*${NC}"; echo -e "${BOLD}${CYAN}════════════════════════════════════════════════════${NC}\n"; }
info()    { echo -e "  ${GREEN}✓${NC} $*"; }
warn()    { echo -e "  ${YELLOW}⚠${NC} $*"; }
fail()    { echo -e "  ${RED}✗${NC} $*"; }
step()    { echo -e "\n  ${BOLD}→ $*${NC}"; }

sql_primary()  { docker exec -e PGPASSWORD=$PG_PASS sb-primary  psql -U postgres -tAc "$1" 2>/dev/null; }
sql_replica1() { docker exec -e PGPASSWORD=$PG_PASS sb-replica1 psql -U postgres -tAc "$1" 2>/dev/null; }

# ===================================================================
banner "SPLIT-BRAIN SIMULATION — SYNC REPLICATION"
echo "  This variant tests whether synchronous replication prevents"
echo "  split-brain writes on an isolated primary."
echo ""
echo "  Hypothesis (counterargument): with synchronous_standby_names"
echo "  configured, the old primary CANNOT commit because no standby"
echo "  can ACK the WAL."
echo ""
echo "  What we test:"
echo "    A) Normal write on isolated primary → should HANG (blocked)"
echo "    B) SET synchronous_commit = local → BYPASSES protection"
echo "    C) pg_rewind → bypassed writes are silently lost"
echo ""

# ===================================================================
banner "Phase 1: Starting PostgreSQL Cluster"

step "Bringing up containers (primary + 2 replicas)"
docker compose down -v 2>/dev/null || true
docker compose up -d primary
info "Primary container started"

step "Waiting for primary to accept connections"
until docker exec -e PGPASSWORD=$PG_PASS sb-primary pg_isready -U postgres >/dev/null 2>&1; do
    sleep 1
done
sleep 2
info "Primary ready"

step "Creating replication user and slots"
sql_primary "CREATE ROLE $REPL_USER WITH REPLICATION LOGIN PASSWORD '$REPL_PASS';"
docker exec sb-primary bash -c "echo 'host replication $REPL_USER all md5' >> /var/lib/postgresql/data/pg_hba.conf"
docker exec sb-primary bash -c "echo 'host all all all md5' >> /var/lib/postgresql/data/pg_hba.conf"
sql_primary "SELECT pg_reload_conf();" >/dev/null
sql_primary "SELECT pg_create_physical_replication_slot('slot_replica1');" >/dev/null
sql_primary "SELECT pg_create_physical_replication_slot('slot_replica2');" >/dev/null
info "Replication user '$REPL_USER' created"
info "Replication slots created"

step "Starting replicas (pg_basebackup + streaming)"
docker compose up -d replica1 replica2

step "Waiting for both replicas to stream"
for attempt in $(seq 1 60); do
    streaming=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming';" || echo "0")
    if [[ "$streaming" == "2" ]]; then
        break
    fi
    sleep 2
done

streaming=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming';" || echo "0")
if [[ "$streaming" != "2" ]]; then
    fail "Expected 2 streaming replicas, got: $streaming"
    docker exec -e PGPASSWORD=$PG_PASS sb-primary \
        psql -U postgres -c "SELECT pid, client_addr, state, sent_lsn, replay_lsn FROM pg_stat_replication;"
    exit 1
fi
info "2/2 replicas streaming"

pg_version=$(sql_primary "SELECT version();")
info "PostgreSQL: $pg_version"

# ===================================================================
banner "Phase 2: Enable Synchronous Replication"

step "Configuring synchronous_standby_names on primary"
sql_primary "ALTER SYSTEM SET synchronous_standby_names = 'ANY 1 (sb-replica1, sb-replica2)';"
sql_primary "SELECT pg_reload_conf();" >/dev/null
sleep 2

sync_config=$(sql_primary "SHOW synchronous_standby_names;")
sync_commit=$(sql_primary "SHOW synchronous_commit;")
info "synchronous_standby_names = $sync_config"
info "synchronous_commit = $sync_commit"

step "Verifying sync replication is active"
sync_state=$(sql_primary "SELECT string_agg(sync_state, ', ' ORDER BY application_name) FROM pg_stat_replication;")
info "Replica sync states: $sync_state"

step "Testing sync commit works (should succeed quickly)"
start_time=$(date +%s%N)
sql_primary "INSERT INTO pg_temp.test_sync DEFAULT VALUES;" 2>/dev/null || \
    sql_primary "CREATE TEMP TABLE test_sync AS SELECT 1; DROP TABLE test_sync;"
end_time=$(date +%s%N)
elapsed_ms=$(( (end_time - start_time) / 1000000 ))
info "Sync write completed in ${elapsed_ms}ms (standbys reachable)"

# ===================================================================
banner "Phase 3: Pre-Partition Data"

step "Inserting baseline data"
sql_primary "CREATE TABLE critical_data (
    id serial PRIMARY KEY,
    value text NOT NULL,
    source text NOT NULL,
    sync_mode text NOT NULL DEFAULT 'default',
    created_at timestamptz DEFAULT now()
);"

for i in $(seq 1 5); do
    sql_primary "INSERT INTO critical_data (value, source) VALUES ('baseline row $i', 'original-primary');"
done
sleep 2

info "Primary data:"
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -c "SELECT id, value, source FROM critical_data ORDER BY id;"

info "Replica1 data (should match):"
docker exec -e PGPASSWORD=$PG_PASS sb-replica1 \
    psql -U postgres -c "SELECT id, value, source FROM critical_data ORDER BY id;"

# ===================================================================
banner "Phase 4: NETWORK PARTITION"

echo -e "  ${RED}${BOLD}Simulating network failure on the primary node.${NC}"
echo ""

step "Disconnecting sb-primary from the network"
docker network disconnect splitbrain-net sb-primary
info "Primary is now ISOLATED"

docker exec sb-primary pg_isready -U postgres >/dev/null 2>&1 \
    && info "PostgreSQL on sb-primary: still ACCEPTING CONNECTIONS" \
    || fail "Primary not accepting connections"

sleep 3

# ===================================================================
banner "Phase 5: PROMOTE REPLICA"

step "Promoting sb-replica1 to primary"
docker exec -e PGPASSWORD=$PG_PASS sb-replica1 \
    psql -U postgres -c "SELECT pg_promote();" >/dev/null 2>&1
sleep 3

is_recovery=$(sql_replica1 "SELECT pg_is_in_recovery();")
if [[ "$is_recovery" == "f" ]]; then
    info "sb-replica1 is now the NEW PRIMARY"
else
    fail "Promotion failed"
    exit 1
fi

# ===================================================================
banner "Phase 6A: Test Normal Write on Isolated Primary"

echo -e "  ${BOLD}Testing: can the isolated primary commit a normal write?${NC}"
echo "  (synchronous_commit = on, synchronous_standby_names is set)"
echo "  Expected: COMMIT should HANG because no standby can ACK."
echo ""

step "Attempting synchronous write (5-second timeout)..."

# Use statement_timeout to avoid hanging forever
sync_write_result=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "
    SET statement_timeout = '5s';
    INSERT INTO critical_data (value, source, sync_mode)
    VALUES ('sync write attempt', 'OLD-primary', 'synchronous_commit=on')
    RETURNING id;" 2>&1 || true)

if echo "$sync_write_result" | grep -q "canceling statement due to statement timeout"; then
    info "CONFIRMED: synchronous write HUNG and timed out after 5 seconds"
    echo -e "  ${GREEN}The counterargument is correct: sync replication blocks the old primary.${NC}"
elif echo "$sync_write_result" | grep -qE "^[0-9]+$"; then
    fail "UNEXPECTED: synchronous write SUCCEEDED (id=$sync_write_result)"
    echo "  This should not happen with sync replication properly configured."
else
    warn "Write result: $sync_write_result"
fi

# ===================================================================
banner "Phase 6B: Test Session-Level Bypass"

echo -e "  ${BOLD}Testing: SET synchronous_commit = local${NC}"
echo "  This PostgreSQL GUC can be set per-session by any client."
echo "  It bypasses synchronous replication — commits return immediately"
echo "  without waiting for standby ACK."
echo ""

step "Writing with synchronous_commit = local (the BYPASS)"

docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "
    SET synchronous_commit = local;
    INSERT INTO critical_data (value, source, sync_mode)
    SELECT 'DOOMED: bypass write #' || g, 'OLD-primary-bypass', 'synchronous_commit=local'
    FROM generate_series(1, 10) g
    RETURNING '  committed: id=' || id || ' → ' || value;" 2>/dev/null

bypass_result=$?
if [[ $bypass_result -eq 0 ]]; then
    fail "BYPASS SUCCEEDED: 10 writes committed with synchronous_commit=local"
    echo -e "  ${RED}These writes bypassed sync replication and will be lost.${NC}"
else
    info "Bypass writes failed (unexpected)"
fi

echo ""

step "Simulating pg_cron with synchronous_commit=local (10 writes/sec, 3 seconds)"
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "
    SET synchronous_commit = local;
    DO \$\$
    BEGIN
        FOR i IN 1..30 LOOP
            INSERT INTO critical_data (value, source, sync_mode)
            VALUES ('DOOMED: pgcron-local tick #' || i, 'OLD-primary-pgcron', 'synchronous_commit=local');
            PERFORM pg_sleep(0.1);
        END LOOP;
    END \$\$;" 2>/dev/null
info "30 pg_cron ticks committed with synchronous_commit=local"

echo ""

# -- Writes to new primary --
step "Writing to NEW primary (sb-replica1)"
for i in $(seq 1 5); do
    sql_replica1 "INSERT INTO critical_data (value, source) VALUES ('post-failover write #$i', 'NEW-primary');"
done
info "5 writes committed on new primary"

# ===================================================================
banner "Phase 7: The Divergence"

old_total=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data;" 2>/dev/null)
old_bypass=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data WHERE source LIKE 'OLD%';" 2>/dev/null)
new_total=$(sql_replica1 "SELECT count(*) FROM critical_data;")
new_survived=$(sql_replica1 "SELECT count(*) FROM critical_data WHERE source = 'NEW-primary';")

echo -e "  ${BOLD}OLD primary (sb-primary) — isolated:${NC}"
echo "  Total rows: $old_total (including $old_bypass written via bypass during partition)"
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -c "
    SELECT id, left(value,40) AS value, source, sync_mode,
           created_at::time(0) AS time
    FROM critical_data ORDER BY id;" 2>/dev/null

echo ""
echo -e "  ${BOLD}NEW primary (sb-replica1):${NC}"
echo "  Total rows: $new_total (including $new_survived post-failover)"
docker exec -e PGPASSWORD=$PG_PASS sb-replica1 \
    psql -U postgres -c "
    SELECT id, left(value,40) AS value, source,
           created_at::time(0) AS time
    FROM critical_data ORDER BY id;" 2>/dev/null

# ===================================================================
banner "Phase 8: Reconnect + pg_rewind → Data Loss"

step "Reconnecting old primary to the network"
docker network connect splitbrain-net sb-primary
sleep 2
info "Network restored"

step "Checkpointing promoted primary"
sql_replica1 "CHECKPOINT;" >/dev/null 2>&1
sleep 1

step "Stopping PostgreSQL on old primary"
docker exec sb-primary su postgres -c "pg_ctl stop -D /var/lib/postgresql/data -m fast" 2>/dev/null || true
sleep 2
info "Old primary stopped"

step "Running pg_rewind"
docker exec sb-primary su postgres -c "
    pg_rewind \
        --target-pgdata=/var/lib/postgresql/data \
        --source-server='host=sb-replica1 port=5432 user=postgres password=$PG_PASS dbname=postgres' \
        --progress" 2>&1 | while read -r line; do
    echo "    $line"
done

rewind_exit=${PIPESTATUS[0]}
if [[ $rewind_exit -eq 0 ]]; then
    info "pg_rewind completed successfully"
else
    warn "pg_rewind exited with code $rewind_exit"
fi

step "Restarting old primary as standby"
docker exec sb-primary bash -c "
    touch /var/lib/postgresql/data/standby.signal
    sed -i '/^primary_conninfo/d' /var/lib/postgresql/data/postgresql.auto.conf
    sed -i '/^primary_slot_name/d' /var/lib/postgresql/data/postgresql.auto.conf
    echo \"primary_conninfo = 'host=sb-replica1 port=5432 user=$REPL_USER password=$REPL_PASS'\" >> /var/lib/postgresql/data/postgresql.auto.conf
"
docker exec sb-primary su postgres -c "pg_ctl start -D /var/lib/postgresql/data -l /tmp/pg.log" 2>/dev/null
sleep 3

is_standby=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT pg_is_in_recovery();" 2>/dev/null || echo "error")
if [[ "$is_standby" == "t" ]]; then
    info "Old primary is now running as STANDBY"
else
    warn "Old primary recovery status: $is_standby"
fi

step "Checking data on old primary AFTER pg_rewind"
post_rewind_total=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data;" 2>/dev/null || echo "?")
post_rewind_bypass=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data WHERE source LIKE 'OLD%';" 2>/dev/null || echo "0")

docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -c "
    SELECT id, left(value,40) AS value, source, sync_mode,
           created_at::time(0) AS time
    FROM critical_data ORDER BY id;" 2>/dev/null

# ===================================================================
banner "RESULTS"

lost=$((old_bypass - post_rewind_bypass))

echo -e "${RED}${BOLD}"
cat << EOF
  ╔══════════════════════════════════════════════════════════════╗
  ║           SYNC REPLICATION — SPLIT-BRAIN REPORT             ║
  ╠══════════════════════════════════════════════════════════════╣
  ║                                                              ║
  ║  Configuration:                                              ║
  ║    synchronous_standby_names = ANY 1 (sb-replica1, ...)      ║
  ║    synchronous_commit = on (server default)                  ║
  ║                                                              ║
  ║  Test A — Normal sync write on isolated primary:             ║
  ║    Result: HUNG (timed out) ← sync replication BLOCKS it     ║
  ║                                                              ║
  ║  Test B — SET synchronous_commit = local:                    ║
  ║    Bypass writes committed: $old_bypass                              ║
  ║    Bypass writes after pg_rewind: $post_rewind_bypass                         ║
  ║    WRITES SILENTLY LOST: $lost                               ║
  ║                                                              ║
  ║  The sync replication defense works for normal writes.       ║
  ║  But any session can SET synchronous_commit = local          ║
  ║  and bypass it entirely. CNPG cannot prevent this.           ║
  ║                                                              ║
  ╚══════════════════════════════════════════════════════════════╝
EOF
echo -e "${NC}"

echo "  Summary:"
echo "  ────────"
echo "  Sync replication (when configured — NOT the default) protects"
echo "  against normal split-brain writes. The R + W > N quorum math"
echo "  is correctly aligned: when promotion is allowed, the old"
echo "  primary's sync commits are guaranteed to hang."
echo ""
echo "  However, PostgreSQL's session-level synchronous_commit = local"
echo "  bypasses this protection entirely. Any client, extension, or"
echo "  background worker can set it. Writes committed with this"
echo "  setting succeed instantly and are lost after pg_rewind."
echo ""
echo "  A consensus-based lease would prevent BOTH cases: the primary"
echo "  would self-demote regardless of synchronous_commit settings."
echo ""
echo "  Cleanup: docker compose down -v"
echo ""
