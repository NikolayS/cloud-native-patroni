#!/usr/bin/env bash
set -Eeuo pipefail

# 6-instance CNPG ANY 2 adversarial logged-data split-brain hunt.
# Set FAILOVER_QUORUM=false to demonstrate the unsafe variant when quorum failover protection is disabled.
#
# Goal: prove the scariest variant, where ordinary WAL-logged table writes can
# commit on both sides after CNPG promotes a new primary.  Topology:
#   side A = old primary + two synchronous standbys
#   side B = operator/API-visible side + three standbys
#
# We keep side-A PostgreSQL replication paths alive so the old primary can still
# satisfy ANY 2 synchronous_commit=on.  We stop kubelet on the old-primary node
# and force-delete the old primary Pod object, so Kubernetes/CNPG can promote a
# side-B standby while the old PostgreSQL process is not fenced.

CLUSTER_NAME=${CLUSTER_NAME:-cnpg-sync6-logged-kubelet-fencing}
NAMESPACE=${NAMESPACE:-$CLUSTER_NAME}
PG_CLUSTER=${PG_CLUSTER:-splitbrain}
CNPG_MANIFEST=${CNPG_MANIFEST:-releases/cnpg-1.29.1.yaml}
PG_MAJOR=${PG_MAJOR:-17}
FAILOVER_QUORUM=${FAILOVER_QUORUM:-true}
OBSERVE_SECONDS=${OBSERVE_SECONDS:-180}
SLEEP=${SLEEP:-0.01}
POD_CIDR=${POD_CIDR:-10.244.0.0/16}
SERVICE_CIDR_HOST=${SERVICE_CIDR_HOST:-10.96.0.1}
EVIDENCE_DIR=${EVIDENCE_DIR:-$PWD/split-brain-sim/evidence/${CLUSTER_NAME}-$(date -u +%Y%m%dT%H%M%SZ)}

mkdir -p "$EVIDENCE_DIR"; : >"$EVIDENCE_DIR/run.log"; : >"$EVIDENCE_DIR/timeline.log"; : >"$EVIDENCE_DIR/rules.txt"
PRIMARY_POD=""; PRIMARY_NODE=""; PRIMARY_IP=""; PARTITIONED=0
KEEP_STANDBYS=(); KEEP_IPS=(); KEEP_NODES=()

log(){ printf '\n\033[1;36m==> %s\033[0m\n' "$*" | tee -a "$EVIDENCE_DIR/run.log"; }
run(){ printf '+ %s\n' "$*" | tee -a "$EVIDENCE_DIR/run.log"; "$@"; }
ts(){ python3 -c 'import time; print(int(time.time()*1000))'; }
all_nodes(){ kind get nodes --name "$CLUSTER_NAME"; }
get_primary(){ kubectl get pods -n "$NAMESPACE" -l "cnpg.io/cluster=$PG_CLUSTER,cnpg.io/instanceRole=primary" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true; }
pod_ip(){ kubectl get pod -n "$NAMESPACE" "$1" -o jsonpath='{.status.podIP}'; }
pod_node(){ kubectl get pod -n "$NAMESPACE" "$1" -o jsonpath='{.spec.nodeName}'; }
psql_pod(){ local pod=$1; shift; kubectl exec -i -n "$NAMESPACE" "$pod" -c postgres -- psql -U postgres app -X -v ON_ERROR_STOP=1 "$@"; }
node_ip(){ docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"; }

cleanup(){
  set +e
  if [[ "$PARTITIONED" == 1 && -s "$EVIDENCE_DIR/rules.txt" ]]; then
    tac "$EVIDENCE_DIR/rules.txt" 2>/dev/null | while IFS='|' read -r node chain rule; do
      docker exec "$node" iptables -D "$chain" $rule >/dev/null 2>&1 || true
    done
  fi
  if [[ "${KEEP_CLUSTER:-0}" != "1" ]]; then
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

add_rule(){
  local node=$1 chain=$2; shift 2
  run docker exec "$node" iptables -I "$chain" 1 "$@"
  printf '%s|%s|%s\n' "$node" "$chain" "$*" >>"$EVIDENCE_DIR/rules.txt"
}

sample(){
  local label=$1 p oldcid out
  out="$EVIDENCE_DIR/$(ts)-sample-$label.txt"
  {
    echo "### $(date +%Y-%m-%dT%H:%M:%S%z) SAMPLE $label"
    echo "## cluster"
    kubectl get cluster -n "$NAMESPACE" "$PG_CLUSTER" -o jsonpath='{.status.phase}{" current="}{.status.currentPrimary}{" target="}{.status.targetPrimary}{" ready="}{.status.readyInstances}{" instances="}{.status.instances}{"\n"}' || true
    echo "## pods"
    kubectl get pods -n "$NAMESPACE" -o wide || true
    p=$(get_primary)
    if [[ -n "$p" ]]; then
      echo "## API-visible primary SQL: $p"
      timeout 10 kubectl exec -i -n "$NAMESPACE" "$p" -c postgres -- psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now(), pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) host;
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state,sent_lsn,write_lsn,flush_lsn,replay_lsn FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) FROM logged_loop_probe GROUP BY 1,2 ORDER BY 1,2;
SQL
    fi
    echo "## direct old primary SQL: $PRIMARY_POD on $PRIMARY_NODE"
    oldcid=$(docker exec "$PRIMARY_NODE" crictl ps -q --label "io.kubernetes.pod.namespace=$NAMESPACE,io.kubernetes.pod.name=$PRIMARY_POD" --name postgres | head -1 || true)
    echo "old_cid=$oldcid"
    if [[ -n "$oldcid" ]]; then
      timeout 10 docker exec -i "$PRIMARY_NODE" crictl exec -i "$oldcid" psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now(), pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) host;
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state,sent_lsn,write_lsn,flush_lsn,replay_lsn FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) FROM logged_loop_probe GROUP BY 1,2 ORDER BY 1,2;
SQL
    fi
  } >"$out" 2>&1
}

