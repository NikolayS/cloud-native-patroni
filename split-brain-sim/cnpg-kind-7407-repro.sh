#!/usr/bin/env bash
set -Eeuo pipefail

# Reproduce the split-brain/data-loss behavior reported in upstream
# cloudnative-pg/cloudnative-pg#7407 with a real kind cluster and the
# CloudNativePG operator. This intentionally matches the original issue shape:
# a 3-instance CNPG Cluster without synchronous replication, with the liveness
# pinger disabled, and a kind node network partition via docker network disconnect.

CLUSTER_NAME=${CLUSTER_NAME:-cnpg-7407}
NAMESPACE=${NAMESPACE:-cnpg-7407}
PG_CLUSTER=${PG_CLUSTER:-splitbrain}
CNPG_MANIFEST=${CNPG_MANIFEST:-releases/cnpg-1.25.1.yaml}
KEEP_CLUSTER=${KEEP_CLUSTER:-0}

PRIMARY_NODE=""

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
run() { printf '+ %s\n' "$*"; "$@"; }

cleanup() {
  set +e
  if [[ -n "${PRIMARY_NODE:-}" ]]; then
    docker network connect kind "$PRIMARY_NODE" >/dev/null 2>&1 || true
  fi
  if [[ "$KEEP_CLUSTER" != "1" ]]; then
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

log "Creating kind cluster"
cat >/tmp/cnpg-7407-kind.yaml <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
- role: worker
YAML
kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
run kind create cluster --name "$CLUSTER_NAME" --config /tmp/cnpg-7407-kind.yaml
kubectl config use-context "kind-$CLUSTER_NAME" >/dev/null

log "Installing CNPG operator from $CNPG_MANIFEST"
run kubectl apply --server-side -f "$CNPG_MANIFEST"
run kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=180s

log "Creating CNPG Cluster matching upstream #7407: 3 instances, liveness pinger disabled, no synchronous replication"
run kubectl create namespace "$NAMESPACE"
cat >/tmp/cnpg-7407-cluster.yaml <<YAML
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: $PG_CLUSTER
  annotations:
    alpha.cnpg.io/livenessPinger: '{"enabled": false}'
spec:
  instances: 3
  postgresql:
    parameters:
      log_replication_commands: "on"
      log_statement: "ddl"
  storage:
    size: 1Gi
YAML
run kubectl apply -n "$NAMESPACE" -f /tmp/cnpg-7407-cluster.yaml
run kubectl wait -n "$NAMESPACE" --for=condition=Ready "cluster/$PG_CLUSTER" --timeout=600s
kubectl get pods -n "$NAMESPACE" -o wide

get_primary() {
  kubectl get pods -n "$NAMESPACE" \
    -l "cnpg.io/cluster=$PG_CLUSTER,cnpg.io/instanceRole=primary" \
    -o jsonpath='{.items[0].metadata.name}'
}

PRIMARY_POD=$(get_primary)
PRIMARY_NODE=$(kubectl get pod -n "$NAMESPACE" "$PRIMARY_POD" -o jsonpath='{.spec.nodeName}')

log "Initial topology"
echo "old primary: $PRIMARY_POD on $PRIMARY_NODE"
kubectl exec -i -n "$NAMESPACE" "$PRIMARY_POD" -c postgres -- psql -U postgres app -v ON_ERROR_STOP=1 <<'SQL'
CREATE TABLE IF NOT EXISTS splitbrain_probe(
  id bigserial primary key,
  source text,
  note text,
  ts timestamptz default clock_timestamp()
);
INSERT INTO splitbrain_probe(source,note)
SELECT 'baseline', 'before partition ' || g FROM generate_series(1,5) g;
SELECT pg_is_in_recovery() AS is_replica, count(*) AS baseline_rows FROM splitbrain_probe;
SQL

log "Disconnecting the kind node that hosts the primary"
run docker network disconnect kind "$PRIMARY_NODE"

echo "Waiting for CNPG to promote a replacement primary..."
NEW_PRIMARY=""
for _ in $(seq 1 150); do
  NEW_PRIMARY=$(kubectl get pods -n "$NAMESPACE" \
    -l "cnpg.io/cluster=$PG_CLUSTER,cnpg.io/instanceRole=primary" \
    -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [[ -n "$NEW_PRIMARY" && "$NEW_PRIMARY" != "$PRIMARY_POD" ]]; then
    break
  fi
  sleep 2
done

echo "old primary: $PRIMARY_POD"
echo "new primary: $NEW_PRIMARY"
if [[ -z "$NEW_PRIMARY" || "$NEW_PRIMARY" == "$PRIMARY_POD" ]]; then
  kubectl get pods -n "$NAMESPACE" -o wide || true
  kubectl get cluster -n "$NAMESPACE" "$PG_CLUSTER" -o yaml || true
  exit 3
fi
kubectl get pods -n "$NAMESPACE" -o wide

log "Writing locally inside isolated old primary via crictl on the disconnected kind node"
OLD_CID=$(docker exec "$PRIMARY_NODE" crictl ps -q \
  --label "io.kubernetes.pod.namespace=$NAMESPACE,io.kubernetes.pod.name=$PRIMARY_POD" \
  --name postgres | head -1)
echo "old primary container id: $OLD_CID"
docker exec -i "$PRIMARY_NODE" crictl exec -i "$OLD_CID" psql -U postgres app -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO splitbrain_probe(source,note)
SELECT 'old-primary', 'during partition ' || g FROM generate_series(1,10) g;
SELECT pg_is_in_recovery() AS old_pg_is_in_recovery,
       count(*) AS old_rows,
       count(*) FILTER (WHERE source='old-primary') AS old_partition_writes
FROM splitbrain_probe;
SQL

log "Writing to the CNPG-visible new primary"
kubectl exec -i -n "$NAMESPACE" "$NEW_PRIMARY" -c postgres -- psql -U postgres app -v ON_ERROR_STOP=1 <<'SQL'
INSERT INTO splitbrain_probe(source,note)
SELECT 'new-primary', 'after promotion ' || g FROM generate_series(1,5) g;
SELECT pg_is_in_recovery() AS new_pg_is_in_recovery,
       count(*) AS new_rows,
       count(*) FILTER (WHERE source='new-primary') AS new_primary_writes
FROM splitbrain_probe;
SQL

log "Reconnecting old-primary node"
run docker network connect kind "$PRIMARY_NODE"
PRIMARY_NODE=""

log "Waiting for CNPG to reconcile the old primary"
for _ in $(seq 1 180); do
  OLD_ROLE=$(kubectl get pod -n "$NAMESPACE" "$PRIMARY_POD" -o jsonpath='{.metadata.labels.cnpg\.io/instanceRole}' 2>/dev/null || true)
  READY=$(kubectl get pod -n "$NAMESPACE" "$PRIMARY_POD" -o jsonpath='{.status.containerStatuses[?(@.name=="postgres")].ready}' 2>/dev/null || true)
  if [[ "$OLD_ROLE" == "replica" && "$READY" == "true" ]]; then
    break
  fi
  sleep 2
done
kubectl get pods -n "$NAMESPACE" -o wide
kubectl get cluster -n "$NAMESPACE" "$PG_CLUSTER" \
  -o jsonpath='{.status.phase}{" currentPrimary="}{.status.currentPrimary}{" targetPrimary="}{.status.targetPrimary}{"\n"}' || true

log "Final data on current primary after reconnect"
FINAL_PRIMARY=$(get_primary)
kubectl exec -i -n "$NAMESPACE" "$FINAL_PRIMARY" -c postgres -- psql -U postgres app -v ON_ERROR_STOP=1 <<'SQL'
SELECT source, count(*) FROM splitbrain_probe GROUP BY source ORDER BY source;
SELECT count(*) AS total_rows,
       count(*) FILTER (WHERE source='old-primary') AS old_primary_partition_writes_remaining
FROM splitbrain_probe;
SQL

log "Operator logs showing the partitioned instance could not reach the Kubernetes API"
kubectl logs -n "$NAMESPACE" "$PRIMARY_POD" -c postgres --tail=60 | \
  grep -E 'network is unreachable|client connection lost|terminating walsender|statement: CREATE TABLE|Starting up|Failing over|demoted|promoted' || true

echo "CNPG/kind #7407-style split-brain reproduction completed"
