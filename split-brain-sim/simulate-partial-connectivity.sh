#!/usr/bin/env bash
#
# Split-Brain Simulation — PARTIAL CONNECTIVITY variant
#
# Demonstrates that even with PostgreSQL synchronous replication properly
# configured (synchronous_standby_names = 'ANY 2 (...)', synchronous_commit = on),
# a PARTIAL network partition creates a window where an "operator-like"
# decision (promoting a less-advanced replica) discards transactions that
# were sync-committed AND ACK'd by quorum.
#
# Why R + W > N is not enough:
#   * In a CLEAN two-sided partition, the set K of replicas on the primary's
#     side and the set R of replicas the operator sees are DISJOINT. K >= W
#     (so the primary keeps committing) and R + W > N (so promotion is
#     allowed) cannot both hold — promotion is correctly blocked.
#   * In PARTIAL connectivity, some replicas live in BOTH K and R. The
#     intersection inflates K + R beyond N and the mutual exclusivity
#     breaks. Both sides can satisfy their quorums.
#
# CNPG's defenses:
#   1) pkg/postgres/status.go:282-291 — most-advanced REACHABLE replica
#      is picked (errors sort to the bottom).
#   2) pkg/postgres/status.go:331-342 — operator waits for all WAL receivers
#      to be down before promoting.
#
#   Defense (2) is FAIL-OPEN: when a replica is unreachable, IsWalReceiverActive
#   defaults to Go's zero value (false). "Can't reach" is treated as
#   "WAL receiver is down" and promotion proceeds.
#
# The flap: if the OVERLAPPING replicas (R3, R4) become temporarily unreachable
# to the operator at the moment of promotion, the operator promotes the
# most-advanced REACHABLE replica (R1, with stale data) while believing all
# WAL receivers are down. Meanwhile R3/R4 are still streaming from P and
# ACK'ing P's commits.
#
# Topology:
#   net-a (primary side):       P, R3, R4
#   net-b (operator side):      R1, R2, R3, R4
#   R3, R4 are on BOTH networks (the overlap).
#
# Prerequisites: Docker, Docker Compose
# Usage: ./simulate-partial-connectivity.sh
# Cleanup: docker compose -f docker-compose-5node.yml down -v
#
set -euo pipefail
cd "$(dirname "$0")"

COMPOSE_FILE="docker-compose-5node.yml"
PG_IMAGE="${PG_IMAGE:-postgres:18beta1}"
PG_PASS="postgres"
REPL_USER="replicator"
REPL_PASS="repl_secret"

NET_A="splitbrain-net-a"
NET_B="splitbrain-net-b"

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

dc() { docker compose -f "$COMPOSE_FILE" "$@"; }

sql_primary()  { docker exec -e PGPASSWORD=$PG_PASS sb5-primary  psql -U postgres -tAc "$1" 2>/dev/null; }
sql_r1()       { docker exec -e PGPASSWORD=$PG_PASS sb5-replica1 psql -U postgres -tAc "$1" 2>/dev/null; }
sql_r2()       { docker exec -e PGPASSWORD=$PG_PASS sb5-replica2 psql -U postgres -tAc "$1" 2>/dev/null; }
sql_r3()       { docker exec -e PGPASSWORD=$PG_PASS sb5-replica3 psql -U postgres -tAc "$1" 2>/dev/null; }
sql_r4()       { docker exec -e PGPASSWORD=$PG_PASS sb5-replica4 psql -U postgres -tAc "$1" 2>/dev/null; }

count_rows() {
    local svc="$1"
    docker exec -e PGPASSWORD=$PG_PASS "$svc" \
        psql -U postgres -tAc "SELECT count(*) FROM critical_data;" 2>/dev/null || echo "?"
}

count_critical() {
    local svc="$1"
    docker exec -e PGPASSWORD=$PG_PASS "$svc" \
        psql -U postgres -tAc "SELECT count(*) FROM critical_data WHERE source = 'sync-acked';" 2>/dev/null || echo "?"
}

# ===================================================================
banner "SPLIT-BRAIN — PARTIAL CONNECTIVITY (sync replication)"