log "Create kind cluster with 7 nodes"
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
- role: worker
YAML
kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
run kind create cluster --name "$CLUSTER_NAME" --config /tmp/$CLUSTER_NAME-kind.yaml
kubectl config use-context kind-$CLUSTER_NAME >/dev/null

log "Install CNPG"
run kubectl apply --server-side -f "$CNPG_MANIFEST"
run kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=180s
run kubectl create namespace "$NAMESPACE"
cat >/tmp/$CLUSTER_NAME-cluster.yaml <<YAML
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: $PG_CLUSTER
spec:
  instances: 6
  imageName: ghcr.io/cloudnative-pg/postgresql:$PG_MAJOR-standard-bookworm
  postgresql:
    synchronous:
      method: any
      number: 2
      dataDurability: required
      failoverQuorum: $FAILOVER_QUORUM
    parameters:
      synchronous_commit: "on"
  storage:
    size: 1Gi
YAML
run kubectl apply -n "$NAMESPACE" -f /tmp/$CLUSTER_NAME-cluster.yaml
run kubectl wait -n "$NAMESPACE" --for=condition=Ready cluster/$PG_CLUSTER --timeout=1200s
kubectl get pods -n "$NAMESPACE" -o wide | tee "$EVIDENCE_DIR/pods-initial.txt"

PRIMARY_POD=$(get_primary)
PRIMARY_NODE=$(pod_node "$PRIMARY_POD")
PRIMARY_IP=$(pod_ip "$PRIMARY_POD")
echo "$(date +%Y-%m-%dT%H:%M:%S%z) old primary=$PRIMARY_POD node=$PRIMARY_NODE ip=$PRIMARY_IP" | tee -a "$EVIDENCE_DIR/timeline.log"

