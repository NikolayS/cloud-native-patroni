# Split-brain test methodology

A reusable playbook for reproducing **primary-isolation split-brain / data-loss**
windows. The worked example here is CloudNativePG (CNPG), but the harness is
designed to be retargeted at other Postgres-HA systems (Patroni, Stolon,
pg_auto_failover, Spilo/Zalando operator, repmgr, or raw streaming replication).

This document is the *map + runbook* over the concrete work in this fork. The
runnable artifacts live in PRs [#1] and [#4]; the reproduction-tracking issue is
[#2]; a candidate mitigation is [#3]; the full catalog (with the marked results
matrix) is issue [#5]. Upstream context: cloudnative-pg/cloudnative-pg#7407 and
discussion cloudnative-pg/cloudnative-pg#7462.

[#1]: https://github.com/NikolayS/cloudnative-pg/pull/1
[#2]: https://github.com/NikolayS/cloudnative-pg/issues/2
[#3]: https://github.com/NikolayS/cloudnative-pg/pull/3
[#4]: https://github.com/NikolayS/cloudnative-pg/pull/4
[#5]: https://github.com/NikolayS/cloudnative-pg/issues/5

---

## The failure mode under test (the hypothesis)

> A primary becomes isolated from the **control-plane / API / network** vantage
> point while PostgreSQL is still running locally and accepting writes. During
> the interval **before the old primary is fenced/stopped**:
> 1. the isolated old primary can still `COMMIT` local writes,
> 2. a replica is promoted and also commits writes,
> 3. **both servers accept writes independently** (split brain),
> 4. after reconnect + `pg_rewind`, the writes the isolated old primary
>    acknowledged are **gone** (data loss).

For CNPG the default window is bounded by the liveness probe:
`LivenessProbePeriod = 10s` (`pkg/specs/pods.go`) × default `failureThreshold = 3`
≈ **~30 s** of unfenced writability. The portable form of the hypothesis:
*every HA system has a detect→fence→promote latency; quantify what an old
primary can still acknowledge inside that window, and whether those acks survive
reconciliation.*

**Thesis that emerged from the runs:** *synchronous replication is not a fencing
mechanism.* Quorum / `failoverQuorum` math protects the **completeness of the
promotion side's data**; it is **not** a lease or fencing token that guarantees
the old primary has stopped accepting writes. Real protection comes from (a)
primary-isolation self-fencing / liveness, (b) kubelet/runtime tearing the pod
down, and (c) `failoverQuorum` *with accurate metadata*. Defeat all three and
split brain reappears even on modern CNPG.

---

## 1. Test harnesses (pick the cheapest layer that still answers the question)

| # | Harness | Files (in PRs #1 / #4) | When to use | K8s? | Operator? |
|---|---|---|---|---|---|
| H1 | **Raw PostgreSQL in Docker Compose** (1 primary + 2–4 replicas, streaming repl) | `setup-and-simulate.sh`, `simulate.sh`, `simulate-sync.sh`, `docker-compose.yml`, `docker-compose-5node.yml` | Fast, deterministic study of the **PostgreSQL-level** split-brain + `pg_rewind` loss, no operator noise | no | no |
| H2 | **Local PostgreSQL processes** (two `initdb` clusters on ports 55432/55433) | `pg-local-non-wal-split-brain-repro.sh` | Classify **which PG state survives/diverges** on promotion; no Docker needed | no | no |
| H3 | **Real CNPG operator on `kind`** (real `Cluster` CRD, real failover logic) | `cnpg-kind-7407-repro.sh`, `cnpg-kind-pgque-repro.sh`, `cnpg-kind-sync5-kubelet-dead-repro.sh`, `cnpg-kind-sync6-logged-*-repro.sh`, `cnpg-kind-sync-unlogged-cron-window-repro.sh`, `cnpg-kind-partial-connectivity-repro.sh` | The real thing: does the **operator** prevent it? | yes | yes |
| H4 | **VM-hosted runner** (Hetzner Ubuntu 24.04) | `vm-harden.sh`, `vm-bootstrap.sh` | Run the heavy `kind` repros off-laptop; harden + install docker/kind/kubectl/helm idempotently | yes | yes |

**Portability note:** H1/H2 are already operator-agnostic. H3 is the only
CNPG-specific layer; to test another system, swap the "install operator + create
cluster" block and the "who is primary?" / "promote" selectors (see §10).

---

## 2. Configuration matrix exercised

- **Replication/durability:** async (`synchronous_commit=on`, no standby names) →
  `FIRST 1 (...)` → `ANY 1 (...)` (3-node) → `ANY 2 (...)` (5/6-node).
- **CNPG modern knobs:**
  `synchronous: {method: any, number: N, dataDurability: required, failoverQuorum: true|false}`.
- **Workload durability class:** ordinary **logged** tables vs **UNLOGGED**
  tables, `synchronous_commit=on`.
- **Fencing posture:** liveness pinger enabled (default) vs disabled
  (`alpha.cnpg.io/livenessPinger: '{"enabled": false}'`); kubelet alive vs killed.

---

## 3. Fault-injection / partition toolbox

| Technique | Mechanism | Granularity |
|---|---|---|
| **Full node isolation** | `docker network disconnect kind <node>` | whole node, symmetric |
| **Container isolation** | `docker network disconnect` of a Compose container | one PG container |
| **Surgical iptables on kind nodes** | `iptables -I FORWARD 1 ... -j DROP` for pod-CIDR, then targeted `-j ACCEPT` for the primary↔kept-standby pairs (inserted **above** the DROPs because `-I ...1`); every rule appended to `rules.txt` for audited teardown | per pod-IP pair, **asymmetric** |
| **Two-port asymmetry** | block `tcp/5432` (streaming) between P and the to-be-stale replicas so they fall behind; block `tcp/9187` (instance-manager status endpoint) on the sync replicas so the **operator's scrape fails while streaming keeps flowing** | per-port |
| **Two-network overlap topology** | `docker-compose-5node.yml`: `net-a`/`net-b`, dual-homed "overlap" replicas → operator vantage overlaps the primary's quorum | topology |
| **Cut control-plane reachability** | DROP from side-A nodes/pods to the service-CIDR host (`10.96.0.1`) and control-plane `:6443` so the stale primary can't observe its own demotion | API plane |

**Why asymmetric matters:** a *clean* two-sided partition cannot produce sync-rep
split brain, because the kept set `K ≥ W` (primary still commits) and the
operator-visible set `R + W > N` (promotion allowed) are mutually exclusive when
`K` and `R` are disjoint. **Partial connectivity** makes `K ∩ R ≠ ∅`, inflating
`K + R` past `N` and breaking the exclusivity — both sides satisfy their quorums.
(Analysis grounded in `pkg/postgres/status.go`: `Less()` sorts errored replicas
to the bottom, so the operator promotes the most-advanced *reachable* replica;
`AreWalReceiversDown()` is **fail-open** — an unreachable replica's
`IsWalReceiverActive` defaults to `false`, i.e. "can't reach" is read as "WAL
receiver down".)

---

## 4. Fencing-defeat / failover-forcing techniques

These are how the runs pushed past the operator's defenses to *expose* the
window. They are adversarial by design — they answer "is sync rep alone enough?"
(no), not "does this happen on a healthy cluster?".

1. **Disable the liveness pinger** — annotation
   `alpha.cnpg.io/livenessPinger: '{"enabled": false}'` (matches the #7407 shape).
2. **Kill kubelet** on the old-primary node —
   `systemctl stop/disable kubelet; pkill -9 kubelet`. Removes probe/fencing
   enforcement so the still-running postmaster is never torn down.
3. **Force-delete the Pod object** — `kubectl delete pod --grace-period=0 --force`
   while kubelet is dead → the operator/API stops seeing it (frees promotion) but
   the **container keeps running**.
4. **Forge `FailoverQuorum.Status`** —
   `kubectl patch failoverquorum --subresource=status --type=merge` to list only
   promotion-side standbys → defeats `failoverQuorum: true`. *(Explicitly injected
   metadata skew; not proven to occur naturally.)*

---

## 5. The key trick: writing into the isolated old primary

Once a node is cut from the API you **cannot** `kubectl exec` into it. Instead, go
through the container runtime on the node:

```bash
OLD_CID=$(docker exec "$NODE" crictl ps -q \
  --label "io.kubernetes.pod.namespace=$NS,io.kubernetes.pod.name=$POD" --name postgres | head -1)
docker exec -i "$NODE" crictl exec -i "$OLD_CID" \
  psql -U postgres app -c "INSERT ...; SELECT pg_is_in_recovery();"
```

This is what proves the old primary is **still a writable primary**
(`pg_is_in_recovery() = false`, commits succeed) *after* the operator already
promoted a replacement. For VM/systemd deployments the equivalent is a local
`psql` over the unix socket on the partitioned host.

---

## 6. Workload generators

| Generator | What it proves |
|---|---|
| Manual `INSERT ... generate_series` batches (baseline + during-partition) | basic divergence |
| **pg_cron** stored-proc loop, `COMMITS_PER_TICK` rows/sec | autonomous DB-internal writes, no external client |
| **PgQue v0.2.0 + pg_cron** 10 Hz ticker + producer/consumer jobs | writes accepted with **zero external client connections** in the window |
| **Pod-local `nohup` loops** on *every* instance: `SET synchronous_commit=on; INSERT` to a logged table at `SLEEP=0.01` | high-rate logged, WAL-backed divergence, per-origin attribution |

---

## 7. Observation & evidence methodology

- **Per-second sampling loop** (`sample t1..tN`) captures, every second: cluster
  phase/`currentPrimary`/`targetPrimary`/ready/instances; pod placement;
  `failoverquorum` YAML; **API-visible-primary SQL** (`pg_is_in_recovery`,
  `SHOW synchronous_standby_names`, `pg_stat_replication`, per-origin row counts
  with `min/max(created_at)`); **and direct old-primary SQL via `crictl`**.
- **Timeline log** with timestamped markers: `old primary=…`, `partition-start`,
  `keep-with-old=…`, `skew-failoverquorum`, `stop-kubelet-service`,
  `force-delete-…-pod`, `new-primary-observed=… at_second=N`, `partition-end`.
- **Per-run evidence directory** `split-brain-sim/evidence/<run>-<UTC>/` with
  `run.log`, `timeline.log`, `rules.txt` (iptables audit), `pods-initial.txt`,
  `sync-standbys.txt`, `*-sample-*.txt`, `failoverquorum-*.{json,yaml}`,
  `summary.md`.

### The success criterion (what counts as "split brain reproduced")

**Overlapping row growth from BOTH `origin=old_primary` AND `origin=new_primary`,
with `pg_is_in_recovery()=false` on both, where the old primary's
`max(created_at)` is *after* the `new-primary-observed` timestamp.**

> Container/pod liveness alone is **explicitly not** accepted as evidence — a
> running container that refuses SQL (`FATAL: the database system is shutting
> down`) is *not* split brain.

### Two ways to close the loop on impact

- **`pg_rewind` loss quantifier** (H1): after healing, `pg_rewind` the old primary
  onto the promoted timeline and count how many old-primary partition writes
  survive (target: **0**) → exact count of lost ACK'd writes.
- **Logical-decoding probe** (H2): show a *blocked* (un-ACK'd) logged transaction
  is already visible via logical decoding on the doomed timeline — a logical
  subscriber can consume a change that will be rewound away.

---

## 8. PostgreSQL state-class taxonomy (H2, non-WAL repro)

Even when ordinary logged writes correctly block in `SyncRep`, these classes
still diverge across a promotion:

| State class | Behavior on isolated old primary | Verdict |
|---|---|---|
| Ordinary **logged** write (control) | blocks in `SyncRep` (no ack) | correctly protected |
| **UNLOGGED** tables | diverge freely | divergent |
| **Sequences** (cached/pre-logged values) | `nextval` advances without waiting for SyncRep until the prelogged window drains | divergent |
| **Advisory locks** | both sides grant the same key | shared-memory, not replicated |
| **LISTEN/NOTIFY** | events stay local to each side | divergent |
| User **replication slots** (phys/logical) | exist only on old primary | lost on promotion (absent failover slots) |
| **Temporary relations** | session-local | excluded (not cluster-visible) |

---

## 9. Results — marked ✅ success / ❌ negative / ⏳ pending

> "Success" = the methodology reproduced what it set out to (split brain or a
> clean negative). Negatives are first-class findings.

| # | Scenario | Harness / config | Outcome | Mark |
|---|---|---|---|---|
| R1 | Raw-PG partition + `pg_rewind` loss | H1, async / `FIRST 1` / `ANY 1` | Two writable primaries; **40 ACK'd writes lost** after rewind (45→10 rows). *Caveat: manually forced topology, not the operator.* | ✅ |
| R2 | CNPG #7407-style | H3, CNPG 1.25.1, 3 inst, pinger **off**, no sync | Old primary stays locally writable after promotion; old-primary partition writes **absent** after reconnect/reconcile | ✅ |
| R3 | CNPG kubelet-dead | H3, CNPG 1.29.1, 5 inst, `ANY 2`, `dataDurability=required`, `failoverQuorum=true`, kubelet killed + pod force-deleted | **Two SQL-writable primaries persisted through the full 180 s window** | ✅ (adversarial) |
| R4 | CNPG logged, `failoverQuorum=false` | H3, 6 inst, `ANY 2` | Ordinary **WAL-backed** logged divergence, both sides committing for minutes | ✅ (protection off) |
| R5 | CNPG logged, **FailoverQuorum status-skew** | H3, 6 inst, `ANY 2`, `failoverQuorum=true` **+ forged status** | Promotion approved while old primary keeps committing logged rows against its own quorum | ✅ (injected skew; not proven natural) |
| R6 | PostgreSQL non-WAL state classes | H2 | unlogged / sequences / advisory locks / NOTIFY / slots all diverge; logical decoding exposes doomed-timeline change | ✅ |
| R7 | CNPG "normal settings" window hunt | H3, 3-node `ANY 1` & 5-node `ANY 2`, `failoverQuorum=true`, **pinger default/on** | **No** two-writable-primary window found — isolated old primary was shut down/refused SQL before it could keep writing | ❌ (could not reproduce — good news) |
| R8 | CNPG logged, `failoverQuorum=true` **accurate metadata** | H3, 6 inst, `ANY 2` | Promotion **blocked**: `Strong consistency check failed … R=3, W=2, N=5` (`3+2 > 5` false) | ❌ (defense works as designed) |
| R9 | CNPG partial-connectivity race | H3/H4, 5 inst, `ANY 2`, two-port iptables asymmetry | Documented template with 3 predicted outcomes; **not yet executed** | ⏳ |

**One-line takeaways:**

- ✅ Sync replication is **not** fencing — R3/R4/R5 each produced two writable
  primaries once fencing/metadata was defeated.
- ❌ With **accurate** `failoverQuorum` metadata + working liveness (R7/R8),
  modern CNPG **blocked** the unsafe promotion. The scariest residual surface is
  therefore **control-plane / quorum-metadata skew**, not the happy path.

---

## 10. How to port this to another system

The harness is mostly system-agnostic. To retarget, replace these seams:

1. **Cluster bring-up** — swap the "install operator + apply `Cluster`" block (H3)
   for the target's deploy (Patroni+DCS, Stolon, pg_auto_failover, Spilo, etc.).
   H1/H2 need no change.
2. **"Who is primary?" selector** — replace
   `kubectl get pods -l cnpg.io/instanceRole=primary` with the target's notion
   (Patroni `/leader` key in etcd/Consul, `patronictl list`, Stolon `stolonctl`,
   a service endpoint, etc.).
3. **Fence-defeat hooks** — the generic levers stay: kill the local agent
   (kubelet / patroni / keeper), force the orchestrator to stop seeing the node,
   and keep the old primary's quorum partner reachable via the asymmetric-iptables
   recipe (§3).
4. **Write-into-isolated-primary path** — `crictl exec` for CNPG; for VM/systemd
   deployments use a local `psql` over the unix socket on the partitioned host.
5. **Keep the invariants:** the per-second dual-vantage sampler (§7), the
   **overlapping-origin success criterion**, and the `pg_rewind` / logical-decoding
   loss quantifiers are reusable as-is.

### Reusable runbook (any target)

1. Stand up N-node cluster; enable the strongest durability the target offers;
   start a per-origin write loop on every node.
2. Record baseline; capture who is primary and the real sync set.
3. Apply an **asymmetric** partition that keeps the old primary's quorum
   partner(s) reachable but hides the old primary from the orchestrator/control
   plane.
4. (Adversarial) defeat fencing: kill the local agent, force-drop the node from
   the orchestrator, optionally skew quorum metadata.
5. Sample both vantage points every second; mark `new-primary-observed`.
6. Assert the success criterion (overlapping `pg_is_in_recovery()=false` writes,
   old-primary timestamp after promotion).
7. Heal; run `pg_rewind`; count surviving old-primary writes → loss figure.
8. Archive the evidence dir; write `summary.md`.

---

## 11. Candidate mitigation (PR #3, for reference)

[#3] prototypes a **Kubernetes Lease fencing token**: the instance manager renews
`<cluster>-primary-lease` (`durationSeconds=15`, renew every `5 s`) while it is
the stable primary; on renewal failure / lost ownership it triggers self-fencing
so PostgreSQL stops accepting writes, and the operator **waits for the old lease
to expire before promoting**. This directly attacks the "sync rep ≠ fencing
token" finding, and is the kind of defense a methodology like this should
regression-test.

---

## 12. Known caveats (carried over from the runs)

- The adversarial scenarios (R3–R5) **do not** prove the race fires on a healthy
  production cluster under typical timing; they prove the *data path* is real and
  that sync rep is not a fence.
- R5's `FailoverQuorum` skew is **injected**, not shown to arise naturally.
- The PG18 image tag in the CNPG catalog may differ from `18.0-bookworm`; verify
  and override `PG_IMAGE`.
- `iptables -I FORWARD 1` ordering matters — DROPs first, ACCEPTs inserted above;
  verify with `docker exec <node> iptables -S FORWARD` if streaming to kept
  standbys unexpectedly stops.
- `kind`'s default CNI (kindnetd) does **not** enforce `NetworkPolicy`, so a
  NetworkPolicy-based partition silently no-ops — that's why these repros use
  iptables.

---

_Authorship note: written with assistance from AI._
