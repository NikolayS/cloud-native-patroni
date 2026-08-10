# Failover failure modes

Postgres failover has three distinct failure classes. They differ in cause,
scope, and whether the system ever has more than one live write history.

## 1. Data loss at failover

Asynchronous replication means that the promoted standby may be behind the old
primary. Without a synchronous standby, Postgres acknowledges a commit once the
commit record is flushed to local storage, and nothing in that acknowledgement
involves a standby, so everything acknowledged whose write-ahead log the standby
had not received and flushed by the time the old primary stopped writing is lost
when that standby is promoted.

This is the durability trade accepted when asynchronous replication is chosen.
It is not a correctness failure, and every asynchronous system has it, including
Patroni. Patroni's `maximum_lag_on_failover` limits which candidates may be
promoted, and Patroni documents the bound that follows: "the amount of lost data
on failover is worst case bounded by `maximum_lag_on_failover` bytes of
transaction log plus the amount that is written in the last `ttl` seconds". The
second term is there because the leader position a candidate is judged against
is the one last published to the distributed configuration store. That is a
bound, not a zero-loss guarantee. Synchronous replication is the mechanism that
addresses this class, and it is a separate design with its own trade-offs.

## 2. Sequential timeline fork

The old primary has already stopped when the new primary begins accepting
writes. Postgres starts a new timeline where recovery ended on the promoted
standby — at the end of the last complete write-ahead log record it had received
and replayed — and writes a timeline history file recording which timeline it
branched from and when. The old primary's tail — write-ahead log that it flushed
locally but never sent — is now orphaned on the abandoned timeline, and that
history file is what another node reads to discover that it has diverged.

There is exactly one live history at every instant. `pg_rewind` is corrective
here, though it does not rewind to the divergence point: it scans the old
primary's write-ahead log forward from the last checkpoint the two servers
share, replaces every block the old primary changed after that checkpoint with
the new primary's copy, and leaves a data directory that replays the new
timeline forward from there. Patroni then starts it as a standby. What
disappears is the old primary's orphaned tail, whose acknowledged part is
exactly the loss already incurred under class 1. Rewind adds nothing to it.

This is normal, healthy failover behaviour. The timeline fork itself needs no
special configuration; `pg_rewind` does, and its requirements fall on the server
being rewound — the old primary. That server must have had data checksums
enabled when its data directory was created, or have run with
`wal_log_hints = on`; `full_page_writes` must be on, which is its default; and
it must be shut down cleanly first. Those settings exist so that a hint-bit
change is written to the write-ahead log and the rewind scan can see it, and
neither can be supplied after the divergence, because the scan cannot recover
records that were never written. `wal_log_hints` can only be set at server
start, and is ignored when data checksums are on. Neither is a
CloudNativePatroni guarantee: the inherited configuration defaults
`wal_log_hints` to `on` and leaves data checksums to an opt-in `initdb` option,
and the operator-generated bootstrap does not yet exist. Under this architecture
the bootstrap is Patroni's; although the walking-skeleton configuration in
`poc/manifests/cluster/20-config.yaml` satisfies both preconditions — its
Patroni `bootstrap` passes `data-checksums` to `initdb` and sets
`wal_log_hints: "on"` under `postgresql.parameters` in that same block — a
hand-written prototype does not make them a CloudNativePatroni guarantee.

## 3. Concurrent timeline fork

Two nodes accept writes past the divergence point at the same time. Both
accumulate transactions that clients were told had committed. This is what this
document means by split brain: the term has no canonical definition, so it is
fixed here to name the single-writer violation, this class and nothing else.
Classes 1 and 2 are durability outcomes and are never called split brain here.
Where a cited source uses the term more widely, that is its sense and not this
one — the issue cited at the end of this section carries "split-brain" in its
title. This class is categorically different from the first two, and that
difference is the reason this project exists.

- The loss is not the last interval of replication lag. It is every write on
  the losing side for however long the divergence lasted, and no timer bounds
  it: `maximum_lag_on_failover` and the lease interval bound a decision, and
  both assume the deciding process is still running.
- `pg_rewind` inverts from corrective to destructive. It discards work that was
  genuinely committed and acknowledged. It does report the divergence point and
  the common checkpoint it rewinds from, but nothing in that output separates a
  harmless tail from acknowledged commits, and nothing names what it removed.
- The two histories cannot be merged, and they are not always distinguishable:
  two standbys promoted independently compute the same next timeline identifier,
  so anything that consumes write-ahead log by timeline and segment name sees
  two different streams under identical names. Recovery is manual, and by the
  time anyone knows there are two histories the losing one has usually been
  destroyed — automated recovery rewinds or rebuilds the loser as routine
  convergence — so preserving it is a decision that has to exist before the
  incident, not during it.
