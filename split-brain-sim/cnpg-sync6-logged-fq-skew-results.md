# CNPG failover-quorum status-skew logged split-brain repro

This run tests the sharper `failoverQuorum: true` hypothesis: if Kubernetes/API-visible
`FailoverQuorum.Status` is stale or skewed toward the promotion side, can CNPG promote a
new primary while the old PostgreSQL primary can still commit ordinary logged rows using a
different actual synchronous quorum?

Important caveat: this is an **injected/forged status-skew experiment**, not yet proof that
CNPG naturally produces this skew. The repro explicitly patches the `FailoverQuorum` status
to list only promotion-side standbys before forcing old-side Pod-object disappearance.

## Configuration

- CNPG: `releases/cnpg-1.29.1.yaml`
- Cluster: 6 PostgreSQL instances
- Synchronous replication: `ANY 2`
- `dataDurability: required`
- `failoverQuorum: true`
- Workload: ordinary logged table inserts into `logged_loop_probe`
- Commit mode: `SET synchronous_commit=on`

Repro script:

```text
split-brain-sim/cnpg-kind-sync6-logged-fq-skew-repro.sh
```

Evidence directory:

```text
split-brain-sim/evidence/cnpg-sync6-logged-fq-skew-20260528T030933Z
```

## Experiment shape

1. Start a healthy 6-instance CNPG cluster.
2. Start pod-local logged insert loops on every PostgreSQL instance.
3. Preserve old-side replication among:
   - old primary: `splitbrain-1`
   - old-side synchronous standbys: `splitbrain-2`, `splitbrain-3`
4. Partition old side away from the Kubernetes/API-visible promotion side.
5. Patch `FailoverQuorum.Status` to list only promotion-side standbys:

```json
{"status":{"method":"any","primary":"splitbrain-1","standbyNumber":2,"standbyNames":["splitbrain-4","splitbrain-5","splitbrain-6"]}}
```

6. Stop kubelet on the old primary and old-side standby nodes.
7. Force-delete the old-side Pod objects so CNPG/API no longer sees them, while their
   containers/postmasters keep running.
8. Observe SQL writability on both sides.

## Timeline

```text
2026-05-27T20:18:02-0700 old primary=splitbrain-1 node=cnpg-sync6-logged-fq-skew-worker5 ip=10.244.3.4
2026-05-27T20:18:18-0700 keep-with-old=splitbrain-2 splitbrain-3 keep_ips=10.244.6.4 10.244.1.4 sync_set=splitbrain-2 splitbrain-3 splitbrain-4 splitbrain-5 splitbrain-6
2026-05-27T20:18:18-0700 partition-start sideA=splitbrain-1,splitbrain-2 splitbrain-3 primary_node_ip=192.168.208.14 control_plane_ip=192.168.208.12
2026-05-27T20:19:05-0700 skew-failoverquorum promotion_side=splitbrain-4 splitbrain-5 splitbrain-6
2026-05-27T20:19:05-0700 stop-kubelet-service kept-standby=splitbrain-2 node=cnpg-sync6-logged-fq-skew-worker3
2026-05-27T20:19:07-0700 force-delete-kept-standby-pod=splitbrain-2
2026-05-27T20:19:07-0700 stop-kubelet-service kept-standby=splitbrain-3 node=cnpg-sync6-logged-fq-skew-worker2
2026-05-27T20:19:08-0700 force-delete-kept-standby-pod=splitbrain-3
2026-05-27T20:19:11-0700 stop-kubelet-service node=cnpg-sync6-logged-fq-skew-worker5
2026-05-27T20:19:13-0700 force-delete-old-primary-pod
2026-05-27T20:20:24-0700 new-primary-observed=splitbrain-4 at_second=18
2026-05-27T20:28:58-0700 partition-end
```

## Evidence

At sample `t120`, Kubernetes/CNPG had promoted `splitbrain-4`:

```text
Waiting for the instances to become active current=splitbrain-4 target=splitbrain-4 ready=3 instances=6
```

The API-visible primary was not in recovery and had quorum synchronous standbys
`splitbrain-5` and `splitbrain-6`:

```text
## API-visible primary SQL: splitbrain-4
splitbrain-5     | streaming | quorum
splitbrain-6     | streaming | quorum
origin       | pg_is_in_recovery | min                              | max                              | count
splitbrain-4 | f                 | 2026-05-28 03:20:13.903663+00    | 2026-05-28 03:28:13.471321+00   | 2669
```

The old primary was directly reachable outside Kubernetes API state, was also not in recovery,
and still had quorum synchronous standbys `splitbrain-2` and `splitbrain-3`:

```text
## direct old primary SQL: splitbrain-1 on cnpg-sync6-logged-fq-skew-worker5
splitbrain-2     | streaming | quorum
splitbrain-3     | streaming | quorum
origin       | pg_is_in_recovery | min                              | max                              | count
splitbrain-1 | f                 | 2026-05-28 03:18:03.813074+00    | 2026-05-28 03:28:15.017059+00   | 3593
```

The old primary's last observed logged-row timestamp (`03:28:15`) is after the new primary was
observed (`03:20:24`) and overlaps with rows committed on the new primary. This satisfies the
working evidence threshold for two SQL-writable primaries with ordinary WAL-backed writes.

## Interpretation

This demonstrates that, under an adversarial stale/forged `FailoverQuorum.Status`,
`failoverQuorum: true` can be made to approve a promotion even while the old PostgreSQL primary
continues committing logged rows against another actual synchronous quorum.

This should be framed narrowly:

- It **does** show the quorum decision is only as safe as the freshness/integrity of the
  `FailoverQuorum.Status` metadata it consumes.
- It **does** reproduce ordinary WAL-backed divergence with `failoverQuorum: true` when that
  metadata is skewed and kubelet/fencing fails.
- It **does not yet** prove CNPG naturally reaches this stale/skewed status state without
  external status injection.
- It reinforces the previous result: synchronous replication is not fencing; CNPG also relies
  on accurate quorum metadata and effective old-primary fencing/liveness enforcement.
