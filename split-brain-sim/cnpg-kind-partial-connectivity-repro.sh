#!/usr/bin/env bash
set -Eeuo pipefail

# =============================================================================
# CNPG kind partial-connectivity split-brain race reproduction.
#
# Targets the race in the CNPG operator's failover decision path:
#
#   pkg/postgres/status.go
#     Less()                  : Error-bearing status records sort to the bottom
#                               so the operator picks the "most advanced
#                               REACHABLE" replica as the promotion candidate.
#     AreWalReceiversDown()   : When a replica status fetch failed, the
#                               replica's IsWalReceiverActive defaults to the
#                               Go zero value (false). The check therefore
#                               concludes "WAL receivers are down" even when
#                               the unreachable replicas are happily streaming
#                               from the primary at the PostgreSQL level.
#
# The race is asymmetric: the operator's HTTP /pg/status fetch fails for
# replicas it cannot reach, but the primary -> replica WAL stream is over a
# different network path (pod-to-pod TCP/5432) and is unaffected. If we time it
# right, the operator promotes a stale replica while sync standbys are still
# acknowledging commits to the old primary.
#
# Topology (5-instance CNPG cluster, sync ANY 2, failoverQuorum: true):
#
#   P  (splitbrain-1)  on node-1   : primary, accepting writes
#   R1 (splitbrain-2)  on node-2   : stale replica (partitioned from P at PG)
#   R2 (splitbrain-5)  on node-5   : stale replica (partitioned from P at PG)
#   R3 (splitbrain-3)  on node-3   : sync standby, ACK'ing commits
#   R4 (splitbrain-4)  on node-4   : sync standby, ACK'ing commits
#
# Two-stage partition:
#
#   Stage 1: Block PostgreSQL streaming traffic (port 5432) between P and the
#            non-sync standbys R1, R2 by iptables on the pods' kind nodes.
#            R3 and R4 keep streaming and stay in the sync quorum. R1, R2
#            fall behind. The operator still reaches everyone over the
#            instance manager status port (default 9187, HTTPS).
#
#   Stage 2: At the moment we trigger a failover, block CNPG's status port
#            (9187) on R3 and R4's kind nodes so the OPERATOR's status fetch
#            for those two pods fails. The 5432 streaming traffic between P
#            and R3/R4 stays untouched. Then we kill kubelet + force-delete
#            the P pod object to push the operator into a failover decision
#            while R3/R4 look "WAL receiver down" from the operator's POV.
#
# Failover trigger: stop kubelet on P's node and force-delete the P Pod object.
# Same pattern as cnpg-kind-sync5-kubelet-dead-repro.sh - this ensures CNPG
# moves on even though the old PostgreSQL container is still up and writable.
#
# Expected race outcome (race wins):
#   - Operator's status snapshot at the decision point lists R3, R4 with Error
#   - Sort puts R3, R4 at the bottom of the candidate list
#   - AreWalReceiversDown() returns true (R3, R4 are zero-value IsWalReceiverActive)
#   - Operator promotes R1 (most advanced reachable replica) - stale
#   - Meanwhile R3, R4 are still streaming and have ACK'd commits the operator
#     cannot see. Those committed-and-ACK'd writes are lost when R3, R4 later
#     rewind to follow the new primary.
#
# Three possible outcomes are documented in the results .md:
#   1. Race wins - split-brain with sync ACK'd writes lost.
#   2. Race lost - operator self-corrects (waits, re-reads, picks a sync standby).
#   3. Race blocked by failoverQuorum - the R+W>N check rejects the promotion.
#
# NOTE on partition mechanism:
#   We use iptables Option A (selective port blocking on the kind nodes).
#   - PG streaming: TCP/5432 to/from the pod IP
#   - Operator status fetch: TCP/9187 to/from the pod IP
#   Both ports terminate in the same pod, so we block on the pod's veth via
#   FORWARD rules matching destination pod IP + dport. Dropping inbound 9187
#   to R3/R4 pods kills the operator's status HTTP while leaving 5432 alone.
#
# Run outside docker-less environments. Requires: docker, kind, kubectl, jq.
# =============================================================================

