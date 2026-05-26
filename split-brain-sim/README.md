# Split-brain reproduction

This directory contains a Docker-based reproduction of a PostgreSQL split-brain window.

It sets up one primary and two streaming replicas, partitions the primary from the Docker network, promotes a replica, then writes to both primaries while they are unable to see each other.

The expected result is a demonstrated split-brain state: both PostgreSQL servers accept and commit writes independently.

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
