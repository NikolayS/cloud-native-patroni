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

## What the output shows

- Initial replicated baseline rows
- Synchronous replication enabled with `ANY 1 (replica1, replica2)`
- Primary partitioned from the promoted-replica network while replica2 remains reachable as a synchronous quorum member
- Replica1 promotion
- Concurrent writes to the old and new primaries
- Divergent row counts confirming split brain
- `pg_rewind` of the old primary onto the promoted primary timeline
- Old-primary partition writes absent after rewind