cat <<'INTRO'
  This variant demonstrates that synchronous replication with quorum
  ACK is NOT sufficient to prevent data loss when the network partition
  is PARTIAL (some replicas reachable by BOTH primary and operator).

  Cluster:           P + R1 + R2 + R3 + R4    (N = 4 replicas)
  Sync policy:       synchronous_standby_names = 'ANY 2 (R1,R2,R3,R4)'
                     synchronous_commit = on
  Quorum math:       W (primary needs) = 2,  R (operator needs) = 3
                     R + W > N      →  3 + 2 > 4   ✓

  Networks:          net-a → P, R3, R4
                     net-b → R1, R2, R3, R4
                     R3 and R4 are on BOTH (the overlap).

  The flow:
    1. All five nodes up; sync replication is active and verified.
    2. Stage 1 partition: disconnect R1, R2 from net-a → R1, R2 fall
       behind. P continues to commit because R3, R4 ACK on net-a.
    3. P commits "critical" writes, each one sync-ACK'd by R3 and R4.
    4. Stage 2 flap: disconnect R3, R4 from net-b. From the OPERATOR's
       view, R3 and R4 are unreachable (Error → IsWalReceiverActive
       defaults to false). P is also unreachable on net-b. Only R1
       and R2 are visible — and R1 looks "most advanced" among them.
    5. Operator promotes R1 (mimicking CNPG's selection logic).
    6. Heal both networks. R3, R4, and P must pg_rewind onto R1's
       timeline. The sync-ACK'd transactions vanish.

INTRO

# ===================================================================
banner "Phase 1: Starting 5-node Cluster (P + R1..R4)"

step "Bringing up containers"
dc down -v 2>/dev/null || true
dc up -d primary
info "Primary container started"

step "Waiting for primary to accept connections"
until docker exec -e PGPASSWORD=$PG_PASS sb5-primary pg_isready -U postgres >/dev/null 2>&1; do
    sleep 1
done
sleep 2
info "Primary ready"

step "Creating replication user, HBA rules, and slots"
sql_primary "CREATE ROLE $REPL_USER WITH REPLICATION LOGIN PASSWORD '$REPL_PASS';"
hba_file=$(sql_primary "SHOW hba_file;")
docker exec sb5-primary bash -c "echo 'host replication $REPL_USER all md5' >> '$hba_file'"
docker exec sb5-primary bash -c "echo 'host all all all md5' >> '$hba_file'"
sql_primary "SELECT pg_reload_conf();" >/dev/null
for slot in slot_replica1 slot_replica2 slot_replica3 slot_replica4; do
    sql_primary "SELECT pg_create_physical_replication_slot('$slot');" >/dev/null
done
info "Replication user '$REPL_USER' created"
info "Replication slots created (slot_replica1..slot_replica4)"

step "Starting replicas (pg_basebackup + streaming)"
dc up -d replica1 replica2 replica3 replica4

step "Waiting for all 4 replicas to stream"
for attempt in $(seq 1 90); do
    streaming=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming';" || echo "0")
    if [[ "$streaming" == "4" ]]; then
        break
    fi
    sleep 2
done

streaming=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming';" || echo "0")
if [[ "$streaming" != "4" ]]; then
    fail "Expected 4 streaming replicas, got: $streaming"
    docker exec -e PGPASSWORD=$PG_PASS sb5-primary \
        psql -U postgres -c "SELECT application_name, client_addr, state FROM pg_stat_replication;"
    exit 1
fi
info "4/4 replicas streaming"

pg_version=$(sql_primary "SELECT version();")
info "PostgreSQL: $pg_version"

# ===================================================================
banner "Phase 2: Enable Synchronous Replication (ANY 2)"

step "Configuring synchronous_standby_names"
sql_primary "ALTER SYSTEM SET synchronous_standby_names = 'ANY 2 (replica1, replica2, replica3, replica4)';"
sql_primary "SELECT pg_reload_conf();" >/dev/null
sleep 2

sync_config=$(sql_primary "SHOW synchronous_standby_names;")
sync_commit=$(sql_primary "SHOW synchronous_commit;")
wal_hints=$(sql_primary "SHOW wal_log_hints;")
info "synchronous_standby_names = $sync_config"
info "synchronous_commit        = $sync_commit"
info "wal_log_hints             = $wal_hints"

step "Replication state across all 4 standbys"
docker exec -e PGPASSWORD=$PG_PASS sb5-primary \
    psql -U postgres -c "
    SELECT application_name, client_addr, state, sync_state, sync_priority
    FROM pg_stat_replication
    ORDER BY application_name;"

quorum_count=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE sync_state = 'quorum';" || echo "0")
if [[ "$quorum_count" != "4" ]]; then
    warn "Expected 4 quorum candidates, got: $quorum_count"
else
    info "All 4 replicas are sync 'quorum' candidates (W=2 ACKs needed)"
fi

cat <<'TOPO'

  TOPOLOGY (Phase 2 — initial steady state):

        ┌─────────────────────────────────────────────────────┐
        │  net-a            P ──► R3 ──► R4                   │
        │                                                     │
        │  net-b      P ──► R1, R2, R3, R4                    │
        └─────────────────────────────────────────────────────┘

  All four replicas are streaming from P. Sync ACK can come from
  any 2 of {R1, R2, R3, R4}.

TOPO

# ===================================================================
banner "Phase 3: Baseline Data (pre-partition)"

step "Creating critical_data table and 5 baseline rows"
sql_primary "CREATE TABLE critical_data (
    id serial PRIMARY KEY,
    value text NOT NULL,
    source text NOT NULL,
    committed_at timestamptz DEFAULT clock_timestamp()
);"

