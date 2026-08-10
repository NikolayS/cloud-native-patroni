# Roadmap

CloudNativePatroni is an architecture spike. This roadmap is the milestone plan
from the architecture specification, section 16, summarised. There is no public
project board, no release schedule, and no date commitment; adding one would be
a guess.

Only M0 is in progress. Each milestone begins only after the previous one is
accepted.

## M0 — fork hygiene, authority audit, and the process decision

Establish a maintainable fork, prove the inherited high-availability paths are
understood, and decide the Pod process model before committing to it.

Work: fork from CloudNativePG — currently at commit `b226821` of 2026-08-06,
post-1.30.0 development on upstream `main`, which is a recorded deviation from
the `v1.30.0` base that specification section 20.1 asks for — with a documented
upstream merge process; a
reproducible build of the operator and database images; Patroni packaged and
version-reported; an authority-audit document covering every
high-availability and process-control path; a responsibility map from the
inherited instance manager to the Patroni container, a finite init, the agent,
the operator, or "disabled"; a lifecycle-root container prototype and the
minimal supervisor comparison ADR-002 needs; a process-model fault matrix with
measured results; runtime and static guardrails; a plan for validating
unsupported fields; and the initial architecture decision records accepted.

Exit: ADR-002 accepted with pros, cons, measurements, and explicit
non-guarantees; reviewers agree there is a concrete plan to make every inherited
high-availability path unreachable; CI proves the fork builds and inherited unit
tests run; no broad rename or feature work obscures the architecture diff.

## M1 — a Patroni-owned three-node cluster

Create a fresh cluster in which Patroni alone starts Postgres and elects a
leader: Patroni as the database-container lifecycle process, startup ending in
`exec patroni`, retained Pod-local facilities moved to a finite init, the agent,
the operator, or removed, three Pods and PVCs from a minimal resource, Patroni
`initdb` and `pg_basebackup` bootstrap, the Kubernetes distributed configuration
store, a selectorless write Service following the Patroni leader, and leader and
member state mirrored into status.

Exit: exactly one instance reports as primary; two replicas stream from the
leader; deleting the operator and the agents does not affect Patroni's high
availability; Patroni exiting terminates the database container and leaves no
postmaster; no inherited primary Lease exists.

## M2 — container lifecycle, probes, and routing

Make process failures and Service behaviour fail closed within the accepted
standard-mode fault model: Patroni process exit coupled to container exit;
direct Patroni startup, liveness, and readiness probes with a tested timing
budget; signal and shutdown tests; Patroni-derived role labels and read
Services; retained metrics and log integration rebased on the agent and Patroni;
authenticated Patroni REST over TLS; and the former-primary rewind or
reinitialize path.

Exit: `SIGKILL`, out-of-memory, and kubelet-healthy `SIGSTOP` scenarios leave no
writable Postgres process behind; agent failure has no high-availability effect;
stale write routing reaches only a demoted or stopped former primary; the former
primary rejoins as a replica.

## M3 — the chaos safety gate

Pass the complete spike fault model: the direct-write oracle, all twelve chaos
scenarios including the reproduction of the partition reported in CloudNativePG
issue #7407, artifact collection, a 100-iteration timing-boundary run, a
two-hour soak, and a written safety and limitations report.

Exit: no round produces acknowledged commits from two Patroni-authorized
primaries; no reachable inherited high-availability path is observed; ambiguous
and unsupported cases are documented rather than hidden.

## M4 — alpha ergonomics

Begins only after M3 is accepted, and requires separate approval. Candidate
work: supported configuration reconciliation through Patroni; manual switchover;
scale up and down; rolling restarts; one backup and recovery path; the API-group
rename and co-installation testing; and a wider Postgres and Kubernetes matrix.

## How work is prioritised

By what the next milestone's exit criteria require. Anything that does not serve
them is deferred, including renames, branding, and features — the specification
is explicit that no rename or feature work may obscure the architecture diff
during the spike.

> CloudNativePatroni is an independent project derived from CloudNativePG. It is not affiliated
> with or endorsed by CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.
