# Identifier and naming policy

Status: proposed. Nothing has accepted it: this project publishes no maintainers
([`MAINTAINERS.md`](../../MAINTAINERS.md)), architecture decision records are the
binding instrument ([`GOVERNANCE.md`](../../GOVERNANCE.md)), and section 4 below
defers this policy's central open question to a naming ADR that does not exist
yet. The mechanical rename it describes is **not** authorised in any case.
Baseline: CloudNativePG at commit `b226821` (2026-08-06), post-1.30.0
development on upstream `main`; the inherited version constant reads 1.30.0.
Last updated: 2026-08-10 00:44:00 UTC.

This document states which identifiers CloudNativePatroni will own, which are
frozen until after the architecture spike, which belong to Patroni and must
never be written by an operator reconciler, and what rule applies to every new
identifier introduced from now on. It also carries the rename inventory, with
counts, so that the eventual rename has a checklist rather than a search.

## 1. The rule for new identifiers

Every identifier that CloudNativePatroni introduces from scratch uses
`cnpatroni` immediately, in the form appropriate to its surface:

| Surface | Form | Example |
|---|---|---|
| DNS-style metadata namespace | `cnpatroni.io` | `cnpatroni.io/role` |
| Alpha metadata namespace | `alpha.cnpatroni.io` | `alpha.cnpatroni.io/…` |
| Kubernetes label and annotation keys | `cnpatroni.io/<lowerCamelCase>` | `cnpatroni.io/instanceName` |
| Finalizers | `cnpatroni.io/<verb><Object>` | `cnpatroni.io/deleteDatabase` |
| Prometheus metrics | `cnpatroni_` | `cnpatroni_patroni_role` |
| Postgres roles and reserved prefix | `cnpatroni_` | `cnpatroni_metrics_exporter` |
| Postgres GUC namespace | `cnpatroni.` | `cnpatroni.config_sha256` |
| Environment variables | `CNPATRONI_` | `CNPATRONI_CLUSTER_NAME` |
| Install object names | `cnpatroni-` | `cnpatroni-controller-manager` |
| Container images | `<registry>/<org>/cloudnative-patroni:<semver>` | never a `latest` tag |

Style rules:

1. One root domain, `cnpatroni.io`. No second-level brand domains.
2. Kubernetes keys keep CloudNativePG's `lowerCamelCase` suffix style, so the
   eventual rename is a pure prefix swap and diffs against upstream stay
   readable.
3. Metrics, Postgres identifiers, and environment variables use `snake_case` and
   `SCREAMING_SNAKE_CASE`. The dotted domain never appears in a metric, GUC, or
   environment-variable name.
4. Go package paths stay lowercase and do not repeat `cnpatroni`; the module
   path already carries it. New Patroni integration code lives under
   `pkg/patroni/` and `internal/patroni/` (specification section 9.4).
5. No aliasing and no dual writes. Existing CloudNativePG clusters are not
   supported (specification section 6.2), so writing both `cnpg.io/x` and
   `cnpatroni.io/x` buys nothing and doubles the collision surface.
6. Every Patroni-owned key is namespaced `cnpatroni.io/` and is never written by
   an operator reconciler. Every operator-owned key is namespaced
   `cnpatroni.io/` and never appears in a Patroni `kubernetes.*` configuration
   stanza. These two sets are disjoint, and that disjointness should be asserted
   by a CI check.

## 2. Target naming table

The target state. None of it is implemented at M0; see section 3.

