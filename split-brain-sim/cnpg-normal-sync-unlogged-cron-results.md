# CNPG normal sync-rep unlogged pg_cron window hunt

This captures a search for an automatic two-writable-primary window using normal modern CloudNativePG settings:

- `dataDurability: required`
- `failoverQuorum: true`
- primary isolation liveness check left enabled/default
- database-internal workload only: `pg_cron` calls a stored procedure once per second; the procedure inserts into an `UNLOGGED` table and commits after each inserted row, for 100 commits/rows per tick
- old primary kind node is isolated with `docker network disconnect kind <node>`

The reproduction script is:

```text
split-brain-sim/cnpg-kind-sync-unlogged-cron-window-repro.sh
```

## Result

No two-writable-primary evidence was found in these runs.

The 5-node `ANY 2` case promoted a new primary and the new primary began local `pg_cron` writes to its own unlogged table. The old primary's container could still be observed on the isolated kind node, but SQL connections to its PostgreSQL server failed with `FATAL: the database system is shutting down`; only pre-partition old-primary rows were observed.

The 3-node `ANY 1` case did not promote a new primary during the observation window in the captured 100-commit/tick run. The Kubernetes/API side continued reporting the old primary as current, while API-side SQL to the disconnected node failed and direct old-node SQL reported PostgreSQL shutting down. Again, no continued old-primary writes were observed.

Container/pod liveness alone is therefore not evidence of split brain here. The evidence needed would be overlapping successful row growth from both `origin = old_primary` and `origin = new_primary` after a new primary is visible; these runs did not show that.

## 3 nodes: `ANY 1`

Command:

```bash
INSTANCES=3 SYNC_NUMBER=1 COMMITS_PER_TICK=100 OBSERVE_SECONDS=60 \
  ./split-brain-sim/cnpg-kind-sync-unlogged-cron-window-repro.sh
```

Evidence directory:

```text
split-brain-sim/evidence/cnpg-sync-unlogged-cron-3-1-20260527T131352Z/
```

Timeline:

```text
2026-05-27T06:18:09-0700 old primary=splitbrain-1 node=cnpg-sync-unlogged-cron-3-1-worker
2026-05-27T06:18:15-0700 partition-start node=cnpg-sync-unlogged-cron-3-1-worker old_primary=splitbrain-1
2026-05-27T06:22:36-0700 partition-end node=cnpg-sync-unlogged-cron-3-1-worker
```

Observed behavior:

- No `new-primary-observed` timeline marker was emitted during the 60 samples.
- API snapshots kept reporting `Cluster in healthy state current=splitbrain-1 target=splitbrain-1 ready=3 instances=3`, although API-side SQL to that pod failed because the node was disconnected.
- Direct old-node SQL samples failed 60 times with:

```text
psql: error: connection to server on socket "/controller/run/.s.PGSQL.5432" failed: FATAL:  the database system is shutting down
```

Rows observed:

```text
splitbrain-1 | f | 2026-05-27 13:18:09.556614+00 | 2026-05-27 13:18:15.115173+00 | 600
```

Interpretation: no automatic two-primary write window was found. The old primary had only pre-partition rows, and no new primary became visible in this run.

## 5 nodes: `ANY 2`

Command:

```bash
INSTANCES=5 SYNC_NUMBER=2 COMMITS_PER_TICK=100 OBSERVE_SECONDS=60 \
  ./split-brain-sim/cnpg-kind-sync-unlogged-cron-window-repro.sh
```

Evidence directory:

```text
split-brain-sim/evidence/cnpg-sync-unlogged-cron-5-2-20260527T132325Z/
```

Timeline:

```text
2026-05-27T06:29:33-0700 old primary=splitbrain-1 node=cnpg-sync-unlogged-cron-5-2-worker2
2026-05-27T06:29:40-0700 partition-start node=cnpg-sync-unlogged-cron-5-2-worker2 old_primary=splitbrain-1
2026-05-27T06:31:09-0700 new-primary-observed=splitbrain-2 at_second=6
2026-05-27T06:33:05-0700 partition-end node=cnpg-sync-unlogged-cron-5-2-worker2
```

Observed old-primary behavior:

- The only successful old-primary SQL sample was the baseline before partition.
- After partition/failover, 61 old-primary SQL samples failed with:

```text
psql: error: connection to server on socket "/controller/run/.s.PGSQL.5432" failed: FATAL:  the database system is shutting down
```

Rows observed:

```text
splitbrain-1 | f | 2026-05-27 13:29:33.93854+00  | 2026-05-27 13:29:39.980083+00 |   700
splitbrain-2 | f | 2026-05-27 13:31:11.794025+00 | 2026-05-27 13:33:36.123035+00 | 14500
```

Interpretation: the old primary had only pre-partition rows. The new primary's unlogged `pg_cron` writes started later, after promotion. No overlapping old-primary writes were observed.

## Careful conclusion

These runs did not reproduce automatic two-active-primary writes under normal CNPG settings for either 3-node `ANY 1` or 5-node `ANY 2` synchronous replication. The observed behavior is consistent with CNPG's primary isolation/liveness machinery stopping or restarting the isolated old primary before it continues accepting local SQL writes while the operator promotes a new primary.