for i in $(seq 1 5); do
    sql_primary "INSERT INTO critical_data (value, source) VALUES ('baseline row $i', 'pre-partition');"
done
sleep 2

info "Baseline row counts:"
echo "    P  : $(count_rows sb5-primary)"
echo "    R1 : $(count_rows sb5-replica1)"
echo "    R2 : $(count_rows sb5-replica2)"
echo "    R3 : $(count_rows sb5-replica3)"
echo "    R4 : $(count_rows sb5-replica4)"

baseline_rows=$(count_rows sb5-primary)

# ===================================================================
banner "Phase 4: Stage 1 Partition — Isolate R1 & R2 from the Primary"

cat <<'STAGE1'
  Disconnecting R1 and R2 from net-a.
  Since R1 and R2 only reach P via net-b, AND P is still on net-b,
  this alone wouldn't disconnect them. So we disconnect R1 and R2
  from net-b's path to P by detaching them from net-b too? No — we
  need a different cut: we keep R1/R2 on net-b but DETACH P from
  net-b, so R1/R2 lose their path to P while R3/R4 continue
  streaming over net-a.

  Equivalent net effect:
    - P keeps net-a only          (talks to R3, R4)
    - R1, R2 keep net-b only      (still on net-b but P is gone)
    - R3, R4 remain on both

STAGE1

step "Disconnecting P from net-b (P now only on net-a)"
docker network disconnect "$NET_B" sb5-primary
info "P is reachable only on net-a (to R3, R4)"

step "Waiting for R1, R2 WAL receivers to notice the disconnect"
sleep 6

step "Replication state on P after Stage 1"
docker exec -e PGPASSWORD=$PG_PASS sb5-primary \
    psql -U postgres -c "
    SELECT application_name, client_addr, state, sync_state,
           pg_wal_lsn_diff(sent_lsn, flush_lsn) AS sent_minus_flush
    FROM pg_stat_replication
    ORDER BY application_name;"

active_now=$(sql_primary "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming';" || echo "?")
info "Streaming replicas now visible to P: $active_now (expected: 2 — R3, R4)"

cat <<'TOPO'

  TOPOLOGY (Phase 4 — Stage 1 partition):

        ┌─────────────────────────────────────────────────────┐
        │  net-a            P ──► R3 ──► R4                   │
        │                                                     │
        │  net-b           ✗P  R1, R2, R3, R4                 │
        └─────────────────────────────────────────────────────┘

  R1 and R2 are now stale — they can no longer pull WAL from P.
  R3 and R4 keep ACKing on net-a, so P's quorum W=2 is satisfied.

TOPO

# ===================================================================
banner "Phase 5: Critical Writes — Sync-Committed and ACK'd by R3, R4"

cat <<'PHASE5'
  Now P commits a batch of "critical" rows with synchronous_commit=on.
  Each COMMIT returns only after at least 2 of {R3, R4} flushed the
  WAL. We capture P's pg_current_wal_lsn() and the flush_lsn on R3,
  R4 from pg_stat_replication as proof.

PHASE5

step "Capturing pre-write LSN on primary"
pre_write_lsn=$(sql_primary "SELECT pg_current_wal_lsn();")
info "P LSN before critical writes: $pre_write_lsn"

step "Committing 10 critical rows (each sync-ACK'd by R3 + R4)"
for i in $(seq 1 10); do
    sql_primary "INSERT INTO critical_data (value, source)
                 VALUES ('CRITICAL sync-acked write #$i', 'sync-acked');" >/dev/null
done
info "10 critical rows committed (each one returned only after quorum ACK)"

sleep 2

post_write_lsn=$(sql_primary "SELECT pg_current_wal_lsn();")
info "P LSN after critical writes : $post_write_lsn"

