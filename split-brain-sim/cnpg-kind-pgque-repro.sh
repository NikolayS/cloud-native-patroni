#!/usr/bin/env bash
set -Eeuo pipefail

# Reproduce the #7407 split-brain/data-loss window with autonomous database-side
# PgQue/pg_cron writes. The test builds a CNPG-compatible PostgreSQL image with
# pg_cron and PgQue v0.2.0, runs a 3-instance CloudNativePG cluster, starts
# PgQue's pg_cron ticker loop, and schedules pg_cron jobs that produce/consume
# PgQue events once per second. During the kind node partition, both promoted
# timelines keep writing without external client connections.

CLUSTER_NAME=${CLUSTER_NAME:-cnpg-pgque-7407}
NAMESPACE=${NAMESPACE:-cnpg-pgque-7407}
PG_CLUSTER=${PG_CLUSTER:-splitbrain}
CNPG_MANIFEST=${CNPG_MANIFEST:-releases/cnpg-1.25.1.yaml}
PGQUE_VERSION=${PGQUE_VERSION:-v0.2.0}
PG_MAJOR=${PG_MAJOR:-17}
PGQUE_IMAGE=${PGQUE_IMAGE:-localhost/cnpg-pgque:${PG_MAJOR}-pgque-${PGQUE_VERSION#v}}
EVENTS_PER_SECOND=${EVENTS_PER_SECOND:-1000}
PARTITION_SECONDS=${PARTITION_SECONDS:-30}
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

log "Building CNPG-compatible PostgreSQL image with pg_cron and PgQue $PGQUE_VERSION"
BUILD_DIR=$(mktemp -d /tmp/cnpg-pgque-image.XXXXXX)
curl -fsSL "https://raw.githubusercontent.com/NikolayS/PgQue/${PGQUE_VERSION}/sql/pgque.sql" \
  -o "$BUILD_DIR/pgque.sql"
cat >"$BUILD_DIR/Dockerfile" <<DOCKERFILE
FROM ghcr.io/cloudnative-pg/postgresql:${PG_MAJOR}-standard-bookworm
USER root
RUN apt-get update \
    && apt-get install -y --no-install-recommends postgresql-${PG_MAJOR}-cron \
    && rm -rf /var/lib/apt/lists/*
COPY --chmod=0644 pgque.sql /usr/share/postgresql/pgque.sql
USER 26
DOCKERFILE
run docker build -t "$PGQUE_IMAGE" "$BUILD_DIR"

log "Creating kind cluster"
cat >/tmp/cnpg-pgque-kind.yaml <<YAML
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
- role: worker
- role: worker
YAML
kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
run kind create cluster --name "$CLUSTER_NAME" --config /tmp/cnpg-pgque-kind.yaml
kubectl config use-context "kind-$CLUSTER_NAME" >/dev/null
run kind load docker-image "$PGQUE_IMAGE" --name "$CLUSTER_NAME"

log "Installing CNPG operator from $CNPG_MANIFEST"
run kubectl apply --server-side -f "$CNPG_MANIFEST"
run kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=180s

log "Creating CNPG Cluster with pg_cron preloaded and liveness pinger disabled"
run kubectl create namespace "$NAMESPACE"
cat >/tmp/cnpg-pgque-cluster.yaml <<YAML
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: $PG_CLUSTER
  annotations:
    alpha.cnpg.io/livenessPinger: '{"enabled": false}'
spec:
  instances: 3
  imageName: $PGQUE_IMAGE
  postgresql:
    shared_preload_libraries:
      - pg_cron
    parameters:
      cron.database_name: app
      cron.use_background_workers: "on"
      log_statement: ddl
      log_replication_commands: "on"
  storage:
    size: 1Gi
YAML
run kubectl apply -n "$NAMESPACE" -f /tmp/cnpg-pgque-cluster.yaml
run kubectl wait -n "$NAMESPACE" --for=condition=Ready "cluster/$PG_CLUSTER" --timeout=600s
kubectl get pods -n "$NAMESPACE" -o wide

get_primary() {
  kubectl get pods -n "$NAMESPACE" \
    -l "cnpg.io/cluster=$PG_CLUSTER,cnpg.io/instanceRole=primary" \
    -o jsonpath='{.items[0].metadata.name}'
}

PRIMARY_POD=$(get_primary)
PRIMARY_NODE=$(kubectl get pod -n "$NAMESPACE" "$PRIMARY_POD" -o jsonpath='{.spec.nodeName}')

log "Installing PgQue and autonomous pg_cron producer/consumer jobs"
echo "old primary: $PRIMARY_POD on $PRIMARY_NODE"
kubectl exec -i -n "$NAMESPACE" "$PRIMARY_POD" -c postgres -- psql -U postgres app -v ON_ERROR_STOP=1 <<SQL
CREATE EXTENSION IF NOT EXISTS pg_cron;
\\i /usr/share/postgresql/pgque.sql
SELECT pgque.create_queue('splitbrain_events');
SELECT pgque.subscribe('splitbrain_events', 'splitbrain_consumer');

CREATE TABLE IF NOT EXISTS splitbrain_phase(phase text primary key);
INSERT INTO splitbrain_phase VALUES ('baseline') ON CONFLICT DO NOTHING;

CREATE SEQUENCE IF NOT EXISTS splitbrain_payload_seq;
CREATE TABLE IF NOT EXISTS splitbrain_workload_stats(
  id bigserial primary key,
  origin text not null,
  phase text not null,
  action text not null,
  rows_count integer not null,
  created_at timestamptz not null default clock_timestamp(),
  pg_is_in_recovery boolean not null default pg_is_in_recovery()
);

CREATE OR REPLACE FUNCTION splitbrain_current_origin()
RETURNS text LANGUAGE sql AS \$\$
  SELECT trim(both E'\n' from pg_read_file('/etc/hostname'))
\$\$;

CREATE OR REPLACE FUNCTION splitbrain_current_phase()
RETURNS text LANGUAGE sql AS \$\$
  SELECT phase FROM splitbrain_phase LIMIT 1
\$\$;

CREATE OR REPLACE FUNCTION splitbrain_pgque_produce(n integer)
RETURNS integer LANGUAGE plpgsql AS \$\$
DECLARE
  v_origin text := splitbrain_current_origin();
  v_phase text := splitbrain_current_phase();
BEGIN
  PERFORM pgque.send_batch(
    'splitbrain_events',
    ARRAY(
      SELECT jsonb_build_object(
        'origin', v_origin,
        'phase', v_phase,
        'seq', nextval('splitbrain_payload_seq'),
        'created_at', clock_timestamp(),
        'is_recovery', pg_is_in_recovery()
      )
      FROM generate_series(1, n)
    )
  );
  INSERT INTO splitbrain_workload_stats(origin, phase, action, rows_count)
  VALUES (v_origin, v_phase, 'produce', n);
  RETURN n;
END
\$\$;

CREATE OR REPLACE FUNCTION splitbrain_pgque_consume(max_events integer)
RETURNS integer LANGUAGE plpgsql AS \$\$
DECLARE
  msg pgque.message;
  v_batch_id bigint := NULL;
  v_count integer := 0;
  v_origin text := splitbrain_current_origin();
  v_phase text := splitbrain_current_phase();
BEGIN
  FOR msg IN SELECT * FROM pgque.receive('splitbrain_events', 'splitbrain_consumer', max_events)
  LOOP
    v_batch_id := msg.batch_id;
    v_count := v_count + 1;
  END LOOP;

  IF v_batch_id IS NOT NULL THEN
    PERFORM pgque.ack(v_batch_id);
  END IF;

  IF v_count > 0 THEN
    INSERT INTO splitbrain_workload_stats(origin, phase, action, rows_count)
    VALUES (v_origin, v_phase, 'consume', v_count);
  END IF;
  RETURN v_count;
END
\$\$;

CREATE OR REPLACE FUNCTION splitbrain_pgque_event_counts()
RETURNS TABLE(origin text, phase text, events bigint) LANGUAGE plpgsql AS \$\$
DECLARE
  qid integer;
BEGIN
  SELECT queue_id INTO qid FROM pgque.queue WHERE queue_name = 'splitbrain_events';
  RETURN QUERY EXECUTE format(
    'SELECT ev_data::jsonb->>''origin'', ev_data::jsonb->>''phase'', count(*)::bigint
       FROM pgque.event_%s
      GROUP BY 1, 2
      ORDER BY 1, 2', qid);
END
\$\$;

SELECT pgque.start();
SELECT cron.schedule('splitbrain-pgque-producer', '1 second', 'SELECT splitbrain_pgque_produce(${EVENTS_PER_SECOND})');
SELECT cron.schedule('splitbrain-pgque-consumer', '1 second', 'SELECT splitbrain_pgque_consume(${EVENTS_PER_SECOND})');
SQL

log "Letting baseline PgQue/pg_cron workload run"
sleep 70
kubectl exec -i -n "$NAMESPACE" "$PRIMARY_POD" -c postgres -- psql -U postgres app -v ON_ERROR_STOP=1 <<'SQL'
SELECT * FROM pgque.status();
SELECT origin, phase, action, sum(rows_count) AS rows FROM splitbrain_workload_stats GROUP BY 1,2,3 ORDER BY 1,2,3;
SELECT * FROM splitbrain_pgque_event_counts();
UPDATE splitbrain_phase SET phase = 'partition';
SQL
sleep 5

log "Disconnecting the kind node that hosts the old primary"
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

log "Letting PgQue/pg_cron run on both timelines for ${PARTITION_SECONDS}s"
sleep "$PARTITION_SECONDS"

log "Counts inside isolated old primary before reconnect"
OLD_CID=$(docker exec "$PRIMARY_NODE" crictl ps -q \
  --label "io.kubernetes.pod.namespace=$NAMESPACE,io.kubernetes.pod.name=$PRIMARY_POD" \
  --name postgres | head -1)
echo "old primary container id: $OLD_CID"
docker exec -i "$PRIMARY_NODE" crictl exec -i "$OLD_CID" psql -U postgres app -v ON_ERROR_STOP=1 <<'SQL'
SELECT pg_is_in_recovery() AS old_pg_is_in_recovery, splitbrain_current_origin() AS old_origin;
SELECT origin, phase, action, sum(rows_count) AS rows
FROM splitbrain_workload_stats
GROUP BY 1,2,3 ORDER BY 1,2,3;
SELECT * FROM splitbrain_pgque_event_counts();
SQL

log "Counts on CNPG-visible new primary before reconnect"
kubectl exec -i -n "$NAMESPACE" "$NEW_PRIMARY" -c postgres -- psql -U postgres app -v ON_ERROR_STOP=1 <<'SQL'
SELECT pg_is_in_recovery() AS new_pg_is_in_recovery, splitbrain_current_origin() AS new_origin;
SELECT origin, phase, action, sum(rows_count) AS rows
FROM splitbrain_workload_stats
GROUP BY 1,2,3 ORDER BY 1,2,3;
SELECT * FROM splitbrain_pgque_event_counts();
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

log "Final PgQue/pg_cron data on current primary after reconnect"
FINAL_PRIMARY=$(get_primary)
kubectl exec -i -n "$NAMESPACE" "$FINAL_PRIMARY" -c postgres -- psql -U postgres app -v ON_ERROR_STOP=1 <<SQL
SELECT splitbrain_current_origin() AS final_primary_origin;
SELECT origin, phase, action, sum(rows_count) AS rows
FROM splitbrain_workload_stats
GROUP BY 1,2,3 ORDER BY 1,2,3;
SELECT * FROM splitbrain_pgque_event_counts();
WITH old_partition AS (
  SELECT coalesce(sum(rows_count), 0) AS rows
  FROM splitbrain_workload_stats
  WHERE origin = '${PRIMARY_POD}' AND phase = 'partition'
)
SELECT rows AS old_primary_partition_stat_rows_remaining FROM old_partition;
SQL

log "Partitioned old-primary logs showing API loss / replication timeout"
kubectl logs -n "$NAMESPACE" "$PRIMARY_POD" -c postgres --tail=80 | \
  grep -E 'network is unreachable|client connection lost|terminating walsender|pg_cron|cron|Failing over|demoted|promoted' || true

echo "CNPG/kind PgQue/pg_cron split-brain reproduction completed"