| Surface | CloudNativePG today | CloudNativePatroni target |
|---|---|---|
| Metadata namespace | `cnpg.io`, `alpha.cnpg.io` | `cnpatroni.io`, `alpha.cnpatroni.io` |
| API group | `postgresql.cnpg.io` | `postgresql.cnpatroni.io` |
| API version | `v1` | `v1alpha1` for the first CloudNativePatroni release |
| Kubebuilder domain and project name | `cnpg.io`, `cloudnative-pg-kubebuilderv4` | `cnpatroni.io`, `cloudnativepatroni` |
| Go module path | `github.com/cloudnative-pg/cloudnative-pg` | the CloudNativePatroni repository path, decided with the repository move |
| Go package layout | `pkg/…`, `internal/…` | unchanged, plus `pkg/patroni/` and `internal/patroni/` |
| Label keys | `cnpg.io/<camelCase>` (22 keys) | `cnpatroni.io/<camelCase>` |
| Deprecated bare `role` label | `role` | removed, not renamed — Patroni owns role |
| Annotation keys | `cnpg.io/<camelCase>` (42 keys) | `cnpatroni.io/<camelCase>` |
| Finalizers | `cnpg.io/delete*` (5) | `cnpatroni.io/delete*`; `cnpg.io/cleanupPlugin` deleted |
| Webhook paths and names | `/mutate-postgresql-cnpg-io-v1-<kind>`, `mcluster.cnpg.io` | regenerated from markers under `postgresql.cnpatroni.io` |
| Webhook configuration names | `cnpg-{mutating,validating}-webhook-configuration` | `cnpatroni-…` |
| kustomize prefix and namespace | `cnpg-`, `cnpg-system` | `cnpatroni-`, `cnpatroni-system` |
| Operator Deployment, ServiceAccount, ClusterRole | `cnpg-controller-manager`, `cnpg-manager` | `cnpatroni-controller-manager`, `cnpatroni-manager` |
| Webhook Service, cert Secret, CA Secret | `cnpg-webhook-service`, `cnpg-webhook-cert`, `cnpg-ca-secret` | `cnpatroni-…` |
| Webhook cert mount | `/run/secrets/cnpg.io/webhook` | `/run/secrets/cnpatroni.io/webhook` |
| Leader-election Lease ID | `db9c8771.cnpg.io` | a fresh value under `.cnpatroni.io` |
| Metric prefix | `cnpg_` | `cnpatroni_` |
| Operator binary | `manager`, staged at `/controller/manager` | `cnpatroni-manager`, staged under a `cnpatroni` scratch path |
| CLI plugin binary | `kubectl-cnpg`, invoked as `kubectl cnpg` | `kubectl-cnpatroni`, invoked as `kubectl cnpatroni` |
| Container names | `postgres`, `bootstrap-controller`, `pgbouncer` | `patroni` (runs Postgres), `cnpatroni-agent`; blocked on ADR-002 |
| Volume names | `pgdata`, `pg-wal`, `shm`, `scratch-data`, `projected` | unchanged; generic, not brand-bearing |
| Port names | `postgresql`, `metrics`, `status` | `postgresql` (must match Patroni's `kubernetes.ports[].name`), `metrics`, plus a Patroni REST port |
| Service suffixes | `-rw`, `-ro`, `-r`, `-any` | unchanged; selection logic changes, names do not |
| Secret and PVC suffixes | `-app`, `-superuser`, `-replication`, `-ca`, `-server`, `-wal`, `-tbs-` | unchanged, plus new Patroni REST and rewind secrets |
| Operator environment variables | bare `WATCH_NAMESPACE`, `OPERATOR_*`, 25 in total | all prefixed `CNPATRONI_` |
| Instance Pod environment variables | `PGDATA`, `POD_NAME`, `NAMESPACE`, `CLUSTER_NAME`, `PSQL_HISTORY`, `PGPORT`, `PGHOST` | `PG*` kept verbatim (they are libpq's); the rest become `CNPATRONI_*`; Patroni's own `PATRONI_*` added |
| Postgres roles | `cnpg_pooler_pgbouncer`, `cnpg_metrics_exporter` | `cnpatroni_pooler_pgbouncer`, `cnpatroni_metrics_exporter`, plus a Patroni rewind role |
| Reserved Postgres role prefix | `cnpg_` | `cnpatroni_` |
| HA replication-slot prefix | `_cnpg_` | Patroni manages HA slots; likely deleted rather than renamed |
| Postgres GUC namespace | `cnpg.*` | `cnpatroni.*` |
| PGDATA sentinel file | `cnpg_initialized` | `cnpatroni_initialized`, or removed if Patroni bootstrap replaces it |
| Cluster condition type | `cnpg.io/hibernation` | `cnpatroni.io/hibernation` |
| Kubernetes event source components | `cloudnative-pg`, `cloudnative-pg-backup`, … | `cnpatroni`, `cnpatroni-backup`, `cnpatroni-pooler`, `cnpatroni-plugin`, `cnpatroni-scheduledbackup` |
| `app.kubernetes.io/managed-by` value | `cloudnative-pg` | `cloudnative-patroni` |
| `app.kubernetes.io/name` value | `postgresql` | unchanged; correct per Kubernetes conventions |
| Release manifest filenames | `releases/cnpg-<version>.yaml` | `releases/cnpatroni-<version>.yaml` |

## 3. What is frozen, and why

Specification section 20.2 states: do not perform broad mechanical renames until
the architecture is stable, because they obscure future diffs. Specification
section 6.2 sequences the compatibility moves — the API group, labels,
finalizers, webhook names, and RBAC identifiers **should** move to a
CloudNativePatroni namespace *before a public alpha*, not during the spike, and
co-installation with CloudNativePG must be tested before that alpha. Section 5.3
lists "a public rename of all packages, labels, and APIs during the spike" as an
explicit non-goal.

Everything in the table in section 2 is therefore a design artifact at M0 and an
implementation task for a later, single, atomic milestone. Per-surface reasons:

| Frozen surface | Why it waits |
|---|---|
| API group `postgresql.cnpg.io` | Touches all 11 CRDs, every file under `config/`, every test manifest, and every historical release manifest. Renaming now makes every upstream merge conflict on generated CRD YAML — precisely the harm section 20.2 names. Gate: the upstream integration pipeline working first. |
| Go module path | The single largest textual diff in the repository; it conflicts with every upstream import-path change. Gate: the merge tooling being able to rewrite import paths deterministically. |
| Container names `postgres` → `patroni` | Blocked on ADR-002, which decides what runs in the container. Naming a container before deciding its contents is backwards. |
| Binary name `manager` and path `/controller/manager` | Same ADR-002 gate: whether the instance manager survives as a process is undecided. |
| `-rw`, `-ro`, `-r` Service suffixes | Deliberately kept CloudNativePG-compatible (specification section 8.7), and the `-rw` name is paired with Patroni's scope by ADR-001. |
| Metric prefix `cnpg_` | Cheap to change (two constants and one literal), but it must land with the metrics work of specification section 14.2 so dashboards are written once. |
| Label and annotation keys | These land **after** the label-authority surgery that removes the operator's role-label writer. Renaming a key that is about to be deleted is wasted churn and hides the deletion in the diff. |
| `_cnpg_` replication-slot prefix | Patroni owns HA slot management. Whether any of the inherited slot machinery is kept is an M1 question; renaming presumes it is. |

Two categories can move earlier than the rest:

- **Deletions, not renames.** The deprecated bare `role` label, the deprecated
  annotations `cnpg.io/hibernateClusterManifest`,
  `cnpg.io/hibernatePgControlData`, `cnpg.io/podEnvHash`, and the finalizer
  `cnpg.io/cleanupPlugin` have no legacy objects to support, because existing
  CloudNativePG clusters are not supported. They belong to the authority-audit
  work, and removing them early reduces the eventual rename surface.
- **The leader-election Lease ID** `db9c8771.cnpg.io`. If CloudNativePatroni is
  ever co-installed with CloudNativePG in the same namespace, both operators
  contend for the same Lease and only one runs. This must change before the
  co-installation testing that specification section 6.2 requires.

A cheap M0 guard that does not rename anything: a CI check asserting that no new
`cnpg.io/` string literal is introduced outside `pkg/utils/labels_annotations.go`
and `pkg/utils/finalizers.go`. Only a handful of such literals exist elsewhere
in non-test code today, so the allowlist is small.

## 4. Patroni-owned identifiers

Patroni writes these on its own Pod and on its own Kubernetes objects. The
operator must never write, patch, or reconcile them. Specification section 8.1
forbids the operator overwriting Patroni-managed role labels; section 8.6 makes
the write Service selectorless so Patroni owns the paired Endpoints object.

| Patroni configuration key | CloudNativePatroni value | Patroni default |
|---|---|---|
| `kubernetes.role_label` | `cnpatroni.io/role` | `role` |
| `kubernetes.leader_label_value` | `primary` | `primary` |
| `kubernetes.follower_label_value` | `replica` | `replica` |
| `kubernetes.standby_leader_label_value` | `primary` | `primary` |
| `kubernetes.scope_label` | see the open question below | `cluster-name` |
| `kubernetes.labels` | the operator-owned cluster selector | none |

Also Patroni-owned: the leader lock and the Endpoints object named after the
Patroni scope, member state, failsafe state, and Patroni's dynamic configuration.

Three collisions must be resolved by the naming ADR before any of this is
implemented:

1. **Patroni's default `role_label` is literally the inherited bare `role`
   label**, with the same `primary` / `replica` vocabulary. Leaving the default
   in place would have Patroni and an operator reconciler writing the same key
   from independent state machines. Setting `role_label: cnpatroni.io/role`
   avoids the key clash but does not by itself stop the operator writing `role`;
   that writer must be removed.
2. **`scope_label` value versus the operator's cluster label.** Patroni stamps
   `scope_label` with the value of its `scope`, which ADR-001 proposes to be
   `<cluster>-rw`, while the operator's cluster label means `<cluster>`. If both
   use the key `cnpatroni.io/cluster` they carry different values and every
   selector becomes ambiguous. The recommendation is to give Patroni a distinct
   key, `cnpatroni.io/scope`, and keep `cnpatroni.io/cluster` operator-owned.
   **This deviates from specification section 8.5** and must be recorded as such
   in the ADR.
3. **The inherited primary Lease**, created per cluster as a promotion mutex, is
   a deletion target rather than a naming problem; specification section 8.1
   forbids the operator creating or renewing it.

## 5. Rename inventory and checklist

Counts are from the M0 identifier recon against the inherited tree at commit
`b226821`
(`grep -rniE "cloudnative-?pg|cnpg"`, excluding `.git`), and are recorded so the
eventual rename can be reviewed against a number rather than a feeling. They are
sizing figures, not a work order.

### 5.1 By category

| Category | Occurrences | Nature |
|---|---:|---|
| Go import path `github.com/cloudnative-pg/cloudnative-pg` | 2,211 lines in `*.go`, 2,849 repo-wide across 634 files | mechanical; one `go.mod` change plus a rewrite |
| Licence headers | 1,974 lines in `*.go`; 1,020 files overall | **not** a rename target — see below |
| API group and domain `cnpg.io` | 2,847 lines; `postgresql.cnpg.io` alone 1,385 | wire compatibility, needs its own decision record |
| Label and annotation keys `*.cnpg.io/*` | subset of the above; 22 labels, 42 annotations, 5 finalizers | documented in `docs/src/labels_annotations.md` |
| Documentation prose | 2,670 lines across 80 files under `docs/src` | editorial; release notes are history and should be archived, not rewritten |
| Generated release manifests | 4,766 lines across 44 files in `releases/` | historical CloudNativePG artefacts; deleting them removes the work entirely |
| Config, kustomize, OLM | 210 lines across 60 files under `config/` | includes the OLM icon and display metadata |
| CI workflows | 189 lines across 21 files under `.github/` | several jobs are gated on the upstream repository owner and are inert in a fork; audit rather than assume |
| Shell and Python tooling | 171 lines under `hack/` | includes filenames such as `install-cnpg-plugin.sh` |
| Tests | 1,741 lines across 372 files | of which about 273 are YAML fixtures and templates |
| Go identifiers, excluding import paths, `cnpg.io`, and headers | 682 lines, few distinct symbols | plus four user-visible constants that are breaking changes, not cosmetics |
| Container image references | 97 operator, 73 Postgres, plus pgbouncer and test images | the fork may keep consuming upstream images as inputs; only the operator image must be rebranded |
| Website and organisation URLs | 104 occurrences of `cloudnative-pg.io`; 16 distinct sibling repositories | |
| Third-party CloudNativePG Go modules (`machinery`, `cnpg-i`, `barman-cloud`) | 358, 63, 42 | **real dependencies, never rename** — exclude from any rewrite |

Two exclusions matter and are easy to get wrong:

- **Licence headers are not renamed.** Apache-2.0 section 4(b) requires
  retaining copyright notices in derived files. New files carry a
  CloudNativePatroni copyright line instead; the resulting mixed state is
  correct and is explained in `NOTICE`.
- **`github.com/cloudnative-pg/machinery`, `cnpg-i`, and `barman-cloud` are
  upstream dependencies**, not fork identifiers. Any rewrite must exclude them
  explicitly.

Excluding licence headers, the `cnpg.io` domain, third-party module paths, and
`releases/` if it is deleted, the mechanical surface is roughly 2,200 Go
import-path lines, about 2,700 documentation lines, and about 600 lines of
configuration, CI, and tooling.

### 5.2 Ordered checklist for the eventual rename

Each step is independently testable, and none is authorised yet.

1. Flip `MetadataNamespace` and `AlphaMetadataNamespace` in
   `pkg/utils/labels_annotations.go`, and the finalizer namespace in
   `pkg/utils/finalizers.go`. This covers 22 labels, 42 annotations, and 5
   finalizers except the hand-written literals — the `cnpg.io/tablespaceName`
   key, the bare `role` key, the hibernation condition type, and one test
   constant.
2. Change the API group and the kubebuilder domain, then regenerate CRDs and
   webhook manifests. Fix the four hardcoded group literals that regeneration
   does not reach: two CEL rules in `api/v1/`, and two rules in
   `pkg/specs/roles.go`.
3. Update the nine webhook markers under `internal/webhook/v1/` and regenerate.
4. Change the kustomize `namespace` and `namePrefix`, which renames 11 install
   objects.
5. Change the webhook Secret, Service, CA Secret, cert mount path, leader
   election ID, and client certificate common name, together with the matching
   mount path and `secretName` in the operator Deployment manifest.
6. Change the operator pull-secret and webhook configuration name defaults, and
   the 25 environment-variable struct tags.
7. Change the two `PrometheusNamespace` constants and the one literal passed to
   the user-query collector.
8. Change the default image names, the Makefile image name, and the bake file.
9. Rename the kubectl plugin command directory, its command name, and its user
   agent.
10. Change the Postgres-side identifiers: reserved role prefix, role names,
    pg_ident user maps, GUC namespace, slot prefix, and the PGDATA sentinel
    file. Each of these is user-visible or in-database and needs its own
    migration note.
11. Change the Go module path and all import paths — last, and only once the
    upstream merge tooling can rewrite them deterministically.
12. Update `docs/src/labels_annotations.md` in the same commit as step 1. The
    in-code rule in `pkg/utils/labels_annotations.go` requires that any label or
    annotation change updates that page.

### 5.3 Surfaces where a rename is a breaking change

These are user contract, not cosmetics, and each needs a documented migration
before a public alpha rather than a sed: the API group and all 11 CRD names;
every documented label and annotation key; the finalizers, where a rename
without stripping the old one wedges deletion; the Service and Secret suffixes;
PVC names; container names, which appear in `kubectl logs -c`, AppArmor
annotations, and Pod patches; port names referenced by NetworkPolicies and
ServiceMonitors; the `cnpg_` metric prefix in every dashboard and alert rule;
the in-database roles and the CEL-enforced reserved role prefix; the operator
install identity; the `kubectl cnpg` verb; the log field names and logger
values; and the Kubernetes event source components.
