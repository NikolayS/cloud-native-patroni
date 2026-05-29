# CNPG kind partial-connectivity split-brain race result

This document is the run template for
`split-brain-sim/cnpg-kind-partial-connectivity-repro.sh`. The script has
**not** been executed yet in this environment (no Docker available). Run it on
a host with `docker`, `kind`, and `kubectl` to populate the evidence sections.

## What this experiment targets

A naturally-occurring race in the CNPG operator's failover decision path,
triggered by partial connectivity between the operator pod and a subset of
replicas. The race is grounded in two existing code paths:

- `pkg/postgres/status.go` `Less()` (lines 282-291): when a replica's status
  fetch errors, that record sorts to the bottom of the candidate list. The
  promotion candidate becomes the most-advanced **reachable** replica.
- `pkg/postgres/status.go` `AreWalReceiversDown()` (lines 331-342): iterates
  the in-memory status list and returns `false` only if some non-primary
  reports `IsWalReceiverActive=true`. When a status fetch fails,
  `IsWalReceiverActive` keeps its Go zero value (`false`), so the operator
  concludes "WAL receivers are down" even when the unreachable replicas are
  still streaming from the primary at the PostgreSQL level.

The race condition: partial connectivity makes the operator's HTTP scrape
to a subset of replicas fail at the moment of the failover decision, while
the PostgreSQL-level streaming traffic between primary and those replicas
keeps flowing. The operator therefore picks a stale (but reachable) replica
as the promotion candidate. Once that stale replica is promoted, the writes
already committed and ACK'd via the synchronous-quorum standbys are
disposable by `pg_rewind`.

## Configuration shape

- CloudNativePG `releases/cnpg-1.29.1.yaml`
- PostgreSQL 18 image: `ghcr.io/cloudnative-pg/postgresql:18.0-bookworm`
- 5 PostgreSQL instances
- `synchronous: { method: any, number: 2, dataDurability: required, failoverQuorum: true }`
- `synchronous_commit: on`
- `livenessProbeTimeout: 10`
- Default primary isolation liveness check enabled

## Topology

| Role | Pod          | Initial node | What we do to it                                               |
|------|--------------|--------------|----------------------------------------------------------------|
| P    | splitbrain-1 | worker-N     | Stops accepting commits when kubelet is killed + pod deleted   |
| R1   | (stale)      | worker-N     | TCP/5432 to/from P is dropped in Stage 1 -> falls behind        |
| R2   | (stale)      | worker-N     | TCP/5432 to/from P is dropped in Stage 1 -> falls behind        |
| R3   | (sync)       | worker-N     | TCP/9187 to the pod is dropped in Stage 2 -> operator sees Error|
| R4   | (sync)       | worker-N     | TCP/9187 to the pod is dropped in Stage 2 -> operator sees Error|

Initial primary and the sync-vs-stale role assignment are picked at runtime
from `pg_stat_replication.sync_state='quorum'`, so the actual pod names will
vary.

## Partition mechanism

We use iptables on the kind nodes (Option A in the task description). Two
separate ports drive the asymmetry:

- `tcp/5432` carries PostgreSQL streaming replication. Blocked in Stage 1
  between P and R1, R2 so those two go stale.
- `tcp/9187` is the CNPG instance-manager HTTPS status endpoint. Blocked in
  Stage 2 on R3, R4's kind nodes so the operator's status scrape fails for
  exactly those two pods, while leaving 5432 streaming intact.

Network policy (Option B) was not chosen: kind's default CNI (kindnetd) does
not enforce NetworkPolicy, so it would silently no-op. Docker network
disconnect (Option C) was not chosen because it would also break 5432 and
defeat the asymmetric nature of the race.

## Failover trigger

The operator does not immediately failover when only the primary's status
fetch fails - the liveness pinger may still report healthy. To force the
decision quickly we use the same pattern as
`cnpg-kind-sync5-kubelet-dead-repro.sh`:

1. `systemctl stop kubelet` on the primary's kind node.
2. `kubectl delete pod <primary> --grace-period=0 --force`.

This guarantees the operator commits to failover during the same scrape
window in which R3, R4 look unreachable. The old primary's PostgreSQL
process keeps running because kubelet is gone and cannot tear it down.

## Three documented outcomes

1. **Race wins (split-brain).** Operator promotes R1 (stale) because R3, R4
   are sorted to the bottom of the candidate list. `AreWalReceiversDown()`
   returns true. R3, R4 had ACK'd commits the new primary doesn't have.
   Heal -> rewind -> ACK'd writes lost.
