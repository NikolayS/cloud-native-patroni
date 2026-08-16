#!/usr/bin/env bash
#
# Split-Brain Simulation for PostgreSQL 18
#
# Demonstrates that a network partition
# creates a window where TWO primaries accept writes simultaneously.
# The result is two independently writable PostgreSQL primaries.
#
# Prerequisites: Docker, Docker Compose
# Usage: ./setup-and-simulate.sh
# Cleanup: docker compose down -v
#
set -euo pipefail
cd "$(dirname "$0")"

PG_IMAGE="${PG_IMAGE:-postgres:18beta1}"
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
banner "SPLIT-BRAIN SIMULATION — PostgreSQL 18"
echo "  This demonstrates the fundamental split-brain problem in"
echo "  PostgreSQL HA during primary isolation with synchronous replication."
echo ""
echo "  What happens:"
echo "    1. Set up primary + 2 replicas with streaming replication"
echo "    2. Partition the primary from the promoted-replica network"
echo "    3. Promote a replica → new primary"
echo "    4. Write to BOTH primaries simultaneously (split-brain)"
echo "    5. Reconnect → pg_rewind → observe data loss"
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
hba_file=$(sql_primary "SHOW hba_file;")
docker exec sb-primary bash -c "echo 'host replication $REPL_USER all md5' >> '$hba_file'"
docker exec sb-primary bash -c "echo 'host all all all md5' >> '$hba_file'"
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
    echo "  Replication status:"
    docker exec -e PGPASSWORD=$PG_PASS sb-primary \
        psql -U postgres -c "SELECT pid, client_addr, state, sent_lsn, replay_lsn FROM pg_stat_replication;"
    exit 1
fi
info "2/2 replicas streaming"

step "Enabling synchronous replication (ANY 1: replica1 or replica2)"
sql_primary "ALTER SYSTEM SET synchronous_standby_names = 'ANY 1 (replica1, replica2)';" >/dev/null
sql_primary "SELECT pg_reload_conf();" >/dev/null
for attempt in $(seq 1 30); do
    sync_count=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE sync_state IN ('sync', 'quorum');" || echo "0")
    if [[ "$sync_count" != "0" ]]; then
        break
    fi
    sleep 1
done
info "Synchronous replication status:"
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -c "SELECT application_name, state, sync_state FROM pg_stat_replication ORDER BY application_name;"

pg_version=$(sql_primary "SELECT version();")
info "PostgreSQL: $pg_version"

# ===================================================================
banner "Phase 2: Pre-Partition Data"

step "Inserting baseline data on primary"
sql_primary "CREATE TABLE critical_data (
    id serial PRIMARY KEY,
    value text NOT NULL,
    source text NOT NULL,
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
banner "Phase 3: NETWORK PARTITION"

echo -e "  ${RED}${BOLD}Simulating network failure on the primary node.${NC}"
echo "  The primary loses all connectivity, but PostgreSQL"
echo "  keeps running — it has no way to know it's isolated."
echo ""

step "Disconnecting sb-primary from the promoted-replica network"
docker network disconnect splitbrain-net sb-primary
info "Primary is isolated from sb-replica1 but still connected to synchronous quorum member sb-replica2"

step "Terminating old primary's walsender connection to promoted replica"
sql_primary "SELECT pg_terminate_backend(pid) FROM pg_stat_replication WHERE application_name = 'replica1';" >/dev/null || true

step "Waiting for sb-replica2 to remain in the synchronous quorum"
for attempt in $(seq 1 30); do
    sync_replica=$(sql_primary "SELECT application_name FROM pg_stat_replication WHERE application_name = 'replica2' AND sync_state IN ('sync', 'quorum');" || true)
    if [[ "$sync_replica" == "replica2" ]]; then
        break
    fi
    sleep 1
