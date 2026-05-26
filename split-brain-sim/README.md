# Split-brain reproduction

This directory contains a Docker-based reproduction of a PostgreSQL split-brain window.

It sets up one primary and two streaming replicas, partitions the primary from the Docker network, promotes a replica, writes to both primaries while they are unable to see each other, then reconnects the old primary and runs `pg_rewind`.

The expected result is a demonstrated split-brain/data-loss state: both PostgreSQL servers accept and commit writes independently during the partition, and the writes acknowledged by the isolated old primary are absent after `pg_rewind`.

## Run

```bash
cd split-brain-sim
./setup-and-simulate.sh
```

Cleanup:

```bash
docker compose down -v
```

There is also a standalone variant:

```bash
./simulate.sh
./simulate.sh --cleanup
```

## What the output shows

- Initial replicated baseline rows
- Primary network isolation while PostgreSQL is still accepting local connections
- Replica promotion
- Concurrent writes to the old and new primaries
- Divergent row counts confirming split brain
- `pg_rewind` of the old primary onto the promoted primary timeline
- Old-primary partition writes absent after rewind