CLUSTER_NAME=${CLUSTER_NAME:-cnpg-partial-conn}
NAMESPACE=${NAMESPACE:-$CLUSTER_NAME}
PG_CLUSTER=${PG_CLUSTER:-splitbrain}
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CNPG_MANIFEST=${CNPG_MANIFEST:-$REPO_ROOT/releases/cnpg-1.29.1.yaml}
# PG18 image. Adjust tag if CNPG image catalog has a different one available.
PG_IMAGE=${PG_IMAGE:-ghcr.io/cloudnative-pg/postgresql:18.0-bookworm}
PG_STATUS_PORT=${PG_STATUS_PORT:-9187}    # CNPG instance-manager status port
PG_PORT=${PG_PORT:-5432}                  # PostgreSQL wire protocol
OBSERVE_SECONDS=${OBSERVE_SECONDS:-180}
SLEEP=${SLEEP:-0.01}
POD_CIDR=${POD_CIDR:-10.244.0.0/16}
SERVICE_CIDR_HOST=${SERVICE_CIDR_HOST:-10.96.0.1}
KEEP_CLUSTER=${KEEP_CLUSTER:-0}
EVIDENCE_DIR=${EVIDENCE_DIR:-$PWD/split-brain-sim/evidence/${CLUSTER_NAME}-$(date -u +%Y%m%dT%H%M%SZ)}

mkdir -p "$EVIDENCE_DIR"
: >"$EVIDENCE_DIR/run.log"
: >"$EVIDENCE_DIR/timeline.log"
: >"$EVIDENCE_DIR/rules.txt"

PRIMARY_POD=""; PRIMARY_NODE=""; PRIMARY_IP=""
STALE_PODS=();  STALE_IPS=();   STALE_NODES=()
SYNC_PODS=();   SYNC_IPS=();    SYNC_NODES=()
PARTITIONED=0

log(){ printf '\n\033[1;36m==> %s\033[0m\n' "$*" | tee -a "$EVIDENCE_DIR/run.log"; }
run(){ printf '+ %s\n' "$*" | tee -a "$EVIDENCE_DIR/run.log"; "$@"; }
ts(){ python3 -c 'import time; print(int(time.time()*1000))'; }
ts_iso(){ date +%Y-%m-%dT%H:%M:%S%z; }