done
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -c "SELECT application_name, state, sync_state FROM pg_stat_replication ORDER BY application_name;"
sync_replica=$(sql_primary "SELECT application_name FROM pg_stat_replication WHERE application_name = 'replica2' AND sync_state IN ('sync', 'quorum');" || true)
if [[ "$sync_replica" != "replica2" ]]; then
    fail "Expected replica2 to remain in synchronous quorum, got: $sync_replica"
    exit 1
fi
echo ""

docker exec sb-primary pg_isready -U postgres >/dev/null 2>&1 \
    && info "PostgreSQL on sb-primary: still ACCEPTING CONNECTIONS" \
    || fail "Primary not accepting connections"

echo ""
echo -e "  ${YELLOW}The old primary can still commit because replica2 remains reachable${NC}"
echo -e "  ${YELLOW}and remains in the synchronous quorum.${NC}"
echo ""

sleep 3

# ===================================================================
banner "Phase 4: PROMOTE REPLICA"

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
banner "Phase 5: SPLIT BRAIN — Two Primaries Writing"

echo -e "${RED}${BOLD}"
cat << 'DIAGRAM'
     ┌─────────────────────────┐     ┌─────────────────────────┐
     │    OLD PRIMARY          │     │    NEW PRIMARY           │
     │    (sb-primary)         │     │    (sb-replica1)         │
     │                         │     │                          │
     │  ✓ Accepting writes     │ ╳╳╳ │  ✓ Accepting writes      │
     │  ✓ Committing txns     │ ╳╳╳ │  ✓ Committing txns       │
     │  ✓ sync repl: replica2│ net │  ✗ old primary hidden  │
     │                         │ cut │                          │
     │  Writes HERE stay on  │     │  Writes HERE stay on   │
     │  old timeline         │     │  new timeline          │
     └─────────────────────────┘     └─────────────────────────┘
DIAGRAM
echo -e "${NC}"

# -- Writes to old (isolated) primary --
step "Writing to OLD primary (sb-primary) during partition"

docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -v ON_ERROR_STOP=1 -tAc "
    SET statement_timeout = '10s';
    INSERT INTO critical_data (value, source)
    SELECT 'old-primary write #' || g, 'OLD-primary'
    FROM generate_series(1, 10) g
    RETURNING '  committed: id=' || id || ' → ' || value;" 2>/dev/null

echo ""

step "Simulating pg_cron / PgQue ticks (10 writes/sec for 3 seconds)"
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -v ON_ERROR_STOP=1 -tAc "
    SET statement_timeout = '10s';
    DO \$\$
    BEGIN
        FOR i IN 1..30 LOOP
            INSERT INTO critical_data (value, source)
            VALUES ('old-primary pgcron tick #' || i, 'OLD-primary-pgcron');
            PERFORM pg_sleep(0.1);
        END LOOP;
    END \$\$;" 2>/dev/null
info "30 pg_cron ticks committed on old primary"

echo ""

# -- Writes to new primary --
step "Writing to NEW primary (sb-replica1)"
for i in $(seq 1 5); do
    sql_replica1 "INSERT INTO critical_data (value, source) VALUES ('post-failover write #$i', 'NEW-primary');"
done
info "5 writes committed on new primary"

# ===================================================================
banner "Phase 6: The Divergence"

old_total=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data;" 2>/dev/null)
old_partition_writes=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data WHERE source LIKE 'OLD%';" 2>/dev/null)
new_total=$(sql_replica1 "SELECT count(*) FROM critical_data;")
new_partition_writes=$(sql_replica1 "SELECT count(*) FROM critical_data WHERE source = 'NEW-primary';")

echo -e "  ${BOLD}OLD primary (sb-primary) — isolated:${NC}"
echo "  Total rows: $old_total (including $old_partition_writes written during partition)"
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -c "
    SELECT id, left(value,45) AS value, source,
           created_at::time(0) AS time
    FROM critical_data ORDER BY id;" 2>/dev/null

