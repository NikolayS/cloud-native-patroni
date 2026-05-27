# CNPG 5-node synchronous replication kubelet-dead split-brain result

This run demonstrates an adversarial CNPG split-brain window with synchronous replication enabled.

Configuration shape:

- CloudNativePG 1.29.1
- 5 PostgreSQL instances
- synchronous replication `ANY 2`
- `dataDurability: required`
- `failoverQuorum: true`
- default primary isolation liveness check enabled
- old-primary side kept one synchronous standby reachable
- kubelet service on the old-primary node was stopped/disabled before deleting the old primary Pod object, simulating loss of Kubernetes probe/fencing enforcement while the PostgreSQL container/process keeps running

Evidence directory:

```text
split-brain-sim/evidence/cnpg-sync5-kubelet-dead-20260527T214346Z
```

## Timeline

```text
2026-05-27T14:51:09-0700 old primary=splitbrain-1 node=cnpg-sync5-kubelet-dead-worker5 ip=10.244.2.4
2026-05-27T14:51:26-0700 keep-with-old=splitbrain-2 ip=10.244.5.4 sync_set=splitbrain-2 splitbrain-3 splitbrain-4 splitbrain-5
2026-05-27T14:51:27-0700 partition-start sideA=splitbrain-1,splitbrain-2 primary_node_ip=192.168.208.12 keep_node_ip=192.168.208.13 control_plane_ip=192.168.208.14
2026-05-27T14:51:50-0700 stop-kubelet-service node=cnpg-sync5-kubelet-dead-worker5
2026-05-27T14:53:42-0700 force-delete-old-primary-pod
2026-05-27T14:53:53-0700 new-primary-observed=splitbrain-4 at_second=33
```

## Key observations

At `t33`, Kubernetes/CNPG had selected `splitbrain-4` as the new primary, while the old primary container `splitbrain-1` was still directly reachable through container runtime exec and was not in recovery:

```text
Failing over current=splitbrain-4 target=splitbrain-4 ready=4 instances=5
old_cid=fe2c93b74648b2659ac575344b1800c2a593e66c50b96484fca754be795e8a6d
splitbrain-4 | f | 2026-05-27 21:53:37.523461+00 | 2026-05-27 21:53:51.681902+00 |    59
splitbrain-1 | f | 2026-05-27 21:51:10.571536+00 | 2026-05-27 21:53:52.65794+00  |  1010
```

At `t34`, both primaries continued to accept local writes after the new primary was visible:

```text
Waiting for the instances to become active current=splitbrain-4 target=splitbrain-4 ready=4 instances=5
splitbrain-4 | f | 2026-05-27 21:53:37.523461+00 | 2026-05-27 21:53:55.518789+00 |    79
splitbrain-1 | f | 2026-05-27 21:51:10.571536+00 | 2026-05-27 21:53:57.832365+00 |  1036
```

The overlap persisted through the full observation window. At `t180`:

```text
Waiting for the instances to become active current=splitbrain-4 target=splitbrain-4 ready=4 instances=5
splitbrain-4 | f | 2026-05-27 21:53:37.523461+00 | 2026-05-27 22:02:35.838177+00 |  4455
splitbrain-1 | f | 2026-05-27 21:51:10.571536+00 | 2026-05-27 22:02:36.622233+00 |  5451
```

## Interpretation

This is not the normal isolated-node case where kubelet liveness restarts the old primary. In this adversarial run, Kubernetes/operator state moved on and promoted `splitbrain-4`, while kubelet enforcement on the old-primary node was absent and the old PostgreSQL process kept running.

The result is two SQL-writable primaries in the same CNPG cluster under synchronous replication: `splitbrain-4` on the Kubernetes-visible promoted side, and stale `splitbrain-1` on the unfenced old-primary side.

This supports the narrower claim that quorum/failover checks protect promotion-side data completeness, but they are not a fencing token or lease for old-primary leadership agreement when kubelet/probe fencing fails or is delayed.