all_nodes(){ kind get nodes --name "$CLUSTER_NAME"; }
get_primary(){ kubectl get pods -n "$NAMESPACE" -l "cnpg.io/cluster=$PG_CLUSTER,cnpg.io/instanceRole=primary" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true; }
pod_ip(){ kubectl get pod -n "$NAMESPACE" "$1" -o jsonpath='{.status.podIP}'; }
pod_node(){ kubectl get pod -n "$NAMESPACE" "$1" -o jsonpath='{.spec.nodeName}'; }
node_ip(){ docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"; }
psql_pod(){ local pod=$1; shift; kubectl exec -i -n "$NAMESPACE" "$pod" -c postgres -- psql -U postgres app -X -v ON_ERROR_STOP=1 "$@"; }

cleanup(){
  set +e
  if [[ "$PARTITIONED" == 1 && -s "$EVIDENCE_DIR/rules.txt" ]]; then
    # Remove rules in reverse insertion order so the firewall is fully restored.
    tac "$EVIDENCE_DIR/rules.txt" 2>/dev/null | while IFS='|' read -r node chain rule; do
      docker exec "$node" iptables -D "$chain" $rule >/dev/null 2>&1 || true
    done
  fi
  if [[ "$KEEP_CLUSTER" != "1" ]]; then
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

add_rule(){
  # Insert at position 1 so later ACCEPTs land above earlier DROPs in chain order.
  local node=$1 chain=$2; shift 2
  run docker exec "$node" iptables -I "$chain" 1 "$@"
  printf '%s|%s|%s\n' "$node" "$chain" "$*" >>"$EVIDENCE_DIR/rules.txt"
}

# Capture operator + cluster + SQL state at a labelled instant.
sample(){
  local label=$1 p oldcid out
  out="$EVIDENCE_DIR/$(ts)-sample-$label.txt"
  {
    echo "### $(ts_iso) SAMPLE $label"
    echo "## cluster status"
    kubectl get cluster -n "$NAMESPACE" "$PG_CLUSTER" \
      -o jsonpath='{.status.phase}{" current="}{.status.currentPrimary}{" target="}{.status.targetPrimary}{" ready="}{.status.readyInstances}{" instances="}{.status.instances}{"\n"}' || true
    echo "## pods"
    kubectl get pods -n "$NAMESPACE" -o wide || true
    echo "## failoverquorum"
    kubectl get failoverquorum -n "$NAMESPACE" "$PG_CLUSTER" -o yaml 2>/dev/null || true
    echo "## operator log tail (last 40 lines)"
    kubectl logs -n cnpg-system deployment/cnpg-controller-manager --tail=40 2>/dev/null || true
    p=$(get_primary)
    if [[ -n "$p" ]]; then
      echo "## API-visible primary SQL: $p"
      timeout 10 kubectl exec -i -n "$NAMESPACE" "$p" -c postgres -- psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now(), pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) host, pg_current_wal_lsn();
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state,sent_lsn,write_lsn,flush_lsn,replay_lsn FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) FROM logged_loop_probe GROUP BY 1,2 ORDER BY 1,2;
SQL
    fi
    if [[ -n "$PRIMARY_POD" && -n "$PRIMARY_NODE" ]]; then
      echo "## direct old primary SQL: $PRIMARY_POD on $PRIMARY_NODE"
      oldcid=$(docker exec "$PRIMARY_NODE" crictl ps -q \
        --label "io.kubernetes.pod.namespace=$NAMESPACE,io.kubernetes.pod.name=$PRIMARY_POD" \
        --name postgres 2>/dev/null | head -1 || true)
      echo "old_cid=$oldcid"
      if [[ -n "$oldcid" ]]; then
        timeout 10 docker exec -i "$PRIMARY_NODE" crictl exec -i "$oldcid" psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now(), pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) host, pg_current_wal_lsn();
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state,sent_lsn,write_lsn,flush_lsn,replay_lsn FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) FROM logged_loop_probe GROUP BY 1,2 ORDER BY 1,2;
SQL
      fi
    fi
  } >"$out" 2>&1
}

# -----------------------------------------------------------------------------
# Stage 0: Bring up the kind cluster, operator, and CNPG cluster.
# -----------------------------------------------------------------------------
log "Create kind cluster with 1 control-plane + 5 workers"
cat >/tmp/$CLUSTER_NAME-kind.yaml <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
- role: worker
- role: worker
- role: worker
YAML
kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
run kind create cluster --name "$CLUSTER_NAME" --config /tmp/$CLUSTER_NAME-kind.yaml
kubectl config use-context "kind-$CLUSTER_NAME" >/dev/null

log "Install CloudNativePG operator from $CNPG_MANIFEST"
run kubectl apply --server-side -f "$CNPG_MANIFEST"
run kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=180s

log "Create namespace and 5-instance CNPG cluster with sync ANY 2 + failoverQuorum"
run kubectl create namespace "$NAMESPACE"
cat >/tmp/$CLUSTER_NAME-cluster.yaml <<YAML
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: $PG_CLUSTER
spec:
  instances: 5
  imageName: $PG_IMAGE
  postgresql:
    synchronous:
      method: any
      number: 2
      dataDurability: required
      failoverQuorum: true
    parameters:
      synchronous_commit: "on"
      log_replication_commands: "on"
  # Tight liveness so the operator gets nudged into failover quickly once we
  # delete the primary pod. Adjust if PG18 image needs longer warmup.
  livenessProbeTimeout: 10
  storage:
    size: 1Gi
YAML
run kubectl apply -n "$NAMESPACE" -f /tmp/$CLUSTER_NAME-cluster.yaml
run kubectl wait -n "$NAMESPACE" --for=condition=Ready cluster/$PG_CLUSTER --timeout=1200s
kubectl get pods -n "$NAMESPACE" -o wide | tee "$EVIDENCE_DIR/pods-initial.txt"

PRIMARY_POD=$(get_primary)
PRIMARY_NODE=$(pod_node "$PRIMARY_POD")
PRIMARY_IP=$(pod_ip "$PRIMARY_POD")
echo "$(ts_iso) primary=$PRIMARY_POD node=$PRIMARY_NODE ip=$PRIMARY_IP" | tee -a "$EVIDENCE_DIR/timeline.log"

