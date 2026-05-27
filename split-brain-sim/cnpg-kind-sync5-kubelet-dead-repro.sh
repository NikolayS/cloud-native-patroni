#!/usr/bin/env bash
set -Eeuo pipefail

# 5-node CNPG ANY 2 adversarial kubelet-dead split-brain hunt:
# side A = old primary + one sync standby; side B = operator/API-visible side + 3 standbys.
# Cut side A from the Kubernetes API and from all pods except each other.
# This aims to make the operator promote side B while kubelet/liveness on
# the old-primary node is dead and cannot fence still-running Postgres.

CLUSTER_NAME=${CLUSTER_NAME:-cnpg-sync5-kubelet-dead}
NAMESPACE=${NAMESPACE:-$CLUSTER_NAME}
PG_CLUSTER=${PG_CLUSTER:-splitbrain}
CNPG_MANIFEST=${CNPG_MANIFEST:-releases/cnpg-1.29.1.yaml}
PG_MAJOR=${PG_MAJOR:-17}
OBSERVE_SECONDS=${OBSERVE_SECONDS:-150}
SLEEP=${SLEEP:-0.01}
POD_CIDR=${POD_CIDR:-10.244.0.0/16}
SERVICE_CIDR_HOST=${SERVICE_CIDR_HOST:-10.96.0.1}
EVIDENCE_DIR=${EVIDENCE_DIR:-$PWD/split-brain-sim/evidence/${CLUSTER_NAME}-$(date -u +%Y%m%dT%H%M%SZ)}

mkdir -p "$EVIDENCE_DIR"; : >"$EVIDENCE_DIR/run.log"; : >"$EVIDENCE_DIR/timeline.log"
PRIMARY_POD=""; PRIMARY_NODE=""; PRIMARY_IP=""; KEEP_STANDBY=""; KEEP_IP=""; PARTITIONED=0

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
  kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
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
      timeout 8 kubectl exec -i -n "$NAMESPACE" "$p" -c postgres -- psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now(), pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) host;
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) FROM unlogged_loop_probe GROUP BY 1,2 ORDER BY 1,2;
SQL
    fi
    echo "## direct old primary SQL: $PRIMARY_POD on $PRIMARY_NODE"
    oldcid=$(docker exec "$PRIMARY_NODE" crictl ps -q --label "io.kubernetes.pod.namespace=$NAMESPACE,io.kubernetes.pod.name=$PRIMARY_POD" --name postgres | head -1 || true)
    echo "old_cid=$oldcid"
    if [[ -n "$oldcid" ]]; then
      timeout 8 docker exec -i "$PRIMARY_NODE" crictl exec -i "$oldcid" psql -U postgres app -X -v ON_ERROR_STOP=0 <<'SQL' || true
SELECT now(), pg_is_in_recovery(), trim(both E'\n' from pg_read_file('/etc/hostname')) host;
SHOW synchronous_standby_names;
SELECT application_name,state,sync_state FROM pg_stat_replication ORDER BY 1;
SELECT origin, pg_is_in_recovery, min(created_at), max(created_at), count(*) FROM unlogged_loop_probe GROUP BY 1,2 ORDER BY 1,2;
SQL
    fi
  } >"$out" 2>&1
}

log "Create kind cluster with 6 nodes"
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
  instances: 5
  imageName: ghcr.io/cloudnative-pg/postgresql:$PG_MAJOR-standard-bookworm
  postgresql:
    synchronous:
      method: any
      number: 2
      dataDurability: required
      failoverQuorum: true
    parameters:
      synchronous_commit: "on"
  storage:
    size: 1Gi
YAML
run kubectl apply -n "$NAMESPACE" -f /tmp/$CLUSTER_NAME-cluster.yaml
run kubectl wait -n "$NAMESPACE" --for=condition=Ready cluster/$PG_CLUSTER --timeout=900s
kubectl get pods -n "$NAMESPACE" -o wide | tee "$EVIDENCE_DIR/pods-initial.txt"

PRIMARY_POD=$(get_primary)
PRIMARY_NODE=$(pod_node "$PRIMARY_POD")
PRIMARY_IP=$(pod_ip "$PRIMARY_POD")
echo "$(date +%Y-%m-%dT%H:%M:%S%z) old primary=$PRIMARY_POD node=$PRIMARY_NODE ip=$PRIMARY_IP" | tee -a "$EVIDENCE_DIR/timeline.log"

