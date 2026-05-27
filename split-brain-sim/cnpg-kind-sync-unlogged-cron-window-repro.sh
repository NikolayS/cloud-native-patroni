#!/usr/bin/env bash
set -Eeuo pipefail

# CNPG normal-settings split-brain window hunter.
#
# Creates a CNPG cluster with synchronous replication and failoverQuorum enabled,
# runs an autonomous pg_cron workload that writes/commits to an UNLOGGED table
# every second, then partitions the old primary node alone by disconnecting it from
# the kind network. Evidence is collected from:
#   - Kubernetes/API side via kubectl
#   - isolated old-primary node via docker exec + crictl
#
# Intended cases:
#   INSTANCES=3 SYNC_NUMBER=1  # ANY 1
#   INSTANCES=5 SYNC_NUMBER=2  # ANY 2

CLUSTER_NAME=${CLUSTER_NAME:-cnpg-sync-unlogged-cron-${INSTANCES:-3}-${SYNC_NUMBER:-1}}
NAMESPACE=${NAMESPACE:-$CLUSTER_NAME}
PG_CLUSTER=${PG_CLUSTER:-splitbrain}
CNPG_MANIFEST=${CNPG_MANIFEST:-releases/cnpg-1.29.1.yaml}
PG_MAJOR=${PG_MAJOR:-17}
INSTANCES=${INSTANCES:-3}
SYNC_NUMBER=${SYNC_NUMBER:-1}
COMMITS_PER_TICK=${COMMITS_PER_TICK:-${ROWS_PER_TICK:-100}}
ROWS_PER_TICK=$COMMITS_PER_TICK
OBSERVE_SECONDS=${OBSERVE_SECONDS:-120}
KEEP_CLUSTER=${KEEP_CLUSTER:-0}
PG_CRON_IMAGE=${PG_CRON_IMAGE:-localhost/cnpg-pgcron:${PG_MAJOR}}
EVIDENCE_DIR=${EVIDENCE_DIR:-$PWD/split-brain-sim/evidence/${CLUSTER_NAME}-$(date -u +%Y%m%dT%H%M%SZ)}

PRIMARY_NODE=""
PRIMARY_POD=""
OLD_CID=""
RECONNECTED=0

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*" | tee -a "$EVIDENCE_DIR/run.log"; }
run() { printf '+ %s\n' "$*" | tee -a "$EVIDENCE_DIR/run.log"; "$@"; }

cleanup() {
  set +e
  if [[ -n "${PRIMARY_NODE:-}" && "$RECONNECTED" != "1" ]]; then
    docker network connect kind "$PRIMARY_NODE" >/dev/null 2>&1 || true
  fi
  if [[ "$KEEP_CLUSTER" != "1" ]]; then
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

get_primary() {
  kubectl get pods -n "$NAMESPACE" \
    -l "cnpg.io/cluster=$PG_CLUSTER,cnpg.io/instanceRole=primary" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
}

psql_pod() {
  local pod=$1; shift
  kubectl exec -i -n "$NAMESPACE" "$pod" -c postgres -- psql -U postgres app -X -v ON_ERROR_STOP=1 "$@"
}

old_sql() {
  docker exec -i "$PRIMARY_NODE" crictl exec -i "$OLD_CID" psql -U postgres app -X -v ON_ERROR_STOP=0 "$@"
}

ts_ms() { python3 -c "import time; print(int(time.time()*1000))"; }

sample_api() {
  local label=$1
  local out="$EVIDENCE_DIR/$(ts_ms)-api-${label}.txt"
  {
    echo "### $(date +%Y-%m-%dT%H:%M:%S%z) API $label"
    echo "## cluster"
    kubectl get cluster -n "$NAMESPACE" "$PG_CLUSTER" \
      -o jsonpath='{.status.phase}{" current="}{.status.currentPrimary}{" target="}{.status.targetPrimary}{" ready="}{.status.readyInstances}{" instances="}{.status.instances}{"\n"}' || true
    echo "## pods"
    kubectl get pods -n "$NAMESPACE" -o wide || true
    echo "## failoverquorum"
    kubectl get failoverquorum -n "$NAMESPACE" "$PG_CLUSTER" -o yaml 2>/dev/null || true
    local p
    p=$(get_primary)
    if [[ -n "$p" ]]; then
      echo "## visible primary SQL: $p"
      kubectl exec -i -n "$NAMESPACE" "$p" -c postgres -- psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now() AS sampled_at, pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) AS host;
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) rows
FROM unlogged_cron_probe GROUP BY 1,2 ORDER BY 1,2;
SELECT count(*) total_unlogged_rows, min(created_at), max(created_at) FROM unlogged_cron_probe;
SELECT jobid, schedule, active, jobname FROM cron.job ORDER BY jobid;
SELECT jobid, status, start_time, end_time, return_message FROM cron.job_run_details ORDER BY runid DESC LIMIT 5;
SQL
    fi
  } >"$out" 2>&1
}

