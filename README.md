# CloudNativePatroni

<!--
  LOGO PLACEHOLDER — do not fill in by hand.
  The brand workstream owns the CloudNativePatroni mark and will replace this
  comment with the logo markup and its alt text. Until then this README ships
  without any image, deliberately: no CloudNativePG, CNCF, or vendor artwork
  may appear here.
-->

A Kubernetes operator for Postgres in which upstream Patroni is the sole
high-availability authority. CloudNativePatroni is derived from CloudNativePG
and keeps its operator surface, while delegating Postgres process lifecycle,
leader election, promotion, demotion, replication topology, and former-primary
recovery to Patroni.

> CloudNativePatroni is an independent project derived from CloudNativePG. It is not affiliated
> with or endorsed by CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.

## Project status

**Pre-alpha architecture spike. Not production software. Not usable today.**

- Nothing works yet. The fork is at milestone M0, whose deliverables are fork
  hygiene, an audit of the inherited high-availability code paths, and an
  accepted architecture decision record for the Pod process model. No
  Patroni-managed cluster can be created from this repository at this time.
- There are no releases, no published images, no installation instructions, and
  no upgrade path. Do not deploy this.
- The architecture described below is a target, not an implementation. The
  process model in particular is subject to ADR-002, which is **proposed and not
  yet accepted**.
- Out of scope for the spike, per specification section 5.3: converting an
  existing CloudNativePG cluster in place; backup, point-in-time recovery,
  scheduled backups, volume snapshots, and CNPG-I plugins; major Postgres
  upgrades; cross-region and standby clusters; synchronous replication; the
  Pooler, Database, DatabaseRole, Publication, and Subscription resources;
  alternative distributed configuration stores; a Patroni fork; and any support
  or service-level commitment.
- Milestones, per specification section 16: **M0** fork hygiene, authority
  audit, and the process ADR; **M1** a Patroni-owned three-node cluster; **M2**
  container lifecycle, probes, and routing; **M3** the chaos safety gate; **M4**
  alpha ergonomics, which begins only after M3 is accepted. Only M0 is in
  progress.

## Why this project exists

### The claim, and what it is not

Running Patroni underneath a Kubernetes operator is a well-trodden,
production-proven model. Crunchy Data PGO, StackGres, and Zalando's
postgres-operator all use Patroni for high availability. CloudNativePG is the
outlier in implementing its own.

CloudNativePatroni therefore does not claim to invent an architecture. It claims
something narrower: that CloudNativePG's operator surface — its declarative API,
its resource and lifecycle machinery, and the ecosystem built around it — is
worth keeping, and that its self-implemented high-availability layer is not. The
fork exists to combine the first with Patroni, rather than to rebuild the
operator surface that adopting an existing Patroni-based operator would ask us
to give up.

### The difference in where write authority is proven

