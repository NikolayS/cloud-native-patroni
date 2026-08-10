# 0003. Patroni is the single high-availability authority

- Status: Accepted. Binding through [`CLAUDE.md`](../../CLAUDE.md) rule 1, which
  calls it non-negotiable. This record documents that rule; it does not create
  it.
- Date: 2026-08-10 02:10:32 UTC
- Sources: [`CLAUDE.md`](../../CLAUDE.md) rule 1 and preamble;
  [`authority-audit.md`](../cnpatroni/authority-audit.md);
  [`responsibility-map.md`](../cnpatroni/responsibility-map.md);
  `hack/cnpatroni/audit/policy/authority-rules.yaml`;
  `internal/cnpatroni/guard/guard.go`;
  `hack/cnpatroni/upstream/boundary.yaml`

## Context

CloudNativePatroni is a fork of CloudNativePG that replaces the inherited
Postgres high-availability and process-control layer with upstream Patroni
(`CLAUDE.md`, preamble). The inherited code holds that authority itself, in the
instance manager and in the operator, and it holds a lot of it. The static audit
in `hack/cnpatroni/audit` measures the surface on the fork base: 119 forbidden
hits in 84 functions across 39 files and 23 packages, of which
`pkg/management/postgres` alone accounts for 39 per cent and the three largest
packages account for 68 per cent (`authority-audit.md`, "How concentrated is the
authority").

Two authorities over one database process is the failure mode the whole fork
exists to remove. `fork-maintenance.md` states the same thing from the merge
side: if this fork absorbs a new upstream code path that starts, stops, promotes
or demotes Postgres, "the project has two authorities over the database process
again, which is the single failure mode the whole architecture exists to
prevent."

## Decision

Exactly one component decides whether Postgres may be writable: Patroni.
`CLAUDE.md` rule 1 quotes specification section 4.1 verbatim:

```text
For every instance and every time t:

PostgreSQL may be primary and accept writes
only if local Patroni considers itself primary
and holds valid write authority under Patroni's DCS rules.
```

The operator, the integration agent, kubelet, the container runtime, and any
minimal init may observe state, or remove availability by terminating a process
or container. They must never grant write authority, promote Postgres, or
restart Postgres independently of Patroni.

Nothing outside Patroni may, per `CLAUDE.md` rule 1 citing specification section
7.4:

1. run `pg_ctl promote`;
2. start, stop or restart PostgreSQL for a high-availability decision;
3. create or remove `standby.signal`;
4. write `primary_conninfo`;
5. write `primary_slot_name`;
6. write `synchronous_standby_names`;
7. run `pg_rewind`;
8. select a promotion candidate;
9. acquire the inherited primary Lease;
10. set `TargetPrimary`.

Identifiers Patroni owns are never written by an operator reconciler
(`CLAUDE.md` rule 2; `naming-policy.md` section 4).

A second controller is not permitted "temporarily". `CLAUDE.md` rule 1: "Any
change that reintroduces a competing decision-maker is rejected regardless of
how small the diff is."

## Consequences

Every inherited responsibility that expresses this authority loses its home.
`responsibility-map.md` maps all 22 instance-manager responsibilities onto the
destination vocabulary Patroni container, finite init, cnpatroni-agent,
operator, disabled, and the largest destination is `disabled`: exec and
supervise the postmaster, `pg_ctl` start/stop/restart/reload, promotion,
`pg_rewind` and `pg_control` handling, `standby.signal` and the replication
topology, `synchronous_standby_names`, the primary Lease, promotion-candidate
selection, the instance role label, the fencing annotation, the deliberate
liveness self-fence, in-place instance-manager replacement, and bootstrap and
replica cloning. By symbol, `authority-audit.md` counts 86 `disable`, 22
`adapt`, 1 `move`, 0 `keep`, 3 `delete-later`.

What survives moves rather than disappears, and every survivor changes in the
same way. `responsibility-map.md`: work that stays inside the Pod "must survive
Patroni restarting PostgreSQL underneath it, and must ask Patroni which instance
is the leader rather than reading the CloudNativePG cluster status". Work that
stays in the control plane "becomes an observation of Patroni rather than a
decision about PostgreSQL" — including the cluster status, which becomes a
read-only mirror of what Patroni reports.

The rule reaches beyond code into merges: `CLAUDE.md` rule 4 requires the
authority audit and the safety suite to be re-run after any upstream integration
touching operator reconciliation, the instance manager, probes, Services,
configuration, bootstrap, or upgrades. `boundary.yaml` expresses the same
requirement mechanically, with `gate: [authority-audit, chaos]` on the rules
that cover those paths.

Two authority paths were found that a literal reading of the specification's own
high-risk file list would have left live, which is why the audit is run rather
than transcribed (`authority-audit.md`): the Lease renewal, take-over and
preemption logic lives in `internal/cmd/manager/instance/run/lease`, not in
`internal/controller/primary_lease.go`; and the promotion-candidate selector is
a `sort.Interface` implementation, `(*PostgresqlStatusList).Less`, whose caller
takes the first element.

## Enforcement

| Mechanism | Path |
|---|---|
| Static authority audit and its gate | `hack/cnpatroni/audit`, see [0007](0007-static-authority-audit-enforces-rule-1.md) |
| Rule set naming the forbidden operations | `hack/cnpatroni/audit/policy/authority-rules.yaml` |
| Generated audit and responsibility map | [`authority-audit.md`](../cnpatroni/authority-audit.md), [`responsibility-map.md`](../cnpatroni/responsibility-map.md) |
| Fail-closed runtime guard | `internal/cnpatroni/guard`, see [0008](0008-fail-closed-runtime-lifecycle-guard.md) |
| CI job | `.github/workflows/cnpatroni-authority-audit.yml` |
| Upstream merge gate | `hack/cnpatroni/upstream/boundary.yaml`, see [0006](0006-boundary-manifest-gates-upstream-merges.md) |

## Known gaps

- **The audit is not a proof.** `authority-audit.md`, "Known limitations":
  reflection, templating, and any path through an interface whose implementation
  is chosen at run time can evade the type-aware engine. The mitigation named
  there is the chaos suite in M3, which does not exist yet.
- **No CI evidence about behaviour exists.** `ci-status.md`: "No CI job
  exercises a running Postgres, a running Patroni, a failover, or a network
  partition", and no end-to-end test has ever run in this repository.
- **The container startup contract has no checker.** `authority-audit.md`
  records contracts C1 to C7 for the database container, and states plainly that
  the guardrail is not implemented at M0 because the repository contains no
  entrypoint script, no image directory and no database-image Dockerfile.
- **No code has been severed yet.** `boundary.yaml` carries
  `classification_state: target`: at M0 no CloudNativePG Go file has been
  modified, so a path marked `disabled` still holds upstream's code verbatim.
  The severing work is gated on `ADR-002`, which is not written.