log "Create logged table and launch pod-local synchronous write loops"
psql_pod "$PRIMARY_POD" <<SQL
CREATE TABLE logged_loop_probe(
  id bigserial primary key,
  origin text not null,
  created_at timestamptz not null default clock_timestamp(),
  pg_is_in_recovery boolean not null default pg_is_in_recovery()
);
SQL
for pod in $(kubectl get pods -n "$NAMESPACE" -l cnpg.io/cluster=$PG_CLUSTER -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
  kubectl exec -n "$NAMESPACE" "$pod" -c postgres -- bash -lc "nohup bash -c 'while true; do psql -U postgres app -X -v ON_ERROR_STOP=0 -c \"SET synchronous_commit=on; INSERT INTO logged_loop_probe(origin) VALUES ('\''$pod'\'')\" >/controller/tmp/logged-loop.last 2>&1; sleep $SLEEP; done' >/controller/tmp/logged-loop.log 2>&1 & echo \$! >/controller/tmp/logged-loop.pid"
done
sleep 8
sample baseline

log "Pick two sync standbys to stay with old primary"
SYNC_STANDBYS=()
while IFS= read -r standby; do [[ -n "$standby" ]] && SYNC_STANDBYS+=("$standby"); done < <(psql_pod "$PRIMARY_POD" -At <<'SQL'
SELECT application_name FROM pg_stat_replication WHERE sync_state='quorum' ORDER BY application_name;
SQL
)
printf '%s\n' "${SYNC_STANDBYS[@]}" >"$EVIDENCE_DIR/sync-standbys.txt"
if [[ ${#SYNC_STANDBYS[@]} -lt 5 ]]; then echo "Expected >=5 quorum standbys, got ${#SYNC_STANDBYS[@]}" >&2; exit 2; fi
KEEP_STANDBYS=("${SYNC_STANDBYS[0]}" "${SYNC_STANDBYS[1]}")
for pod in "${KEEP_STANDBYS[@]}"; do
  KEEP_IPS+=("$(pod_ip "$pod")")
  KEEP_NODES+=("$(pod_node "$pod")")
done
echo "$(date +%Y-%m-%dT%H:%M:%S%z) keep-with-old=${KEEP_STANDBYS[*]} keep_ips=${KEEP_IPS[*]} sync_set=${SYNC_STANDBYS[*]}" | tee -a "$EVIDENCE_DIR/timeline.log"

log "Apply surgical side-A partition preserving old primary <-> two kept standbys"
PRIMARY_NODE_IP=$(node_ip "$PRIMARY_NODE")
CONTROL_PLANE=$(kind get nodes --name "$CLUSTER_NAME" | grep control-plane)
CONTROL_PLANE_IP=$(node_ip "$CONTROL_PLANE")
echo "$(date +%Y-%m-%dT%H:%M:%S%z) partition-start sideA=$PRIMARY_POD,${KEEP_STANDBYS[*]} primary_node_ip=$PRIMARY_NODE_IP control_plane_ip=$CONTROL_PLANE_IP" | tee -a "$EVIDENCE_DIR/timeline.log"

# Isolate side-A pod IPs from all pod traffic, then allow old primary <-> each
# kept standby. Broad drops first; iptables -I means later ACCEPTs land above.
SIDEA_IPS=("$PRIMARY_IP" "${KEEP_IPS[@]}")
for n in $(all_nodes); do
  for ip in "${SIDEA_IPS[@]}"; do
    add_rule "$n" FORWARD -s "$ip" -d "$POD_CIDR" -j DROP
    add_rule "$n" FORWARD -s "$POD_CIDR" -d "$ip" -j DROP
  done
  for keep_ip in "${KEEP_IPS[@]}"; do
    add_rule "$n" FORWARD -s "$PRIMARY_IP" -d "$keep_ip" -j ACCEPT
    add_rule "$n" FORWARD -s "$keep_ip" -d "$PRIMARY_IP" -j ACCEPT
  done
done

# Block side-A nodes and side-A pods from the Kubernetes API. This prevents the
# stale old primary from observing the targetPrimary demotion while allowing its
# database-plane replication to the kept standbys.
SIDEA_NODES=("$PRIMARY_NODE" "${KEEP_NODES[@]}")
for n in "${SIDEA_NODES[@]}"; do
  add_rule "$n" OUTPUT -d "$SERVICE_CIDR_HOST" -j DROP
  add_rule "$n" OUTPUT -d "$CONTROL_PLANE_IP" -p tcp --dport 6443 -j DROP
done
for n in $(all_nodes); do
  for ip in "${SIDEA_IPS[@]}"; do
    add_rule "$n" FORWARD -s "$ip" -d "$SERVICE_CIDR_HOST" -j DROP
    add_rule "$n" FORWARD -s "$ip" -d "$CONTROL_PLANE_IP" -p tcp --dport 6443 -j DROP
    add_rule "$n" FORWARD -s "$ip" -p tcp --dport 6443 -j DROP
  done
done
PARTITIONED=1

log "Stop kubelet service on old-primary node to remove probe/fencing enforcement"
echo "$(date +%Y-%m-%dT%H:%M:%S%z) stop-kubelet-service node=$PRIMARY_NODE" | tee -a "$EVIDENCE_DIR/timeline.log"
run docker exec "$PRIMARY_NODE" bash -lc 'systemctl stop kubelet || true; systemctl disable kubelet || true; pkill -9 kubelet || true; systemctl is-active kubelet || true; ps -ef | grep kubelet | grep -v grep || true'

log "Force-delete old-primary Pod object while kubelet is stopped; container should keep running"
echo "$(date +%Y-%m-%dT%H:%M:%S%z) force-delete-old-primary-pod" | tee -a "$EVIDENCE_DIR/timeline.log"
run kubectl delete pod -n "$NAMESPACE" "$PRIMARY_POD" --grace-period=0 --force

NEW_PRIMARY=""
for i in $(seq 1 "$OBSERVE_SECONDS"); do
  sample "t$i"
  p=$(get_primary)
  if [[ -n "$p" && "$p" != "$PRIMARY_POD" && -z "$NEW_PRIMARY" ]]; then
    NEW_PRIMARY=$p
    echo "$(date +%Y-%m-%dT%H:%M:%S%z) new-primary-observed=$NEW_PRIMARY at_second=$i" | tee -a "$EVIDENCE_DIR/timeline.log"
  fi
  sleep 1
done

log "Heal partition"
tac "$EVIDENCE_DIR/rules.txt" 2>/dev/null | while IFS='|' read -r node chain rule; do docker exec "$node" iptables -D "$chain" $rule >/dev/null 2>&1 || true; done
PARTITIONED=0
echo "$(date +%Y-%m-%dT%H:%M:%S%z) partition-end" | tee -a "$EVIDENCE_DIR/timeline.log"
sleep 20
sample final

{
  echo "# CNPG 6-instance ANY 2 logged-data kubelet/fencing split-brain"
  echo
  echo "## Timeline"
  cat "$EVIDENCE_DIR/timeline.log"
  echo
  echo "## Row snippets"
  grep -hE "^[[:space:]]*splitbrain-[0-9]+[[:space:]]*\|[[:space:]]*[ft][[:space:]]*\|" "$EVIDENCE_DIR"/*-sample-*.txt 2>/dev/null | tail -160 || true
  echo
  echo "Evidence directory: \`$EVIDENCE_DIR\`"
} >"$EVIDENCE_DIR/summary.md"
cat "$EVIDENCE_DIR/summary.md"