log "Create unlogged table and launch pod-local write loops"
psql_pod "$PRIMARY_POD" <<SQL
CREATE UNLOGGED TABLE unlogged_loop_probe(
  origin text not null,
  created_at timestamptz not null default clock_timestamp(),
  pg_is_in_recovery boolean not null default pg_is_in_recovery()
);
SQL
for pod in $(kubectl get pods -n "$NAMESPACE" -l cnpg.io/cluster=$PG_CLUSTER -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'); do
  kubectl exec -n "$NAMESPACE" "$pod" -c postgres -- bash -lc "nohup bash -c 'while true; do psql -U postgres app -X -v ON_ERROR_STOP=0 -c \"INSERT INTO unlogged_loop_probe(origin) VALUES ('\''$pod'\'')\" >/controller/tmp/unlogged-loop.last 2>&1; sleep $SLEEP; done' >/controller/tmp/unlogged-loop.log 2>&1 & echo \$! >/controller/tmp/unlogged-loop.pid"
done
sleep 8
sample baseline

log "Pick one sync standby to stay with old primary"
SYNC_STANDBYS=()
while IFS= read -r standby; do [[ -n "$standby" ]] && SYNC_STANDBYS+=("$standby"); done < <(psql_pod "$PRIMARY_POD" -At <<'SQL'
SELECT application_name FROM pg_stat_replication WHERE sync_state='quorum' ORDER BY application_name;
SQL
)
printf '%s\n' "${SYNC_STANDBYS[@]}" >"$EVIDENCE_DIR/sync-standbys.txt"
if [[ ${#SYNC_STANDBYS[@]} -lt 3 ]]; then echo "Expected >=3 quorum standbys, got ${#SYNC_STANDBYS[@]}" >&2; exit 2; fi
KEEP_STANDBY=${SYNC_STANDBYS[0]}
KEEP_IP=$(pod_ip "$KEEP_STANDBY")
echo "$(date +%Y-%m-%dT%H:%M:%S%z) keep-with-old=$KEEP_STANDBY ip=$KEEP_IP sync_set=${SYNC_STANDBYS[*]}" | tee -a "$EVIDENCE_DIR/timeline.log"

log "Apply API-cut side-A partition"
: >"$EVIDENCE_DIR/rules.txt"
PRIMARY_NODE_IP=$(node_ip "$PRIMARY_NODE")
KEEP_NODE=$(pod_node "$KEEP_STANDBY")
KEEP_NODE_IP=$(node_ip "$KEEP_NODE")
CONTROL_PLANE=$(kind get nodes --name "$CLUSTER_NAME" | grep control-plane)
CONTROL_PLANE_IP=$(node_ip "$CONTROL_PLANE")
echo "$(date +%Y-%m-%dT%H:%M:%S%z) partition-start sideA=$PRIMARY_POD,$KEEP_STANDBY primary_node_ip=$PRIMARY_NODE_IP keep_node_ip=$KEEP_NODE_IP control_plane_ip=$CONTROL_PLANE_IP" | tee -a "$EVIDENCE_DIR/timeline.log"

# On all kind nodes: isolate side A from all pod traffic, then allow primary<->kept standby.
for n in $(all_nodes); do
  # Broad drops first, then ACCEPTs. Since iptables -I inserts at position 1,
  # the ACCEPT rules end up above the broad drops.
  add_rule "$n" FORWARD -s "$PRIMARY_IP" -d "$POD_CIDR" -j DROP
  add_rule "$n" FORWARD -s "$POD_CIDR" -d "$PRIMARY_IP" -j DROP
  add_rule "$n" FORWARD -s "$KEEP_IP" -d "$POD_CIDR" -j DROP
  add_rule "$n" FORWARD -s "$POD_CIDR" -d "$KEEP_IP" -j DROP
  add_rule "$n" FORWARD -s "$PRIMARY_IP" -d "$KEEP_IP" -j ACCEPT
  add_rule "$n" FORWARD -s "$KEEP_IP" -d "$PRIMARY_IP" -j ACCEPT
done
# On the side-A nodes: block kubelet/node access to Kubernetes API so the
# control plane marks those nodes stale/NotReady and the operator can promote.
for n in "$PRIMARY_NODE" "$KEEP_NODE"; do
  add_rule "$n" OUTPUT -d "$SERVICE_CIDR_HOST" -j DROP
  add_rule "$n" OUTPUT -d "$CONTROL_PLANE_IP" -p tcp --dport 6443 -j DROP
done

# Also block the side-A *pods* from reaching the API server. Node OUTPUT is not
# enough for pod traffic in kind/CNI; pod egress traverses FORWARD and kube-proxy
# may DNAT the service IP to the control-plane endpoint. Block both service-IP
# and post-DNAT API-server traffic from the primary and its kept standby. This
# is the adversarial bit: old primary cannot receive targetPrimary demotion via
# the API, but can still reach one peer for the isolation liveness pinger.
for n in $(all_nodes); do
  add_rule "$n" FORWARD -s "$PRIMARY_IP" -d "$SERVICE_CIDR_HOST" -j DROP
  add_rule "$n" FORWARD -s "$KEEP_IP" -d "$SERVICE_CIDR_HOST" -j DROP
  add_rule "$n" FORWARD -s "$PRIMARY_IP" -d "$CONTROL_PLANE_IP" -p tcp --dport 6443 -j DROP
  add_rule "$n" FORWARD -s "$KEEP_IP" -d "$CONTROL_PLANE_IP" -p tcp --dport 6443 -j DROP
  add_rule "$n" FORWARD -s "$PRIMARY_IP" -p tcp --dport 6443 -j DROP
  add_rule "$n" FORWARD -s "$KEEP_IP" -p tcp --dport 6443 -j DROP
done
PARTITIONED=1

log "Stop kubelet service on old-primary node to simulate probe/fencing failure"
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
  echo "# CNPG 5-node ANY 2 side-A API-cut partition"
  echo
  echo "## Timeline"
  cat "$EVIDENCE_DIR/timeline.log"
  echo
  echo "## Row snippets"
  grep -hE "^[[:space:]]*splitbrain-[0-9]+[[:space:]]*\|[[:space:]]*[ft][[:space:]]*\|" "$EVIDENCE_DIR"/*-sample-*.txt 2>/dev/null | tail -120 || true
  echo
  echo "Evidence directory: \`$EVIDENCE_DIR\`"
} >"$EVIDENCE_DIR/summary.md"
cat "$EVIDENCE_DIR/summary.md"
