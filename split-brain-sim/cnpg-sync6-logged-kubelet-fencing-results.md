# CNPG 6-instance `ANY 2` logged-table fencing experiments

This file records an adversarial attempt to make ordinary WAL-backed rows commit on an old primary while CNPG/Kubernetes promotes another primary.

The topology is intentionally stronger than the earlier unlogged repro:

- CNPG 1.29.1
- 6 PostgreSQL instances
- synchronous replication: `method: any`, `number: 2`
- `dataDurability: required`
- old side: old primary + two synchronous standbys, preserving PostgreSQL replication streams
- new side: remaining three standbys + Kubernetes API/operator
- kubelet stopped on the old-primary node, then the old-primary Pod object is force-deleted so local probe/fencing enforcement is absent
- workload: ordinary logged table inserts with `synchronous_commit=on`

Script:

- `split-brain-sim/cnpg-kind-sync6-logged-kubelet-fencing-repro.sh`

## Result A: default `failoverQuorum: true` blocked promotion

Evidence directory:

- `split-brain-sim/evidence/cnpg-sync6-logged-kubelet-fencing-20260527T221228Z/`

Timeline excerpt:

```text
2026-05-27T15:19:54-0700 old primary=splitbrain-1 node=cnpg-sync6-logged-kubelet-fencing-worker4 ip=10.244.3.4
2026-05-27T15:20:12-0700 keep-with-old=splitbrain-2 splitbrain-3 keep_ips=10.244.6.4 10.244.4.4 sync_set=splitbrain-2 splitbrain-3 splitbrain-4 splitbrain-5 splitbrain-6
2026-05-27T15:20:13-0700 partition-start sideA=splitbrain-1,splitbrain-2 splitbrain-3 primary_node_ip=192.168.208.14 control_plane_ip=192.168.208.11
2026-05-27T15:20:54-0700 stop-kubelet-service node=cnpg-sync6-logged-kubelet-fencing-worker4
2026-05-27T15:20:56-0700 force-delete-old-primary-pod
```

The old primary remained directly SQL-writable through `crictl exec`, with two quorum synchronous standbys:

```text
## direct old primary SQL: splitbrain-1 on cnpg-sync6-logged-kubelet-fencing-worker4
 pg_is_in_recovery | host
-------------------+--------------
 f                 | splitbrain-1

 application_name |   state   | sync_state
------------------+-----------+------------
 splitbrain-2     | streaming | quorum
 splitbrain-3     | streaming | quorum

 origin       | pg_is_in_recovery | max(created_at)              | count
--------------+-------------------+------------------------------+------
 splitbrain-1 | f                 | 2026-05-27 22:23:10.20649+00 | 1060
```

But CNPG did **not** promote a new primary. Controller logs showed the failover quorum check blocking the failover:

```text
Strong consistency check failed. Preventing failover.
readSetCardinality=3
writeSetCardinality=2
nodeSetCardinality=5
```

This matches the quorum math. With `ANY 2` and five possible synchronous standby names, if two synchronous standbys are kept with the old primary, only three standby names remain on the promotion side. CNPG requires `R + W > N`, so `3 + 2 > 5` is false. This is a useful negative result: with accurate FailoverQuorum metadata, the straightforward logged-table split-brain topology is prevented.

## Result B: disabling `failoverQuorum` produced logged-table split brain

Evidence directory:

- `split-brain-sim/evidence/cnpg-sync6-logged-no-fq-20260527T222325Z/`

This used the same topology and workload, but with:

```yaml
postgresql:
  synchronous:
    method: any
    number: 2
    dataDurability: required
    failoverQuorum: false
```

Timeline:

```text
2026-05-27T15:30:59-0700 old primary=splitbrain-1 node=cnpg-sync6-logged-no-fq-worker3 ip=10.244.6.4
2026-05-27T15:31:17-0700 keep-with-old=splitbrain-2 splitbrain-3 keep_ips=10.244.4.4 10.244.5.4 sync_set=splitbrain-2 splitbrain-3 splitbrain-4 splitbrain-5 splitbrain-6
2026-05-27T15:31:18-0700 partition-start sideA=splitbrain-1,splitbrain-2 splitbrain-3 primary_node_ip=192.168.208.14 control_plane_ip=192.168.208.17
2026-05-27T15:32:00-0700 stop-kubelet-service node=cnpg-sync6-logged-no-fq-worker3
2026-05-27T15:32:01-0700 force-delete-old-primary-pod
2026-05-27T15:32:57-0700 new-primary-observed=splitbrain-5 at_second=16
2026-05-27T15:40:29-0700 partition-end
```

At `t17`, the new primary was visible and SQL-writable, while the old primary was still directly SQL-writable and still had two quorum synchronous standbys:

```text
Failing over current=splitbrain-5 target=splitbrain-5 ready=5 instances=6

## API-visible primary SQL: splitbrain-5
 pg_is_in_recovery | host
-------------------+--------------
 f                 | splitbrain-5

 origin       | pg_is_in_recovery | max(created_at)              | count
--------------+-------------------+------------------------------+------
 splitbrain-5 | f                 | 2026-05-27 22:32:41.583443+00 | 1

## direct old primary SQL: splitbrain-1
 pg_is_in_recovery | host
-------------------+--------------
 f                 | splitbrain-1

 application_name |   state   | sync_state
------------------+-----------+------------
 splitbrain-2     | streaming | quorum
 splitbrain-3     | streaming | quorum

 origin       | pg_is_in_recovery | max(created_at)              | count
--------------+-------------------+------------------------------+------
 splitbrain-1 | f                 | 2026-05-27 22:33:02.420696+00 | 686
```

At `t120`, both sides had continued committing ordinary logged rows for minutes:

```text
## API-visible primary SQL: splitbrain-5
 application_name |   state   | sync_state
------------------+-----------+------------
 splitbrain-4     | streaming | quorum
 splitbrain-6     | streaming | quorum

 origin       | pg_is_in_recovery | max(created_at)              | count
--------------+-------------------+------------------------------+------
 splitbrain-5 | f                 | 2026-05-27 22:39:51.214166+00 | 2614

## direct old primary SQL: splitbrain-1
 application_name |   state   | sync_state
------------------+-----------+------------
 splitbrain-2     | streaming | quorum
 splitbrain-3     | streaming | quorum

 origin       | pg_is_in_recovery | max(created_at)              | count
--------------+-------------------+------------------------------+------
 splitbrain-1 | f                 | 2026-05-27 22:39:52.941495+00 | 3388
```

This proves ordinary WAL-backed data divergence when failover quorum protection is disabled and kubelet/fencing enforcement fails on the old-primary node.

## Interpretation

- This is **not** a proof that modern/default CNPG with `failoverQuorum: true` permits logged-table split brain. The matching default run blocked promotion exactly because the promotion side lacked a strong-enough read quorum.
- It **does** prove that synchronous replication alone is not a fencing mechanism. If the old primary keeps enough synchronous standbys, it can keep committing normal logged transactions while another side promotes, unless the failover-quorum/fencing layers prevent it.
- The scariest remaining target is therefore metadata/control-plane skew: a case where CNPG's FailoverQuorum object says the promotion side is safe while the old primary's actual PostgreSQL `synchronous_standby_names` still lets it commit with another set of standbys.