sample_old() {
  local label=$1
  local out="$EVIDENCE_DIR/$(ts_ms)-old-${label}.txt"
  {
    echo "### $(date +%Y-%m-%dT%H:%M:%S%z) OLD $label"
    if ! docker exec "$PRIMARY_NODE" crictl ps --name postgres >/tmp/old-crictl.$$ 2>&1; then
      echo "crictl failed"
      cat /tmp/old-crictl.$$
      rm -f /tmp/old-crictl.$$
      return 0
    fi
    cat /tmp/old-crictl.$$
    rm -f /tmp/old-crictl.$$
    OLD_CID=$(docker exec "$PRIMARY_NODE" crictl ps -q \
      --label "io.kubernetes.pod.namespace=$NAMESPACE,io.kubernetes.pod.name=$PRIMARY_POD" \
      --name postgres | head -1)
    if [[ -z "$OLD_CID" ]]; then
      echo "old postgres container not running"
      return 0
    fi
    echo "old container: $OLD_CID"
    docker exec -i "$PRIMARY_NODE" crictl exec -i "$OLD_CID" psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now() AS sampled_at, pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) AS host;
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) rows
FROM unlogged_cron_probe GROUP BY 1,2 ORDER BY 1,2;
SELECT count(*) total_unlogged_rows, min(created_at), max(created_at) FROM unlogged_cron_probe;
SELECT jobid, schedule, active, jobname FROM cron.job ORDER BY jobid;
SELECT jobid, status, start_time, end_time, return_message FROM cron.job_run_details ORDER BY runid DESC LIMIT 5;
SQL
  } >"$out" 2>&1
}