step "Proof of ACK — flush_lsn on R3 and R4 from P's pg_stat_replication"
docker exec -e PGPASSWORD=$PG_PASS sb5-primary \
    psql -U postgres -c "
    SELECT application_name,
           sync_state,
           sent_lsn,
           flush_lsn,
           pg_wal_lsn_diff('$post_write_lsn', flush_lsn) AS bytes_behind
    FROM pg_stat_replication
    ORDER BY application_name;"

step "Row counts now (R1, R2 should be stuck at baseline)"
echo "    P  : $(count_rows sb5-primary)   (baseline + 10 critical)"
echo "    R1 : $(count_rows sb5-replica1)   (still baseline — isolated)"
echo "    R2 : $(count_rows sb5-replica2)   (still baseline — isolated)"
echo "    R3 : $(count_rows sb5-replica3)   (baseline + 10 — ACKed)"
echo "    R4 : $(count_rows sb5-replica4)   (baseline + 10 — ACKed)"

critical_committed=10

# ===================================================================
banner "Phase 6: Stage 2 Flap — R3 & R4 Disappear From the Operator"

cat <<'STAGE2'
  This is the partial-connectivity moment. The operator's view of the
  world is "net-b". To the operator:

    * P  — was already unreachable since Stage 1
    * R3 — now unreachable (we detach it from net-b)
    * R4 — now unreachable (we detach it from net-b)
    * R1 — reachable, has stale LSN
    * R2 — reachable, has stale LSN

  CNPG would call status.go's reachable-replica sort. R3 and R4 fall
  to the bottom (Error). R1 looks most-advanced among the reachable
  replicas. The WAL-receiver-wait defense queries each replica's
  IsWalReceiverActive; unreachable ones default to false (Go zero
  value). The operator believes all WAL receivers are down and
  promotes R1.

  Meanwhile, on net-a, R3 and R4 are STILL streaming from P, still
  ACKing sync commits. We could even do more sync writes here and
  they would succeed — but we won't, to keep the demo focused.

STAGE2

step "Disconnecting R3 from net-b"
docker network disconnect "$NET_B" sb5-replica3
info "R3 no longer reachable on net-b (still streams to P on net-a)"

step "Disconnecting R4 from net-b"
docker network disconnect "$NET_B" sb5-replica4
info "R4 no longer reachable on net-b (still streams to P on net-a)"

sleep 3

cat <<'TOPO'

  TOPOLOGY (Phase 6 — Stage 2 flap):

        ┌─────────────────────────────────────────────────────┐
        │  net-a            P ──► R3 ──► R4    (sync still OK)│
        │                                                     │
        │  net-b           ✗P  R1, R2  ✗R3  ✗R4                │
        │                  operator sees only R1, R2          │
        └─────────────────────────────────────────────────────┘

  Operator's view:
        R1 reachable — LSN at baseline  (looks most advanced)
        R2 reachable — LSN at baseline
        R3 Error     — assumed wal-receiver-down (fail-open)
        R4 Error     — assumed wal-receiver-down (fail-open)
        P  unreachable

TOPO

step "Verifying from operator's net-b vantage point"
# Run a probe container on net-b that tries to reach each replica
probe_image="$PG_IMAGE"
for tgt in sb5-primary sb5-replica1 sb5-replica2 sb5-replica3 sb5-replica4; do
    reach=$(docker run --rm --network "$NET_B" "$probe_image" \
        pg_isready -h "$tgt" -t 2 -U postgres 2>&1 || true)
    case "$reach" in
        *"accepting connections"*) echo "    $tgt : REACHABLE on net-b" ;;
        *)                         echo "    $tgt : unreachable on net-b" ;;
    esac
done

# ===================================================================
banner "Phase 7: Operator Promotes R1 (the wrong choice)"

cat <<'PROMOTE'
  The operator's "most-advanced reachable" pick is R1 — it has only
  the baseline LSN, because it was cut off in Stage 1. We promote
  R1, simulating CNPG's failover decision.

PROMOTE

r1_pre_promo_lsn=$(sql_r1 "SELECT pg_last_wal_replay_lsn();")
info "R1 replay LSN before promotion: $r1_pre_promo_lsn"

step "Promoting R1"
docker exec -e PGPASSWORD=$PG_PASS sb5-replica1 \
    psql -U postgres -c "SELECT pg_promote();" >/dev/null
sleep 3

