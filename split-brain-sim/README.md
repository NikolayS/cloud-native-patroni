# Split-brain/data-loss reproduction

This directory contains a Docker-based reproduction of a PostgreSQL split-brain/data-loss window using PostgreSQL synchronous streaming replication.

It sets up one primary and two streaming replicas, enables `synchronous_standby_names = 'ANY 1 (replica1, replica2)'`, partitions the old primary away from the replica that will be promoted while keeping another synchronous quorum member reachable, promotes replica1, writes to both primaries, then reconnects the old primary and runs `pg_rewind`.

The expected result is a demonstrated split-brain/data-loss state: both PostgreSQL servers accept and commit writes independently during the partition, and the writes acknowledged by the isolated old primary are absent after `pg_rewind`.

## Run

```bash
cd split-brain-sim
./setup-and-simulate.sh
```

Compatibility wrapper:

```bash
./simulate.sh
```

Cleanup:

```bash
docker compose down -v
# or:
./simulate.sh --cleanup
```

A synchronous-replication variant:

```bash
./simulate-sync.sh
```

A partial-connectivity, 5-node, 2-network variant that demonstrates how
sync-committed-and-ACK'd transactions can still be lost when the
operator's vantage point overlaps the primary's via shared replicas:

```bash
./simulate-partial-connectivity.sh
docker compose -f docker-compose-5node.yml down -v
```

## What the output shows

- Initial replicated baseline rows
- Synchronous replication enabled with `ANY 1 (replica1, replica2)`
- Primary partitioned from the promoted-replica network while replica2 remains reachable as a synchronous quorum member
- Replica1 promotion
- Concurrent writes to the old and new primaries
- Divergent row counts confirming split brain
- `pg_rewind` of the old primary onto the promoted primary timeline
- Old-primary partition writes absent after rewind

For the partial-connectivity variant specifically:

- Five nodes (`P, R1, R2, R3, R4`) on two Docker networks (`net-a`, `net-b`)
- `R3` and `R4` are dual-homed (the "overlap" replicas)
- Sync replication with `synchronous_standby_names = 'ANY 2 (...)'`
- A two-stage partition where the operator sees only stale replicas
  at the moment of promotion while the primary is still satisfying its
  quorum on the other side
- The most-advanced reachable replica from the operator's vantage point
  is the WRONG one to promote
- `pg_rewind` on `R3`, `R4`, and `P` onto the promoted replica's timeline
  discards transactions that were synchronously committed AND ACK'd

## CloudNativePG/kind reproduction for upstream #7407

This repository also includes a kind-based reproduction that uses the real CloudNativePG operator and Kubernetes resources. It creates a 3-instance CNPG cluster matching the original upstream issue shape: liveness pinger disabled, no synchronous replication, and a kind node partition using `docker network disconnect kind <node>`.

```bash
./split-brain-sim/cnpg-kind-7407-repro.sh
```

The expected result is that CNPG promotes a new primary while the old primary remains locally writable on the disconnected kind node. After the node is reconnected and CNPG reconciles the old primary, rows written only to the old primary during the partition are absent from the current primary.

Useful environment variables:

```bash
KEEP_CLUSTER=1 ./split-brain-sim/cnpg-kind-7407-repro.sh
CLUSTER_NAME=cnpg-7407-test ./split-brain-sim/cnpg-kind-7407-repro.sh
CNPG_MANIFEST=releases/cnpg-1.25.1.yaml ./split-brain-sim/cnpg-kind-7407-repro.sh
```

### PgQue/pg_cron workload variant

For a higher-write-rate database-internal workload, use the PgQue/pg_cron variant. It builds a CNPG-compatible PostgreSQL image with `pg_cron` and PgQue v0.2.0, schedules PgQue's 10 Hz ticker loop through pg_cron, and schedules producer/consumer jobs that continue writing inside PostgreSQL during the partition without external client writes.

```bash
./split-brain-sim/cnpg-kind-pgque-repro.sh
```

Useful knobs:

```bash
EVENTS_PER_SECOND=1000 PARTITION_SECONDS=30 ./split-brain-sim/cnpg-kind-pgque-repro.sh
KEEP_CLUSTER=1 ./split-brain-sim/cnpg-kind-pgque-repro.sh
```

_Authorship note: written with assistance from AI._