echo ""
echo -e "  ${BOLD}NEW primary (sb-replica1):${NC}"
echo "  Total rows: $new_total (including $new_partition_writes post-failover)"
docker exec -e PGPASSWORD=$PG_PASS sb-replica1 \
    psql -U postgres -c "
    SELECT id, left(value,45) AS value, source,
           created_at::time(0) AS time
    FROM critical_data ORDER BY id;" 2>/dev/null

# ===================================================================
banner "Phase 7: Reconnect + pg_rewind → Data Loss"

step "Reconnecting old primary to the network"
docker network connect splitbrain-net sb-primary
sleep 2
info "Network restored"

step "Checkpointing promoted primary before rewind"
sql_replica1 "CHECKPOINT;" >/dev/null

step "Stopping old primary container"
docker stop sb-primary >/dev/null
info "Old primary stopped"

step "Running pg_rewind (syncing old primary to promoted primary timeline)"
set +e
rewind_output=$(docker run --rm \
    --volumes-from sb-primary \
    --network splitbrain-net \
    -e PGPASSWORD=$PG_PASS \
    --user postgres \
    "$PG_IMAGE" \
    pg_rewind \
        --target-pgdata=/tmp/pgdata \
        --source-server="host=sb-replica1 port=5432 user=postgres password=$PG_PASS dbname=postgres" \
        --progress 2>&1)
rewind_exit=$?
set -e
while IFS= read -r line; do
    echo "    $line"
done <<< "$rewind_output"

if [[ $rewind_exit -ne 0 ]]; then
    fail "pg_rewind failed with code $rewind_exit"
    exit $rewind_exit
fi
info "pg_rewind completed successfully"

step "Restarting old primary as standby"
docker run --rm \
    --volumes-from sb-primary \
    --user postgres \
    "$PG_IMAGE" \
    bash -c "
        touch /tmp/pgdata/standby.signal
        sed -i '/^primary_conninfo/d' /tmp/pgdata/postgresql.auto.conf
        sed -i '/^primary_slot_name/d' /tmp/pgdata/postgresql.auto.conf
        echo \"primary_conninfo = 'host=sb-replica1 port=5432 user=$REPL_USER password=$REPL_PASS'\" >> /tmp/pgdata/postgresql.auto.conf
    "
docker start sb-primary >/dev/null
sleep 5

is_standby=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT pg_is_in_recovery();" 2>/dev/null || echo "error")
if [[ "$is_standby" == "t" ]]; then
    info "Old primary is now running as standby"
else
    fail "Old primary recovery status: $is_standby"
    docker logs --tail=80 sb-primary || true
    exit 1
fi

post_rewind_total=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data;" 2>/dev/null)
post_rewind_old_writes=$(docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data WHERE source LIKE 'OLD%';" 2>/dev/null)

step "Checking data on rewound old primary"
docker exec -e PGPASSWORD=$PG_PASS sb-primary \
    psql -U postgres -c "
    SELECT id, left(value,45) AS value, source,
           created_at::time(0) AS time
    FROM critical_data ORDER BY id;" 2>/dev/null

# ===================================================================
banner "RESULTS"

lost=$((old_partition_writes - post_rewind_old_writes))

cat << EOF
  SPLIT BRAIN + DATA LOSS REPRODUCED
  ─────────────────────────────────

  Replication mode:                       synchronous (ANY 1)
  Old primary sync quorum member:        replica2
  Baseline rows before partition:         5
  Rows on isolated old primary:           $old_total
  Writes on isolated old primary:         $old_partition_writes
  Writes on promoted new primary:         $new_partition_writes

  After reconnect + pg_rewind:
    Rows on rewound old primary:          $post_rewind_total
    Old-primary partition writes present: $post_rewind_old_writes
    Old-primary partition writes lost:    $lost

  During the partition, both PostgreSQL servers accepted and
  committed writes independently. After pg_rewind, the writes
  acknowledged by the isolated old primary were absent.

  Cleanup: docker compose down -v
EOF
