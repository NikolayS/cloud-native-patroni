# PostgreSQL non-WAL / split-brain-adjacent reproduction

Generated: 2026-05-27 11:45:10 UTC

This local PostgreSQL reproduction promotes a synchronous standby while leaving
old primary running.  It first proves a normal logged write on the old primary
blocks in `SyncRep`, then tests state classes that can still change or diverge.

## Control: logged writes block

```text
logged write exit code: 124
```

Expected: non-zero exit from the client-side timeout while the old primary has no synchronous standby; pg_stat_activity shows the backend waiting in SyncRep.

## 1. Unlogged relation divergence

Old primary view:
```text
old-primary|3
primary|1
```
Promoted primary view:
```text
promoted-primary|1
```

## 2. Sequence cached/prelogged values

Old primary nextvals after promotion:
```text
2
3
4
5
6
```
Promoted primary nextvals:
```text
34
35
36
37
38
```

Interpretation: sequence values are not regular transactional table state. PostgreSQL pre-logs future sequence state, then later nextval calls can complete on the old primary without waiting for SyncRep until the prelogged window is exhausted.

## 3. Advisory locks

```text
old primary pg_try_advisory_lock(7407): t
promoted primary pg_try_advisory_lock(7407): t
```

Both return true: advisory locks are shared-memory state, not replicated cluster state.

## 4. LISTEN/NOTIFY

Promoted-primary listener while old primary NOTIFYs:
```text
LISTEN
 pg_sleep
----------

(1 row)

```
Promoted-primary listener while promoted primary NOTIFYs:
```text
LISTEN
 pg_sleep
----------

(1 row)

Asynchronous notification "split_brain_chan" with payload "from promoted primary" received from server process with PID 77041.
```

Expected: the listener sees only the local promoted-primary notification, not the old-primary notification.

## 5. Physical and logical replication slots

Old primary slots:
```text
user_logical_slot|logical|f
user_physical_slot|physical|f
```
Promoted primary slots:
```text
<no slots>
```

User-created physical and logical replication slots are not ordinary WAL-replayed catalog rows; slots created after basebackup remain only on the old primary. PostgreSQL newer than this local test has explicit failover logical slots, but ordinary logical slots still need that failover/synchronization path to survive promotion.

## Explicitly excluded: temporary relations

Temporary relation data can bypass WAL/SyncRep, but it is session-local and not visible on either standby/promoted primary. It is not durable cluster state and is therefore excluded from the main split-brain list.

## Raw logs

Run logs were captured under: `/tmp/pg-nonwal-split-brain.gGBSl7/logs`