is_recovery=$(sql_r1 "SELECT pg_is_in_recovery();")
if [[ "$is_recovery" == "f" ]]; then
    info "R1 is now the NEW PRIMARY (timeline advanced)"
else
    fail "R1 promotion failed (still in recovery: $is_recovery)"
    exit 1
fi

new_timeline=$(sql_r1 "SELECT timeline_id FROM pg_control_checkpoint();")
info "R1 timeline_id: $new_timeline"

step "Writing a few rows on the NEW primary (R1) to advance its timeline"
for i in $(seq 1 3); do
    sql_r1 "INSERT INTO critical_data (value, source)
            VALUES ('post-failover write #$i on R1', 'new-primary-R1');" >/dev/null
done
info "3 post-failover rows committed on R1"

# ===================================================================
banner "Phase 8: Heal — Reconnect Everything"

step "Reconnecting P to net-b"
docker network connect "$NET_B" sb5-primary || true
step "Reconnecting R3 to net-b"
docker network connect "$NET_B" sb5-replica3 || true
step "Reconnecting R4 to net-b"
docker network connect "$NET_B" sb5-replica4 || true
sleep 3
info "All networks restored"

step "State just after heal (BEFORE rewind)"
echo "    P  rows : $(count_rows sb5-primary)"
echo "    R1 rows : $(count_rows sb5-replica1) (new primary, advanced timeline)"
echo "    R2 rows : $(count_rows sb5-replica2)"
echo "    R3 rows : $(count_rows sb5-replica3) (still has sync-acked rows!)"
echo "    R4 rows : $(count_rows sb5-replica4) (still has sync-acked rows!)"

r3_before_rewind=$(count_critical sb5-replica3)
r4_before_rewind=$(count_critical sb5-replica4)
p_before_rewind=$(count_critical sb5-primary)

info "Sync-ACKed rows present on R3 before rewind: $r3_before_rewind / 10"
info "Sync-ACKed rows present on R4 before rewind: $r4_before_rewind / 10"
info "Sync-ACKed rows present on P  before rewind: $p_before_rewind / 10"

# ===================================================================
banner "Phase 9: pg_rewind — R3, R4, and P follow R1"

cat <<'REWIND'
  R3, R4, and P each carry a divergent timeline that includes the
  10 sync-ACKed rows. To rejoin the cluster they must rewind onto
  R1's timeline. That rewind DISCARDS the sync-ACKed rows.

REWIND

step "Checkpointing new primary R1"
sql_r1 "CHECKPOINT;" >/dev/null

rewind_one() {
    local svc="$1"
    local container="sb5-$svc"

    step "Stopping $container before rewind"
    docker stop "$container" >/dev/null
    info "$container stopped"

    step "Running pg_rewind for $container onto R1"
    set +e
    rewind_output=$(docker run --rm \
        --volumes-from "$container" \
        --network "$NET_B" \
        -e PGPASSWORD="$PG_PASS" \
        --user postgres \
        "$PG_IMAGE" \
        pg_rewind \
            --target-pgdata=/tmp/pgdata \
            --source-server="host=sb5-replica1 port=5432 user=postgres password=$PG_PASS dbname=postgres" \
            --progress 2>&1)
    rewind_exit=$?
    set -e
    while IFS= read -r line; do
        echo "    $line"
    done <<< "$rewind_output"
    if [[ $rewind_exit -ne 0 ]]; then
        fail "pg_rewind failed for $container (exit $rewind_exit)"
        exit $rewind_exit
    fi
    info "pg_rewind completed for $container"

    step "Reconfiguring $container as a standby of R1"
    docker run --rm \
        --volumes-from "$container" \
        --user postgres \
        "$PG_IMAGE" \
        bash -c "
            touch /tmp/pgdata/standby.signal
            sed -i '/^primary_conninfo/d' /tmp/pgdata/postgresql.auto.conf
            sed -i '/^primary_slot_name/d' /tmp/pgdata/postgresql.auto.conf
            echo \"primary_conninfo = 'host=sb5-replica1 port=5432 user=$REPL_USER password=$REPL_PASS'\" >> /tmp/pgdata/postgresql.auto.conf
        "
    docker start "$container" >/dev/null
    sleep 4

    local is_standby
    is_standby=$(docker exec -e PGPASSWORD=$PG_PASS "$container" \
        psql -U postgres -tAc "SELECT pg_is_in_recovery();" 2>/dev/null || echo "error")
    if [[ "$is_standby" == "t" ]]; then
        info "$container is now a standby of R1"
    else
        warn "$container recovery status: $is_standby"
    fi
}