CloudNativePG arbitrates failover through the control plane: the operator
decides which instance is primary and records that decision. An instance that
loses connectivity to the Kubernetes API has no obligation to prove it still
holds that authority, and can keep accepting writes while the remaining side
promotes a replacement. Service label changes do not stop it, because stale
kube-proxy state and workloads inside the isolated partition — including jobs
running inside Postgres itself — can still reach the old primary. This is the
failure reported in
[CloudNativePG issue #7407](https://github.com/cloudnative-pg/cloudnative-pg/issues/7407)
and described in specification section 3.2.

Patroni's model places the proof on the instance. A node may remain primary only
while it can renew the leader lock in the distributed configuration store, or
while failsafe mode confirms it can still reach every known member. Otherwise
Patroni demotes Postgres locally, before another member can win leadership.

### The argument is not one issue

Issue #7407 is a symptom, and repairing it is not the objective. The objective
is to stop reimplementing Postgres high availability. Patroni carries roughly
fifteen years of accumulated production behaviour that any self-implemented
high-availability layer must otherwise rediscover, including:

- distributed-configuration-store failsafe mode;
- watchdog keepalive tied to leader-lock renewal;
- `pg_rewind` safety, including checkpoint-before-rewind, diverged-timeline
  detection, non-superuser rewind grants, the `wal_log_hints` and data-checksum
  preconditions, and the data-directory removal policy;
- timeline and history-file checks before following a leader;
- candidate eligibility through `maximum_lag_on_failover`, `failover_priority`,
  and `nofailover`;
- the LSN-comparison leader race;
- crash-recovery semantics driven by `pg_controldata` that refuse to restart a
  possibly diverged former primary as leader;
- quorum commit and `synchronous_mode_strict`;
- permanent replication slots, and slot-position advancing on replicas so slots
  survive failover;
- `pending_restart` tracking with per-major-version parameter validation;
- `pause` for maintenance without stopping Postgres.

Adding a fence to a self-implemented layer would deliver the leader lock alone
and leave every item above to be built and maintained here.

### The counter-argument, stated honestly

Crunchy Data PGO, StackGres, and Zalando postgres-operator already work. For
anyone who does not specifically need CloudNativePG's API, resource model, and
ecosystem, adopting one of them is the cheaper path, and this project does not
argue otherwise. The case for CloudNativePatroni rests entirely on wanting to
keep that particular operator surface. If you do not, one of those projects is
the better choice.

### Why a fork rather than a contribution

Adopting Patroni would place a Python runtime in the database image and replace
a subsystem that CloudNativePG has deliberately chosen to own, so it is not a
change that project is likely to accept; that is a description of a design
difference, not a criticism of it.

### What is not claimed

- **No universal split-brain impossibility guarantee.** Without a functioning
  watchdog or an external fence, specification section 12.3 lists cases the
  project explicitly does not cover: complete VM or node pause and later resume;
  a frozen container cgroup; Patroni suspended while kubelet is stopped, hung,
  or unable to execute probes; kernel hangs; storage that acknowledges writes
  incorrectly; Byzantine networking or Kubernetes API behaviour; a compromised
  root account or a process that deliberately bypasses Patroni; and failure to
  stop Postgres before lock expiration when the whole local safety stack is
  stalled.
- **Preventing two writable primaries is not zero data loss.** The spike uses
  asynchronous replication. Acknowledged commits may be lost during failover if
  they were not replicated to the promoted standby. `maximum_lag_on_failover`
  bounds candidate eligibility; it is not a recovery-point guarantee.
  Specification sections 4.6 and 12.5 separate high-availability safety from
  durability, and durability policy is a later design with its own test matrix.

## Architecture summary

### The single-authority rule

Specification section 4.1 is normative and is quoted here verbatim. At all times
there is exactly one component with authority to decide whether PostgreSQL may
be writable: **Patroni**.

```text
For every instance and every time t:

PostgreSQL may be primary and accept writes
only if local Patroni considers itself primary
and holds valid write authority under Patroni's DCS rules.
```

The operator, the integration agent, kubelet, the container runtime, and any
optional minimal init may observe state or remove availability by terminating a
process or container. They must not grant write authority, promote Postgres, or
restart Postgres independently of Patroni.

### Process and container model

The target Pod layout, compressed from specification section 7.1:

```text
Kubernetes Pod
|
+-- config-init (optional, finite)
|
+-- patroni container (main database container)
|   |
|   +-- minimal init (optional; signal forwarding and reaping only)
|       |
|       +-- Patroni (container lifecycle root)
|           |
|           +-- PostgreSQL postmaster
|
+-- cnpatroni-agent container (sidecar; no HA or process authority)
```

The numeric PID is not the invariant. Patroni may be PID 1, or the direct child
of a transparent init such as `tini`. The invariant is that no long-lived
wrapper may remain healthy after Patroni exits and thereby leave the database
container or Postgres running. Patroni and Postgres run in the same container
and cgroup; the integration agent is a separate container so that its failure or
restart cannot keep an unsupervised Postgres process alive.

**This model is subject to ADR-002, which is proposed and not yet accepted.**
The alternative under evaluation retains a CloudNativePG-derived supervisor as
PID 1. Specification section 21 states the decision drivers, the required
experiment, and the acceptance criteria; the ADR must be accepted during M0.

### Who owns what

Compressed from the authority matrix in specification section 7.3:

| Area | Sole owner |
|---|---|
| Pod, PVC, Service, Secret, and RBAC lifecycle | Operator |
| Scheduling, resources, security context | Operator |
| Postgres process lifecycle | Patroni |
| Primary election and the leader lock | Patroni |
| Promotion and demotion | Patroni |
| Replication topology, slots, `pg_rewind`, reinitialization | Patroni |
| Dynamic high-availability configuration | Patroni |
| Write routing | Patroni-managed Endpoint behind a selectorless Service |
| Read routing | Patroni-derived Pod labels; not a write-safety boundary |
| Cluster status | Operator, as an observer; informational, never authoritative |
| Container restart and hang detection | kubelet and the container runtime |
| Hard local fencing | Patroni watchdog or an external fence, in a future strict mode |

The operator may request a switchover; it never implements one. The distributed
configuration store, not any field in the resource status, is the source of
write authority.

## Relationship to the upstream projects

### CloudNativePG

This repository is a fork of [CloudNativePG](https://github.com/cloudnative-pg/cloudnative-pg)
at release 1.30.0. The inherited code is licensed under Apache-2.0, and the
inherited documentation under CC BY 4.0. Both licences are preserved:
[`LICENSE`](LICENSE) covers the repository, [`docs/LICENSE`](docs/LICENSE)
covers `docs/`, and [`licenses/`](licenses/) carries third-party dependency
licences. Copyright notices in inherited files are retained unchanged, as
Apache-2.0 requires for derivative work. [`NOTICE`](NOTICE) records the
derivation, the attribution CC BY 4.0 requires for adapted documentation, and
the fact that files have been modified.

The merge policy is continuous upstream tracking rather than a single merge.
Specification section 20 defines the repository model, the merge policy, and the
dependency policy; the in-repository documentation for that process is collected
under [`docs/cnpatroni/`](docs/cnpatroni/) as it lands.

CloudNativePatroni does not use the CloudNativePG name, logo, or visual identity
as its own branding. It names the project only to describe derivation.

### Patroni

[Patroni](https://github.com/patroni/patroni) is licensed under the MIT licence.
It is not packaged in this repository yet. When the database image is built,
Patroni is packaged as an unmodified, pinned upstream release, with dependencies
pinned by version and hash, and its licence text and attribution ship with that
image. Behavioural patches to Patroni are not permitted: integration code
belongs in this repository, and any required change is proposed upstream first.

### The other Patroni-based operators

Crunchy Data PGO, StackGres, and Zalando postgres-operator are named in this
README because they establish the model this project adopts. StackGres is also
cited in the specification as a production precedent for the container process
model. None of these projects is affiliated with CloudNativePatroni, and nothing
here implies their endorsement.

## Who this is for

**Right now, this repository is for engineers implementing and reviewing the
fork.** It is useful if you are working through the authority audit, ADR-002,
the identifier policy, or the upstream integration process, and you need the
architecture and its constraints in one place.

**It is not for anyone who wants to run Postgres.** If you need a Postgres
operator today, use one that is released: CloudNativePG, or one of the
Patroni-based operators named above. This repository has nothing to install.

## Repository orientation

- [`docs/cnpatroni/`](docs/cnpatroni/) — documentation owned by this fork,
  including the [identifier and naming policy](docs/cnpatroni/naming-policy.md).
- [`CLAUDE.md`](CLAUDE.md) — the working rules for this repository, including
  the single-authority rule and the constraint that no broad identifier rename
  happens yet.
- [`CONTRIBUTING.md`](CONTRIBUTING.md), [`GOVERNANCE.md`](GOVERNANCE.md),
  [`MAINTAINERS.md`](MAINTAINERS.md), [`SECURITY.md`](SECURITY.md),
  [`SUPPORT.md`](SUPPORT.md), [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md),
  [`ROADMAP.md`](ROADMAP.md) — this project's own statements, not
  CloudNativePG's.
- `api/`, `internal/`, `pkg/`, `config/`, `tests/` — inherited CloudNativePG
  code, unchanged at M0.
- [`contribute/`](contribute/README.md) — inherited developer documentation,
  still largely accurate for build and test mechanics.

## Licence

Apache-2.0 for code, CC BY 4.0 for the documentation under `docs/`. See
[`LICENSE`](LICENSE), [`docs/LICENSE`](docs/LICENSE), and [`NOTICE`](NOTICE).

---

<p align="center">
<a href="https://www.postgresql.org/about/policies/trademarks/">Postgres, PostgreSQL, and the Slonik Logo</a>
are trademarks or registered trademarks of the PostgreSQL Community Association
of Canada, and used with their permission.
</p>

---

> CloudNativePatroni is an independent project derived from CloudNativePG. It is not affiliated
> with or endorsed by CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.
