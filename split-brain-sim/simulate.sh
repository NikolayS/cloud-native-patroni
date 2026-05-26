#!/usr/bin/env bash
#
# Split-Brain Simulation for PostgreSQL 18 Streaming Replication
#
# Demonstrates that a network partition creates a window where TWO primaries accept
# writes simultaneously.
# The result is two independently writable PostgreSQL primaries.
#
# Usage: ./simulate.sh [--cleanup]
#
set -euo pipefail

PG_IMAGE="${PG_IMAGE:-postgres:18beta1}"
NET="splitbrain-net"
PRIMARY="sb-primary"
REPLICA1="sb-replica1"
REPLICA2="sb-replica2"
REPL_USER="replicator"
REPL_PASS="repl_secret"
PG_PASS="postgres"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

banner()  { echo -e "\n${BOLD}${CYAN}=== $* ===${NC}\n"; }
info()    { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()    { echo -e "${YELLOW}[WARN]${NC} $*"; }
fail()    { echo -e "${RED}[FAIL]${NC} $*"; }
divider() { echo -e "${CYAN}$(printf '%.0s─' {1..60})${NC}"; }

sql_primary()  { docker exec -e PGPASSWORD=$PG_PASS $PRIMARY psql -U postgres -tAc "$1" 2>/dev/null; }
sql_replica1() { docker exec -e PGPASSWORD=$PG_PASS $REPLICA1 psql -U postgres -tAc "$1" 2>/dev/null; }
sql_replica2() { docker exec -e PGPASSWORD=$PG_PASS $REPLICA2 psql -U postgres -tAc "$1" 2>/dev/null; }

cleanup() {
    banner "Cleanup"
    docker rm -f $PRIMARY $REPLICA1 $REPLICA2 2>/dev/null || true
    docker network rm $NET 2>/dev/null || true
    info "Cleaned up containers and network"
}

if [[ "${1:-}" == "--cleanup" ]]; then
    cleanup
    exit 0
fi

trap cleanup EXIT

# -------------------------------------------------------------------
banner "SPLIT-BRAIN SIMULATION"
echo "This simulation demonstrates a split-brain scenario in"
echo "PostgreSQL streaming replication during primary isolation."
echo ""
echo "Image: $PG_IMAGE"
divider

# -------------------------------------------------------------------
banner "Phase 1: Infrastructure Setup"

info "Creating Docker network: $NET"
docker network create $NET 2>/dev/null || true

info "Starting primary: $PRIMARY"
docker run -d --name $PRIMARY --network $NET \
    -e POSTGRES_PASSWORD=$PG_PASS \
    -e POSTGRES_HOST_AUTH_METHOD=md5 \
    -e PGDATA=/tmp/pgdata \
    $PG_IMAGE \
    -c wal_level=replica \
    -c max_wal_senders=10 \
    -c max_replication_slots=10 \
    -c synchronous_commit=on \
    -c hot_standby=on \
    -c wal_log_hints=on \
    -c listen_addresses='*' >/dev/null

info "Waiting for primary to be ready..."
until docker exec -e PGPASSWORD=$PG_PASS $PRIMARY pg_isready -U postgres >/dev/null 2>&1; do
    sleep 1
done
sleep 2

info "Configuring replication user and pg_hba"
sql_primary "CREATE ROLE $REPL_USER WITH REPLICATION LOGIN PASSWORD '$REPL_PASS';"

hba_file=$(sql_primary "SHOW hba_file;")
docker exec $PRIMARY bash -c "cat >> '$hba_file' <<HBA
host replication $REPL_USER all md5
host all all all md5
HBA"
sql_primary "SELECT pg_reload_conf();" >/dev/null

info "Creating replication slots"
sql_primary "SELECT pg_create_physical_replication_slot('slot_replica1');" >/dev/null
sql_primary "SELECT pg_create_physical_replication_slot('slot_replica2');" >/dev/null

# -------------------------------------------------------------------
banner "Phase 2: Creating Replicas via pg_basebackup"

for replica in $REPLICA1 $REPLICA2; do
    slot_name="slot_${replica/sb-/}"
    info "Creating $replica from basebackup..."

    docker run -d --name $replica --network $NET \
        -e POSTGRES_PASSWORD=$PG_PASS \
        -e PGPASSWORD=$REPL_PASS \
        -e PGDATA=/tmp/pgdata \
        $PG_IMAGE \
        -c hot_standby=on \
        -c wal_log_hints=on >/dev/null

    until docker exec $replica pg_isready -U postgres >/dev/null 2>&1; do
        sleep 1
    done
    docker exec $replica pg_ctlcluster 18 main stop 2>/dev/null || \
        docker stop $replica >/dev/null

    docker start $replica >/dev/null 2>/dev/null || true
    sleep 1

    docker rm -f $replica >/dev/null 2>/dev/null

    docker run -d --name $replica --network $NET \
        -e POSTGRES_PASSWORD=$PG_PASS \
        -e PGPASSWORD=$REPL_PASS \
        -e PGDATA=/tmp/pgdata \
        $PG_IMAGE bash -c "
        rm -rf /tmp/pgdata/*
        PGPASSWORD=$REPL_PASS pg_basebackup -h $PRIMARY -U $REPL_USER \
            -D /tmp/pgdata -Fp -Xs -P -R \
            --slot=$slot_name 2>&1
        echo \"primary_slot_name = '$slot_name'\" >> /tmp/pgdata/postgresql.auto.conf
        chown -R postgres:postgres /tmp/pgdata
        chmod 700 /tmp/pgdata
        exec docker-entrypoint.sh postgres -c hot_standby=on -c wal_log_hints=on
    " >/dev/null
done

info "Waiting for replicas to start streaming..."
sleep 5

for i in {1..30}; do
    streaming=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming';")
    if [[ "$streaming" == "2" ]]; then
        break
    fi
    sleep 1
done

streaming=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming';")
if [[ "$streaming" != "2" ]]; then
    fail "Expected 2 streaming replicas, got: $streaming"
    sql_primary "SELECT client_addr, state, sent_lsn, replay_lsn FROM pg_stat_replication;"
    exit 1
fi
info "Both replicas streaming successfully"

# -------------------------------------------------------------------
banner "Phase 3: Initial Data — Writing to Primary"

sql_primary "CREATE TABLE critical_data (
    id serial PRIMARY KEY,
    value text NOT NULL,
    written_by text NOT NULL,
    ts timestamptz DEFAULT now()
);"

for i in $(seq 1 5); do
    sql_primary "INSERT INTO critical_data (value, written_by) VALUES ('pre-partition row $i', 'original-primary');"
done

sleep 2
info "Data on primary:"
sql_primary "SELECT id, value, written_by FROM critical_data ORDER BY id;"
info "Data replicated to replica1:"
sql_replica1 "SELECT id, value, written_by FROM critical_data ORDER BY id;"

divider

# -------------------------------------------------------------------
banner "Phase 4: NETWORK PARTITION"
echo -e "${RED}${BOLD}Disconnecting $PRIMARY from the network${NC}"
echo "This simulates a network partition where the primary node"
echo "loses connectivity but PostgreSQL keeps running."
echo ""

docker network disconnect $NET $PRIMARY
info "Primary disconnected from $NET"
info "Primary PG process is still running:"
docker exec $PRIMARY pg_isready -U postgres 2>/dev/null && info "  -> PostgreSQL on $PRIMARY: ACCEPTING CONNECTIONS" || true

sleep 3

# -------------------------------------------------------------------
banner "Phase 5: PROMOTE REPLICA (New Primary)"
echo "In a real HA system, the operator/controller would detect"
echo "the primary is unreachable and promote a replica."
echo ""

info "Promoting $REPLICA1 to primary..."
docker exec -e PGPASSWORD=$PG_PASS $REPLICA1 psql -U postgres -c "SELECT pg_promote();" >/dev/null 2>&1
sleep 3

is_recovery=$(sql_replica1 "SELECT pg_is_in_recovery();")
if [[ "$is_recovery" == "f" ]]; then
    info "$REPLICA1 is now the NEW PRIMARY"
else
    fail "$REPLICA1 failed to promote"
    exit 1
fi

# -------------------------------------------------------------------
banner "Phase 6: SPLIT BRAIN — Two Concurrent Writers"
echo -e "${RED}${BOLD}"
echo "  ┌─────────────────────────────────────────────────┐"
echo "  │     SPLIT BRAIN: TWO PRIMARIES ARE ACTIVE       │"
echo "  │                                                 │"
echo "  │  Old primary ($PRIMARY): accepting writes  │"
echo "  │  New primary ($REPLICA1): accepting writes  │"
echo "  └─────────────────────────────────────────────────┘"
echo -e "${NC}"

info "Writing to OLD primary ($PRIMARY) during the partition:"
for i in $(seq 1 10); do
    docker exec -e PGPASSWORD=$PG_PASS $PRIMARY \
        psql -U postgres -tAc \
        "INSERT INTO critical_data (value, written_by) VALUES ('old-primary write #$i during partition', 'OLD-primary') RETURNING id, value;" \
        2>/dev/null
done

info "Simulating pg_cron / PgQue ticking (10 writes/sec for 3 seconds):"
docker exec -e PGPASSWORD=$PG_PASS $PRIMARY \
    psql -U postgres -tAc "
    DO \$\$
    BEGIN
        FOR i IN 1..30 LOOP
            INSERT INTO critical_data (value, written_by)
            VALUES ('pg_cron tick #' || i, 'OLD-primary-pgcron');
            PERFORM pg_sleep(0.1);
        END LOOP;
    END \$\$;" 2>/dev/null

info "Writing to NEW primary ($REPLICA1):"
for i in $(seq 1 5); do
    sql_replica1 "INSERT INTO critical_data (value, written_by) VALUES ('post-failover write #$i', 'NEW-primary') RETURNING id, value;"
done

divider

# -------------------------------------------------------------------
banner "Phase 7: Examining the Divergence"

old_count=$(docker exec -e PGPASSWORD=$PG_PASS $PRIMARY psql -U postgres -tAc "SELECT count(*) FROM critical_data;" 2>/dev/null)
new_count=$(sql_replica1 "SELECT count(*) FROM critical_data;")

echo -e "${BOLD}OLD primary ($PRIMARY):${NC}"
echo "  Total rows: $old_count"
echo "  Data:"
docker exec -e PGPASSWORD=$PG_PASS $PRIMARY \
    psql -U postgres -c "SELECT id, left(value,50) as value, written_by, ts FROM critical_data ORDER BY id;" \
    2>/dev/null

echo ""
echo -e "${BOLD}NEW primary ($REPLICA1):${NC}"
echo "  Total rows: $new_count"
echo "  Data:"
sql_replica1 "SELECT id, left(value,50) as value, written_by, ts FROM critical_data ORDER BY id;" 2>/dev/null || \
    docker exec -e PGPASSWORD=$PG_PASS $REPLICA1 \
        psql -U postgres -c "SELECT id, left(value,50) as value, written_by, ts FROM critical_data ORDER BY id;"

divider

old_partition_count=$(docker exec -e PGPASSWORD=$PG_PASS $PRIMARY \
    psql -U postgres -tAc "SELECT count(*) FROM critical_data WHERE written_by LIKE 'OLD%';" 2>/dev/null)

echo ""
echo -e "${RED}${BOLD}SPLIT-BRAIN RESULT:${NC}"
echo -e "  Old primary has ${RED}${BOLD}$old_count${NC} rows ($old_partition_count written during partition)"
echo -e "  New primary has ${GREEN}${BOLD}$new_count${NC} rows"
echo -e "  ${RED}${BOLD}$old_partition_count rows${NC} were committed on the isolated old primary"
echo ""

# -------------------------------------------------------------------
banner "RESULTS"

echo ""
echo "SPLIT BRAIN REPRODUCED"
echo "──────────────────────"
echo "Old primary rows:          $old_count"
echo "Old primary partition rows: $old_partition_count"
echo "New primary rows:          $new_count"
echo ""
echo "Both PostgreSQL servers accepted and committed writes during the partition."
echo "Cleanup: ./simulate.sh --cleanup"