rewind_one primary
rewind_one replica3
rewind_one replica4

sleep 3

# ===================================================================
banner "Phase 10: Final Data on Every Node"

step "Counts after rewind"
echo "    R1 (new primary) rows : $(count_rows sb5-replica1)"
echo "    R2 rows               : $(count_rows sb5-replica2)"
echo "    R3 rows (rewound)     : $(count_rows sb5-replica3)"
echo "    R4 rows (rewound)     : $(count_rows sb5-replica4)"
echo "    P  rows (rewound)     : $(count_rows sb5-primary)"

p_after=$(count_critical sb5-primary)
r3_after=$(count_critical sb5-replica3)
r4_after=$(count_critical sb5-replica4)

step "Rows from the 'sync-acked' batch surviving on each node"
echo "    R1 : $(count_critical sb5-replica1) / 10"
echo "    R2 : $(count_critical sb5-replica2) / 10"
echo "    R3 : $r3_after / 10"
echo "    R4 : $r4_after / 10"
echo "    P  : $p_after / 10"

echo ""
info "Final critical_data on the cluster (via R1):"
docker exec -e PGPASSWORD=$PG_PASS sb5-replica1 \
    psql -U postgres -c "
    SELECT id, left(value,45) AS value, source,
           committed_at::time(0) AS time
    FROM critical_data ORDER BY id;" 2>/dev/null

# ===================================================================
banner "RESULTS"

lost_on_r3=$((r3_before_rewind - r3_after))
lost_on_r4=$((r4_before_rewind - r4_after))
lost_on_p=$((p_before_rewind - p_after))

echo -e "${RED}${BOLD}"
cat <<EOF
  ╔════════════════════════════════════════════════════════════════════╗
  ║   PARTIAL CONNECTIVITY — SYNC-ACK'd WRITES LOST                    ║
  ╠════════════════════════════════════════════════════════════════════╣
  ║                                                                    ║
  ║  Configuration:                                                    ║
  ║    synchronous_standby_names = 'ANY 2 (replica1..replica4)'        ║
  ║    synchronous_commit        = on                                  ║
  ║    quorum math: R + W > N   →   3 + 2 > 4   ✓                      ║
  ║                                                                    ║
  ║  Pre-partition baseline rows committed       : $baseline_rows                   ║
  ║  Critical writes sync-committed & ACK'd      : $critical_committed                  ║
  ║                                                                    ║
  ║  Sync-ACK'd rows present BEFORE rewind:                            ║
  ║    on R3 : $r3_before_rewind / 10                                              ║
  ║    on R4 : $r4_before_rewind / 10                                              ║
  ║    on P  : $p_before_rewind / 10                                              ║
  ║                                                                    ║
  ║  Sync-ACK'd rows present AFTER rewind onto R1's timeline:          ║
  ║    on R3 : $r3_after / 10        (lost: $lost_on_r3)                            ║
  ║    on R4 : $r4_after / 10        (lost: $lost_on_r4)                            ║
  ║    on P  : $p_after / 10        (lost: $lost_on_p)                            ║
  ║                                                                    ║
  ╚════════════════════════════════════════════════════════════════════╝
EOF
echo -e "${NC}"

cat <<'EXPL'
  What this shows
  ───────────────
  Synchronous replication with ANY 2 quorum was active throughout.
  R3 and R4 BOTH flushed the 10 critical rows and the primary's
  COMMITs only returned after that quorum was satisfied. Application
  clients had every guarantee PostgreSQL can give that those writes
  were durable.

  The partial network partition put R3 and R4 in the intersection
  of "primary's side" and "operator's side". When the operator
  briefly lost net-b connectivity to R3 and R4, the most-advanced
  REACHABLE replica from the operator's vantage point was R1 —
  which had been isolated from P since Stage 1 and was missing all
  10 critical rows.

  CNPG's WAL-receiver-wait defense is fail-open: an unreachable
  replica's IsWalReceiverActive defaults to false, so the operator
  treats "can't reach" as "WAL receiver already down" and proceeds
  to promote.

  Because R3, R4, and P diverged from R1's new timeline, the only
  way to rejoin is pg_rewind, which discards their divergent WAL —
  including the sync-ACK'd transactions.

  This is the gap the quorum math doesn't close: R + W > N assumes
  K and R are disjoint; partial connectivity makes |K ∩ R| > 0 and
  the proof falls apart.

  Cleanup: docker compose -f docker-compose-5node.yml down -v
EXPL