- Classes 1 and 2 are bounded and expected. Class 3 is unbounded. It is an
  integrity failure, not a durability trade.

Preventing class 3 requires that the node holding write authority lose the
ability to acknowledge a commit before any replacement can start. "Refuses new
connections" and "has decided to demote" are both too weak: until the postmaster
can no longer complete a commit, sessions already open still flush and
acknowledge work onto the divergent side. Two families of control produce that
property — the node fences itself, or something outside it fences the node.
Patroni takes the first route, demoting when it cannot renew the leader lock,
which holds while the Patroni process keeps making progress, the local clock
does not jump, and the demotion completes inside the lease interval. Where those
assumptions fail, the property has to come from the second family.

In CloudNativePG the promotion decision belongs to the operator, and the
primary's own fence — the liveness-probe isolation check — does not fire while
the primary can still reach its replicas, so a primary partitioned from the
control plane alone keeps accepting writes while the primary lease expires and a
replica promotes. [issue #7407] reported a case in this family; it is closed.

### Limits of prevention

Preventing class 3 does nothing about class 1, and preventing class 1 does
nothing about class 3.
Synchronous replication alone does not stop a second writer. An isolated former
primary flushes each commit record locally before it waits for anyone, and
Postgres states the rest plainly: "If primary restarts while commits are waiting
for acknowledgment, those waiting transactions will be marked fully committed
once the primary database recovers."

## Two invariants, two controls

Some writers define split brain as any forked history containing acknowledged
commits. Under that definition, every asynchronous failover with non-zero lag
is split brain — Patroni and a plain `pg_ctl promote` against a warm standby
alike. The term then discriminates nothing and becomes a synonym for "recovery
point objective greater than zero". Worse, the corrective silently changes from
fencing to synchronous replication, re-merging two controls that must be kept
apart.

That conflation has an operational cost. If any fork is split brain, then
"fencing does not prevent split brain" is true by definition. A reader can
conclude that fencing does not matter, leaving live the failures that only
fencing prevents.

**Durability of acknowledgements.** A sequential fork violates this invariant:
the acknowledged prefix is truncated, committed transactions are erased, and
external side effects are orphaned. These are real and serious outcomes, but at
every instant of wall-clock time there was exactly one authoritative state.
The controls are synchronous replication and promoting the standby that holds
the acknowledged prefix.

**Single writer.** Only a concurrent fork violates this invariant, in addition
to the durability consequences above. Two clients can each receive an
acknowledgement for the same primary key, a sequence can allocate the same value
twice to different sessions, both branches can serve reads as though current,
and contradictory states can be observed at the same moment. These outcomes
are unreachable in the sequential case no matter how large the lag was. The
controls are fencing, a watchdog, and external node fencing.

Durability of acknowledgements is governed by synchronous replication and
promotion-target selection; the single-writer invariant is governed by
fencing. Neither is a partial version of the other. The distinction yields two
conclusions:

- Patroni with asynchronous replication permits a bounded recovery-point breach
  by design.
- Patroni without a watchdog permits split brain by accident.

## What is proven, and where the proof ends

### Consensus is settled science

The coordination problem underneath leader election is not folklore. It has
been studied formally for decades, and the core protocols carry proofs.
Lamport's *The Part-Time Parliament* (1998) and *Paxos Made Simple* (2001)
present Paxos. Oki and Liskov's *Viewstamped Replication: A New Primary Copy
Method to Support Highly-Available Distributed Systems* (1988) presents
Viewstamped Replication. Ongaro and Ousterhout's
*In Search of an Understandable Consensus Algorithm* (2014) presents Raft.
Raft's safety case additionally includes Wilcox et al.'s machine-checked Coq
proof of linearizability in
*Verdi: A Framework for Implementing and Formally Verifying Distributed
Systems* (2015).

Under the stated failure model, these protocols establish safety
unconditionally: at most one value is chosen, and a committed prefix never
diverges. The model is a non-Byzantine asynchronous network in which messages
may be lost, duplicated, delayed and reordered, and processes may crash and
recover. A majority quorum must be reachable for progress; losing the quorum
stops progress rather than weakening safety.

For this project, that means the hard part of agreeing who leads does not have
to be invented, argued about, or discovered in production. It is a theorem.

### What is not proven

Liveness is not guaranteed. Fischer, Lynch and Paterson's *Impossibility of
Distributed Consensus with One Faulty Process* (1985) proves that no
deterministic protocol can guarantee termination in a fully asynchronous system
in the presence of even one crash failure. Real systems recover liveness by
assuming partial synchrony, formalised by Dwork, Lynch and Stockmeyer's
*Consensus in the Presence of Partial Synchrony* (1988), and by using failure
detectors — which in practice means timeouts.

The accurate statement is: **safety always, under the failure model; liveness
only under timing assumptions.** Every timeout in a configuration is one of
those assumptions made concrete.

The proofs cover protocols, not implementations. They say nothing about a bug
in the code, a disk that acknowledges a write it has not made durable, a clock
that jumps, or an operator action.

### Where this project sits

Patroni is not a consensus algorithm. It is a client of one. The consensus
lives in the distributed configuration store: etcd's Raft implementation, or
the Kubernetes API server, which is itself backed by etcd. This project plans
to use the Kubernetes API server as that store. Patroni acquires and renews a
leader lease through a linearizable compare-and-swap against the store.

The property "at most one holder of the leader lease" is therefore inherited
from a proven core. That inheritance is real, and it is the reason this
architecture builds on Patroni rather than implementing coordination itself.

### The proof ends at the lease

**"At most one lease holder" is not "at most one writable postmaster."**

Bridging the two requires the node that has lost the lease to stop accepting
writes locally before the winner begins accepting them, using only local
knowledge and the local clock. No consensus theorem covers that step. It is a
local, timing-dependent engineering property, and it is exactly where watchdog
support, liveness-driven container termination and external node fencing
operate.

The surface not covered by proof is small, singular and testable: one last
mile, on one node, at one moment. Minimising that surface is the design goal,
and testing it is the purpose of the fault-injection work.

In the taxonomy above, the proven core is what makes a concurrent fork
preventable at all. The unproven last mile is why prevention must still be
demonstrated rather than asserted.

## Why split brain is hard to observe

Split brain does not happen to a correctly fenced system. A working Patroni
prevents it by construction, so a test run against a working Patroni is not
evidence about it at all. The claim can be tested only by breaking the fencing
mechanism itself and showing that the system still fails closed. A green run
against a healthy cluster proves nothing here, and the entire value of the
chaos suite is in the fencing-failure cases.

- **Patroni alive but not progressing.** `SIGSTOP`, a long garbage-collection
  pause, memory pressure, or CPU starvation can let the lease expire while
  Postgres keeps serving because nothing stopped it. Patroni cannot demote
  itself when Patroni is not running.
- **Kubelet unable to act at the same time.** In standard mode the liveness
  probe is the fence. If kubelet is stopped or hung, or the node is
  unschedulable, that fence is gone. Whole-node freezes, frozen cgroups, and a
  hung Patroni process while kubelet is also stopped are outside the current
  supported fault model and require a watchdog or external fencing.
- **No watchdog.** `/dev/watchdog` requires privileged access to a host device
  and is typically unavailable on managed Kubernetes, so Patroni's strongest
  fence is the hardest to obtain in the environments this project targets.
- **Asymmetric partition.** The primary cannot reach the distributed
  configuration store, but clients can still reach the primary. Patroni demotes
  when the lease expires, but the demotion must complete before a replacement
  is promoted. That is a timing property, not a logical one, which is why fault
  injection must be randomised against the renewal boundary rather than tested
  once.
- **Storage that acknowledges writes it has not durably written.** Fencing is
  irrelevant if `fsync` lies. This is outside the current supported fault model
  and is a precondition that no amount of fencing can supply.

This project must never claim that Patroni prevents split brain. It may claim
only that a *working* Patroni does. The honest work is to enumerate what stops
Patroni working and test each case.

Nothing is proven yet: no cluster or fault-injection evidence exists. The
direct-Pod write oracle has not run: it will write to every instance by Pod IP,
bypassing Service routing, and record which instances accepted a write, at
which timeline and LSN. No chaos-suite run has supplied fencing-failure
evidence. Any eventual prevention guarantee will be bounded by the supported
fault model stated above.

## References

No standards body defines *split brain*; no IETF, ISO or ANSI document does.
Rigorous academic literature largely avoids it: Davidson, Garcia-Molina and
Skeen's *Consistency in Partitioned Networks* (1985) discusses partitions and
mutual inconsistency without the word. The term comes from neuropsychology:
the callosotomy patients studied by Sperry and Gazzaniga had surgically
separated hemispheres acting as independent agents — two decision-makers, not
two histories. Clustering stacks popularised a state-based convention: more
than one subset believes it is active, or more than one node acts as primary.
With no canonical definition, "you are using the word incorrectly" is not a
settling argument in either direction. That is why this document fixes the
sense locally.

- [Reproducing split-brain on CloudNativePG](https://coroot.com/blog/reproducing-split-brain-on-cloudnativepg/)
- [Patroni dynamic configuration](https://patroni.readthedocs.io/en/latest/dynamic_configuration.html)
- [Patroni DCS failsafe mode](https://patroni.readthedocs.io/en/latest/dcs_failsafe_mode.html)
- [Patroni watchdog support](https://patroni.readthedocs.io/en/latest/watchdog.html)

[issue #7407]: https://github.com/cloudnative-pg/cloudnative-pg/issues/7407