2. **Race lost (operator self-corrects).** Operator either waits for a
   fresh status, picks a sync standby anyway, or refuses to promote because
   no usable candidate. Documented as a negative result.
3. **Race blocked by failoverQuorum.** The R+W>N math correctly rejects the
   promotion because too few replicas are reachable. With ANY 2 and only
   2 of 4 standbys visible: R=2, W=2, N=4, 4=4 not >4, so promotion
   is refused. This would be an interesting positive finding about the
   `failoverQuorum: true` defense.

## Evidence directory

The script writes everything to:

```text
split-brain-sim/evidence/cnpg-partial-conn-<UTC_TIMESTAMP>/
```

Captured artifacts:

- `timeline.log` - phase-by-phase events with timestamps
- `pods-initial.txt` - initial pod placement
- `sync-standbys.txt` - which standbys CNPG put in the quorum
- `rules.txt` - every iptables rule applied (for cleanup and audit)
- `stage1-ackd-lsns.txt` - LSNs R3, R4 had ACK'd before Stage 2
- `<ms>-sample-<label>.txt` - per-second cluster/SQL/operator snapshots
- `operator.log` and `operator-decisions.log` - controller log + grep
- `final-rows-new-primary.txt` - per-origin row count after heal
- `summary.md` - short generated summary

## How to run

```bash
# from repo root, on a host with docker + kind + kubectl
./split-brain-sim/cnpg-kind-partial-connectivity-repro.sh
```

Useful environment knobs:

| Variable               | Default                                       | Purpose                                  |
|------------------------|-----------------------------------------------|------------------------------------------|
| `CNPG_MANIFEST`        | `releases/cnpg-1.29.1.yaml`                   | Operator release manifest                |
| `PG_IMAGE`             | `ghcr.io/cloudnative-pg/postgresql:18.0-bookworm` | PG18 image; pick a tag that exists in the catalog |
| `PG_STATUS_PORT`       | `9187`                                        | CNPG instance-manager status port        |
| `PG_PORT`              | `5432`                                        | PostgreSQL wire protocol                 |
| `OBSERVE_SECONDS`      | `180`                                         | Length of the post-trigger sampling loop |
| `KEEP_CLUSTER`         | `0`                                           | Set to `1` to keep the kind cluster for debugging |

## Validation steps to confirm the outcome

After the script finishes, look at:

1. `summary.md` -> "Timeline" -> the `new-primary-observed` line. Compare its
   pod name against `sync-standbys.txt`. If it's one of the "make_stale"
   pods (R1 or R2), this is outcome 1 (race wins).
2. `stage1-ackd-lsns.txt` (LSNs R3, R4 ACK'd before Stage 2) versus the new
   primary's LSN in `<ms>-sample-final.txt`. If the new primary's LSN is
   below R3/R4's ACK'd LSN, ACK'd writes are missing.
3. `final-rows-new-primary.txt` per-origin counts. If the writer running
   inside the new primary's pod has lower row counts than the writers that
   ran on R3, R4 during Stage 1, those R3/R4-streamed rows were lost.
4. `operator-decisions.log` for the exact decision points. Look for the
   chosen `TargetPrimary` and for any messages about `AreWalReceiversDown`
   or quorum math.

## Run record

TODO once executed - fill in:

- Run timestamp (UTC):
- Evidence directory path:
- Initial primary / initial sync set:
- Stage 2 unreachable replicas (R3, R4):
- New primary chosen by operator:
- LSN at primary at the end of Stage 1:
- LSNs ACK'd by R3, R4 at the end of Stage 1:
- LSN at the new API-visible primary at `t=final`:
- Outcome category (1 / 2 / 3):
- Notes on operator log messages around the decision:

## Caveats

- The PG18 image tag in the CNPG image catalog may differ from
  `18.0-bookworm`. Verify against
  https://github.com/cloudnative-pg/postgres-containers and override
  `PG_IMAGE` if needed.
- iptables `-I FORWARD 1` order matters. The script inserts DROPs first then
  ACCEPTs above them. Verify with `docker exec <node> iptables -S FORWARD`
  if PG streaming to R3/R4 unexpectedly stops.
- The race window is small. If outcome 2 dominates, consider running the
  script in a loop or shortening `livenessProbeTimeout` further so the
  operator has even less time to re-poll between scrape failure and
  failover commit.
- This is an adversarial reproduction. It does not constitute proof that the
  natural race fires on a healthy production cluster under typical timing.
  It does show that the data path the bug analysis identifies is real.

_Authorship note: written with assistance from AI._