# -----------------------------------------------------------------------------
# Workload: continuous synchronous-commit logged writes from every pod, tagged
# with origin pod name. Survives crashes (logged), so divergence after a rewind
# shows up as missing rows in the final SELECT.
# -----------------------------------------------------------------------------
log "Create logged probe table and start one continuous writer per pod"
psql_pod "$PRIMARY_POD" <<SQL
CREATE TABLE logged_loop_probe(
  id bigserial primary key,
  origin text not null,
  created_at timestamptz not null default clock_timestamp(),
  pg_is_in_recovery boolean not null default pg_is_in_recovery(),
  lsn pg_lsn not null default pg_current_wal_lsn()
);
SQL
for pod in $(kubectl get pods -n "$NAMESPACE" -l "cnpg.io/cluster=$PG_CLUSTER" -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
  kubectl exec -n "$NAMESPACE" "$pod" -c postgres -- bash -lc \
    "nohup bash -c 'while true; do psql -U postgres app -X -v ON_ERROR_STOP=0 -c \"SET synchronous_commit=on; INSERT INTO logged_loop_probe(origin) VALUES ('\''$pod'\'')\" >/controller/tmp/logged-loop.last 2>&1; sleep $SLEEP; done' >/controller/tmp/logged-loop.log 2>&1 & echo \$! >/controller/tmp/logged-loop.pid"
done
sleep 8
sample baseline

# -----------------------------------------------------------------------------
# Pick which replicas play the stale role (R1, R2) and which play the sync
# overlapping role (R3, R4). We let CNPG choose the initial sync set, then
# arbitrarily pick the first two as "keep streaming" (R3, R4) and the rest as
# "make stale" (R1, R2).
# -----------------------------------------------------------------------------
log "Read current quorum standbys from primary"
SYNC_STANDBYS=()
while IFS= read -r s; do
  [[ -n "$s" ]] && SYNC_STANDBYS+=("$s")
done < <(psql_pod "$PRIMARY_POD" -At <<'SQL'
SELECT application_name FROM pg_stat_replication WHERE sync_state='quorum' ORDER BY application_name;
SQL
)
printf '%s\n' "${SYNC_STANDBYS[@]}" >"$EVIDENCE_DIR/sync-standbys.txt"
if [[ ${#SYNC_STANDBYS[@]} -lt 4 ]]; then
  echo "Expected 4 quorum standbys, got ${#SYNC_STANDBYS[@]}" >&2
  exit 2
fi

# R3, R4 = first two: stay streaming, will become unreachable to operator at Stage 2.
SYNC_PODS=("${SYNC_STANDBYS[0]}" "${SYNC_STANDBYS[1]}")
# R1, R2 = the rest: partitioned from primary's PG port, fall behind, become
# the only reachable replicas to the operator.
STALE_PODS=("${SYNC_STANDBYS[2]}" "${SYNC_STANDBYS[3]}")

for p in "${SYNC_PODS[@]}";  do SYNC_IPS+=("$(pod_ip "$p")");  SYNC_NODES+=("$(pod_node "$p")"); done
for p in "${STALE_PODS[@]}"; do STALE_IPS+=("$(pod_ip "$p")"); STALE_NODES+=("$(pod_node "$p")"); done

echo "$(ts_iso) sync_overlap=${SYNC_PODS[*]} ips=${SYNC_IPS[*]} nodes=${SYNC_NODES[*]}" | tee -a "$EVIDENCE_DIR/timeline.log"
echo "$(ts_iso) make_stale=${STALE_PODS[*]} ips=${STALE_IPS[*]} nodes=${STALE_NODES[*]}" | tee -a "$EVIDENCE_DIR/timeline.log"

# -----------------------------------------------------------------------------
# Stage 1: Drop PG streaming between primary and the "stale" replicas R1, R2.
#
# We block TCP/5432 in BOTH directions between the primary pod IP and R1, R2's
# pod IPs. R3, R4 keep streaming and stay in the quorum. The operator's
# status fetch (port 9187) is unaffected for everyone.
# -----------------------------------------------------------------------------
log "Stage 1: drop PG streaming between primary and the stale replicas (R1, R2)"
for n in $(all_nodes); do
  for ip in "${STALE_IPS[@]}"; do
    add_rule "$n" FORWARD -s "$PRIMARY_IP" -d "$ip" -p tcp --dport $PG_PORT -j DROP
    add_rule "$n" FORWARD -d "$PRIMARY_IP" -s "$ip" -p tcp --dport $PG_PORT -j DROP
    add_rule "$n" FORWARD -s "$PRIMARY_IP" -d "$ip" -p tcp --sport $PG_PORT -j DROP
    add_rule "$n" FORWARD -d "$PRIMARY_IP" -s "$ip" -p tcp --sport $PG_PORT -j DROP
  done
done
PARTITIONED=1
echo "$(ts_iso) stage1-partition-applied primary=$PRIMARY_IP stale=${STALE_IPS[*]}" | tee -a "$EVIDENCE_DIR/timeline.log"

log "Wait ~20s and verify quorum still includes R3, R4 only; R1, R2 lag"
sleep 20
sample stage1-applied
psql_pod "$PRIMARY_POD" <<'SQL' | tee -a "$EVIDENCE_DIR/run.log" || true
SELECT pg_current_wal_lsn() AS primary_lsn;
SELECT application_name, state, sync_state, sent_lsn, flush_lsn, replay_lsn,
       pg_current_wal_lsn() - flush_lsn AS flush_lag_bytes
FROM pg_stat_replication ORDER BY application_name;
SQL

# Snapshot the LSNs that R3, R4 have ACK'd. These are the writes we expect
# to disappear if the operator promotes R1 (a stale replica).
psql_pod "$PRIMARY_POD" -At <<'SQL' >"$EVIDENCE_DIR/stage1-ackd-lsns.txt" || true
SELECT pg_current_wal_lsn();
SELECT application_name, flush_lsn FROM pg_stat_replication WHERE sync_state='quorum' ORDER BY application_name;
SQL

# -----------------------------------------------------------------------------
# Stage 2: At failover trigger time, block the OPERATOR's status fetch to
# R3 and R4 by dropping inbound TCP/9187 on R3, R4's kind nodes.
#
# PG streaming on 5432 stays open, so R3, R4 keep ACK'ing commits to the
# primary while the operator sees them as Error and treats their
# IsWalReceiverActive as the Go zero value false.
# -----------------------------------------------------------------------------
log "Stage 2: block CNPG status port $PG_STATUS_PORT to R3, R4 (operator status fetch only)"
for idx in "${!SYNC_PODS[@]}"; do
  node="${SYNC_NODES[$idx]}"
  ip="${SYNC_IPS[$idx]}"
  pod="${SYNC_PODS[$idx]}"
  # Block inbound to the pod IP on the status port from anywhere. This is
  # what the operator scrapes. PG/5432 stays open so streaming continues.
  add_rule "$node" FORWARD -d "$ip" -p tcp --dport $PG_STATUS_PORT -j DROP
  # Also block on INPUT in case the operator pod is colocated.
  add_rule "$node" INPUT   -d "$ip" -p tcp --dport $PG_STATUS_PORT -j DROP
  echo "$(ts_iso) status-port-blocked pod=$pod node=$node ip=$ip" | tee -a "$EVIDENCE_DIR/timeline.log"
done
sample stage2-status-port-blocked

# Wait a few seconds for the operator's next status scrape to actually fail
# and write the resulting "Error" snapshot into its in-memory list. The
# operator polls on the order of 1-2s so 5s is comfortable.
sleep 5

# -----------------------------------------------------------------------------
# Failover trigger: stop kubelet on the primary's node and force-delete the
# primary pod object. The PG container keeps running; the operator observes
# the API-side pod gone and starts the failover decision. This is the moment
# AreWalReceiversDown and the candidate sort are evaluated against the
# status snapshot we just poisoned.
# -----------------------------------------------------------------------------
log "Stop kubelet on primary node and force-delete primary pod"
echo "$(ts_iso) stop-kubelet primary_node=$PRIMARY_NODE" | tee -a "$EVIDENCE_DIR/timeline.log"
run docker exec "$PRIMARY_NODE" bash -lc \
  'systemctl stop kubelet || true; systemctl disable kubelet || true; pkill -9 kubelet || true; systemctl is-active kubelet || true'

echo "$(ts_iso) force-delete-primary-pod $PRIMARY_POD" | tee -a "$EVIDENCE_DIR/timeline.log"
run kubectl delete pod -n "$NAMESPACE" "$PRIMARY_POD" --grace-period=0 --force

# -----------------------------------------------------------------------------
# Observation loop: sample once per second; record the moment a new primary
# is observed; keep capturing operator logs.
# -----------------------------------------------------------------------------
log "Observe operator decisions for $OBSERVE_SECONDS seconds"
NEW_PRIMARY=""
for i in $(seq 1 "$OBSERVE_SECONDS"); do
  sample "t$i"
  p=$(get_primary)
  if [[ -n "$p" && "$p" != "$PRIMARY_POD" && -z "$NEW_PRIMARY" ]]; then
    NEW_PRIMARY=$p
    echo "$(ts_iso) new-primary-observed=$NEW_PRIMARY at_second=$i" | tee -a "$EVIDENCE_DIR/timeline.log"
  fi
  sleep 1
done

# -----------------------------------------------------------------------------
# Capture full operator log for the decision window.
# -----------------------------------------------------------------------------
log "Capture operator logs"
kubectl logs -n cnpg-system deployment/cnpg-controller-manager --tail=2000 \
  > "$EVIDENCE_DIR/operator.log" 2>&1 || true
grep -E 'failover|promot|TargetPrimary|WAL receiver|status|quorum|AreWalReceiversDown|skipping|candidate' \
  "$EVIDENCE_DIR/operator.log" > "$EVIDENCE_DIR/operator-decisions.log" 2>/dev/null || true

# -----------------------------------------------------------------------------
# Heal partition: remove all iptables rules in reverse order.
# -----------------------------------------------------------------------------
log "Heal partition"
tac "$EVIDENCE_DIR/rules.txt" 2>/dev/null | while IFS='|' read -r node chain rule; do
  docker exec "$node" iptables -D "$chain" $rule >/dev/null 2>&1 || true
done
PARTITIONED=0
echo "$(ts_iso) partition-end" | tee -a "$EVIDENCE_DIR/timeline.log"

# Restart kubelet on the old primary node so its pod can re-attach for cleanup.
docker exec "$PRIMARY_NODE" bash -lc 'systemctl enable kubelet || true; systemctl start kubelet || true' || true

sleep 30
sample final

# Final divergence diagnostic: count rows by origin on each visible primary.
log "Post-heal divergence snapshot"
FINAL_PRIMARY=$(get_primary)
if [[ -n "$FINAL_PRIMARY" ]]; then
  psql_pod "$FINAL_PRIMARY" <<'SQL' | tee "$EVIDENCE_DIR/final-rows-new-primary.txt" || true
SELECT origin, count(*), min(id) AS first_id, max(id) AS last_id, max(lsn) AS max_lsn
FROM logged_loop_probe GROUP BY origin ORDER BY origin;
SQL
fi

# -----------------------------------------------------------------------------
# Summary file: short text-only summary of the run.
# -----------------------------------------------------------------------------
{
  echo "# CNPG kind partial-connectivity split-brain race"
  echo
  echo "## Timeline"
  cat "$EVIDENCE_DIR/timeline.log"
  echo
  echo "## Stage 1 ACK'd LSNs (writes R3, R4 confirmed)"
  cat "$EVIDENCE_DIR/stage1-ackd-lsns.txt" 2>/dev/null || true
  echo
  echo "## Operator decisions excerpt"
  cat "$EVIDENCE_DIR/operator-decisions.log" 2>/dev/null | tail -80 || true
  echo
  echo "## Final per-origin row counts"
  cat "$EVIDENCE_DIR/final-rows-new-primary.txt" 2>/dev/null || true
  echo
  echo "Evidence directory: \`$EVIDENCE_DIR\`"
} >"$EVIDENCE_DIR/summary.md"
cat "$EVIDENCE_DIR/summary.md"