write_summary() {
  local result="$EVIDENCE_DIR/summary.md"
  {
    echo "# CNPG sync unlogged pg_cron window hunt"
    echo
    echo "- Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "- Cluster: $CLUSTER_NAME"
    echo "- Instances: $INSTANCES"
    echo "- Sync: ANY $SYNC_NUMBER"
    echo "- dataDurability: required"
    echo "- failoverQuorum: true"
    echo "- isolationCheck: default enabled"
    echo "- Commits/rows per pg_cron tick: $COMMITS_PER_TICK"
    echo
    echo "## Timeline markers"
    cat "$EVIDENCE_DIR/timeline.log" 2>/dev/null || true
    echo
    echo "## Primary labels observed"
    grep -h "current=" "$EVIDENCE_DIR"/*-api-*.txt 2>/dev/null | sort | uniq -c || true
    echo
    echo "## Old-primary container observations"
    grep -h "old postgres container not running\|old container:\|pg_is_in_recovery" "$EVIDENCE_DIR"/*-old-*.txt 2>/dev/null || true
    echo
    echo "## Row-count snippets"
    grep -hE "^[[:space:]]*splitbrain-[0-9]+[[:space:]]*\|[[:space:]]*[ft][[:space:]]*\|" "$EVIDENCE_DIR"/*-{api,old}-*.txt 2>/dev/null || true
    echo
    echo "Evidence directory: \`$EVIDENCE_DIR\`"
  } >"$result"
  echo "$result"
}

mkdir -p "$EVIDENCE_DIR"
: >"$EVIDENCE_DIR/run.log"
: >"$EVIDENCE_DIR/timeline.log"

log "Building CNPG-compatible PostgreSQL image with pg_cron"
BUILD_DIR=$(mktemp -d /tmp/cnpg-pgcron-image.XXXXXX)
cat >"$BUILD_DIR/Dockerfile" <<DOCKERFILE
FROM ghcr.io/cloudnative-pg/postgresql:${PG_MAJOR}-standard-bookworm
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends postgresql-${PG_MAJOR}-cron \
    && rm -rf /var/lib/apt/lists/*
USER 26
DOCKERFILE
run docker build -t "$PG_CRON_IMAGE" "$BUILD_DIR"

log "Creating kind cluster with $((INSTANCES + 1)) nodes"
{
  echo "kind: Cluster"
  echo "apiVersion: kind.x-k8s.io/v1alpha4"
  echo "nodes:"
  echo "- role: control-plane"
  for _ in $(seq 1 "$INSTANCES"); do echo "- role: worker"; done
} >/tmp/${CLUSTER_NAME}-kind.yaml
kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
run kind create cluster --name "$CLUSTER_NAME" --config /tmp/${CLUSTER_NAME}-kind.yaml
kubectl config use-context "kind-$CLUSTER_NAME" >/dev/null
run kind load docker-image "$PG_CRON_IMAGE" --name "$CLUSTER_NAME"

log "Installing CNPG operator from $CNPG_MANIFEST"
run kubectl apply --server-side -f "$CNPG_MANIFEST"
run kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=180s

log "Creating CNPG cluster: instances=$INSTANCES, ANY $SYNC_NUMBER, normal isolation settings"
run kubectl create namespace "$NAMESPACE"
cat >/tmp/${CLUSTER_NAME}-cluster.yaml <<YAML
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: $PG_CLUSTER
spec:
  instances: $INSTANCES
  imageName: $PG_CRON_IMAGE
  postgresql:
    synchronous:
      method: any
      number: $SYNC_NUMBER
      dataDurability: required
      failoverQuorum: true
    shared_preload_libraries:
      - pg_cron
    parameters:
      cron.database_name: app
      cron.use_background_workers: "on"
      cron.log_run: "off"
      synchronous_commit: "on"
      log_min_messages: info
  storage:
    size: 1Gi
YAML
run kubectl apply -n "$NAMESPACE" -f /tmp/${CLUSTER_NAME}-cluster.yaml
run kubectl wait -n "$NAMESPACE" --for=condition=Ready "cluster/$PG_CLUSTER" --timeout=700s
kubectl get pods -n "$NAMESPACE" -o wide | tee "$EVIDENCE_DIR/pods-initial.txt"

PRIMARY_POD=$(get_primary)
PRIMARY_NODE=$(kubectl get pod -n "$NAMESPACE" "$PRIMARY_POD" -o jsonpath='{.spec.nodeName}')
OLD_CID=$(docker exec "$PRIMARY_NODE" crictl ps -q \
  --label "io.kubernetes.pod.namespace=$NAMESPACE,io.kubernetes.pod.name=$PRIMARY_POD" \
  --name postgres | head -1)
if [[ -z "$OLD_CID" ]]; then
  echo "Could not find old primary postgres container" >&2
  exit 2
fi
printf '%s old primary=%s node=%s cid=%s\n' "$(date +%Y-%m-%dT%H:%M:%S%z)" "$PRIMARY_POD" "$PRIMARY_NODE" "$OLD_CID" | tee -a "$EVIDENCE_DIR/timeline.log"

log "Install autonomous unlogged pg_cron workload"
psql_pod "$PRIMARY_POD" <<SQL
CREATE EXTENSION IF NOT EXISTS pg_cron;
CREATE UNLOGGED TABLE IF NOT EXISTS unlogged_cron_probe(
  origin text not null,
  created_at timestamptz not null default clock_timestamp(),
  pg_is_in_recovery boolean not null default pg_is_in_recovery()
);
CREATE OR REPLACE FUNCTION unlogged_cron_origin() RETURNS text LANGUAGE sql AS \$\$
  SELECT trim(both E'\n' from pg_read_file('/etc/hostname'))
\$\$;
CREATE OR REPLACE PROCEDURE unlogged_cron_write(n integer) LANGUAGE plpgsql AS \$\$
DECLARE
  v_origin text := unlogged_cron_origin();
BEGIN
  FOR i IN 1..n LOOP
    INSERT INTO unlogged_cron_probe(origin) VALUES (v_origin);
    COMMIT;
  END LOOP;
END
\$\$;
SELECT cron.schedule('unlogged-cron-writer', '1 second', 'CALL unlogged_cron_write($COMMITS_PER_TICK)');
CALL unlogged_cron_write($COMMITS_PER_TICK);
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state FROM pg_stat_replication ORDER BY 1;
SQL
sleep 5
sample_api baseline
sample_old baseline

log "Partition old primary node alone via docker network disconnect"
printf '%s partition-start node=%s old_primary=%s\n' "$(date +%Y-%m-%dT%H:%M:%S%z)" "$PRIMARY_NODE" "$PRIMARY_POD" | tee -a "$EVIDENCE_DIR/timeline.log"
run docker network disconnect kind "$PRIMARY_NODE"

NEW_PRIMARY=""
for i in $(seq 1 "$OBSERVE_SECONDS"); do
  sample_api "t${i}"
  sample_old "t${i}"
  p=$(get_primary)
  if [[ -n "$p" && "$p" != "$PRIMARY_POD" && -z "$NEW_PRIMARY" ]]; then
    NEW_PRIMARY="$p"
    printf '%s new-primary-observed=%s at_second=%s\n' "$(date +%Y-%m-%dT%H:%M:%S%z)" "$NEW_PRIMARY" "$i" | tee -a "$EVIDENCE_DIR/timeline.log"
  fi
  sleep 1
done

log "Reconnect old primary node"
docker network connect kind "$PRIMARY_NODE" >/dev/null 2>&1 || true
RECONNECTED=1
printf '%s partition-end node=%s\n' "$(date +%Y-%m-%dT%H:%M:%S%z)" "$PRIMARY_NODE" | tee -a "$EVIDENCE_DIR/timeline.log"
sleep 30
sample_api final
sample_old final || true

SUMMARY=$(write_summary)
log "Completed; summary: $SUMMARY"
cat "$SUMMARY"
