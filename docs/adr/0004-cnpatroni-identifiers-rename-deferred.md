# 0004. New identifiers use `cnpatroni`, and the broad rename is deferred

- Status: Accepted. Binding through [`CLAUDE.md`](../../CLAUDE.md) rule 2. The
  wider target naming table it points at is not accepted; see "Known gaps".
- Date: 2026-08-10 02:10:32 UTC
- Sources: [`CLAUDE.md`](../../CLAUDE.md) rule 2;
  [`naming-policy.md`](../cnpatroni/naming-policy.md);
  [`NOTICE`](../../NOTICE)

## Context

The fork inherits CloudNativePG's identifier surface whole. `naming-policy.md`
section 5 sizes it from the M0 recon against the inherited tree at commit
`b226821`: 2,211 lines carrying the Go import path in `*.go` and 2,849 repo-wide
across 634 files; 2,847 lines carrying the `cnpg.io` domain, of which
`postgresql.cnpg.io` alone is 1,385; 2,670 documentation lines across 80 files
under `docs/src`; 4,766 lines across 44 generated release manifests.

A mechanical rename of that surface is cheap to type and expensive to live with.
`naming-policy.md` section 3 records the constraint: specification section 20.2
says do not perform broad mechanical renames until the architecture is stable,
because they obscure future diffs; specification section 6.2 sequences the
compatibility moves for *before a public alpha*, not during the spike; and
specification section 5.3 lists "a public rename of all packages, labels, and
APIs during the spike" as an explicit non-goal.

The fork also has to introduce identifiers of its own from day one, and those
have no such constraint: nothing upstream collides with them.

## Decision

Two rules, from `CLAUDE.md` rule 2.

**Every identifier CloudNativePatroni introduces from scratch uses `cnpatroni`
immediately**, in the form appropriate to its surface. `naming-policy.md`
section 1 fixes the forms: `cnpatroni.io` and `alpha.cnpatroni.io` for metadata
namespaces, `cnpatroni.io/<lowerCamelCase>` for label and annotation keys,
`cnpatroni_` for Prometheus metrics and Postgres roles, `cnpatroni.` for the
Postgres GUC namespace, `CNPATRONI_` for environment variables, `cnpatroni-` for
install object names, and never a `latest` image tag. One root domain, no
second-level brand domains, no aliasing and no dual writes — existing
CloudNativePG clusters are not supported (specification section 6.2), so writing
both `cnpg.io/x` and `cnpatroni.io/x` "buys nothing and doubles the collision
surface".

**Existing `cnpg` and `cloudnative-pg` identifiers are not renamed yet.** The
target state is written down in `naming-policy.md` section 2 as a design
artifact, and its implementation is a later, single, atomic milestone.

Two exclusions from any eventual rename are decided now, because getting them
wrong is a licence or a dependency problem rather than a naming one
(`naming-policy.md` section 5.1, and `NOTICE` section 1):

- **Licence headers are never renamed.** Apache-2.0 section 4(b) requires
  retaining copyright notices in derived files. New files carry a
  CloudNativePatroni copyright line instead, and the resulting mixed header
  state across the tree is correct.
- **`github.com/cloudnative-pg/machinery`, `cnpg-i` and `barman-cloud` are
  upstream dependencies, not fork identifiers.** Any rewrite must exclude them
  explicitly.

Identifiers Patroni owns are never written, patched or reconciled by an operator
reconciler (`CLAUDE.md` rule 2; `naming-policy.md` section 4).

## Consequences

Anyone adding a label, annotation, metric, environment variable or container
name has to read `naming-policy.md` first — `CLAUDE.md` rule 2 says so
explicitly — because the new identifier is fixed at creation and the inherited
one beside it is not.

The eventual rename has a checklist rather than a search. `naming-policy.md`
section 5.2 gives twelve ordered, independently testable steps, ending with the
Go module path, which moves last and only once the upstream merge tooling can
rewrite import paths deterministically. None of the twelve is authorised.

The rename is not cosmetic when it lands. `naming-policy.md` section 5.3 lists
the surfaces where it is a breaking change needing a documented migration: the
API group and all 11 CRD names, every documented label and annotation key, the
finalizers — where a rename without stripping the old one wedges deletion — the
Service and Secret suffixes, PVC names, container names, port names, the `cnpg_`
metric prefix in every dashboard and alert rule, the in-database roles and the
CEL-enforced reserved role prefix, the operator install identity, the `kubectl
cnpg` verb, the log field names, and the Kubernetes event source components.

Some surfaces are deletions rather than renames and can move earlier: the
deprecated bare `role` label, three deprecated annotations, and the finalizer
`cnpg.io/cleanupPlugin` have no legacy objects to support. So can the
leader-election Lease ID `db9c8771.cnpg.io`, which otherwise makes a
co-installed CloudNativePatroni and CloudNativePG contend for the same Lease
(`naming-policy.md` section 3).

## Enforcement

Partly by convention only, and this is worth being blunt about.

| Mechanism | Path | State |
|---|---|---|
| `docs/adr/**`, `hack/cnpatroni/**`, `internal/cnpatroni/**` declared as fork-owned paths | `hack/cnpatroni/upstream/boundary.yaml`, rules `owned.*` | Active |
| `cnpatroni_`-prefixed audit and upstream tooling, `cnpatroni-*` workflow names | `hack/cnpatroni/`, `.github/workflows/cnpatroni-*.yml` | Active, follows the rule |
| CI check forbidding new `cnpg.io/` literals outside two files | none | **Proposed only, not implemented** |

## Known gaps

- **`naming-policy.md` is itself status "proposed".** Its own header says
  "Nothing has accepted it", on the grounds that this project publishes no
  maintainers and that architecture decision records are the binding instrument.
  This record therefore accepts only what `CLAUDE.md` rule 2 states: the rule for
  new identifiers, and the deferral. The target table in section 2 and the
  checklist in section 5.2 remain proposed.
- **Three collisions are explicitly deferred to a naming ADR that does not
  exist** (`naming-policy.md` section 4): Patroni's default `role_label` is
  literally the inherited bare `role` label with the same `primary` / `replica`
  vocabulary; Patroni's `scope_label` value conflicts with the operator's
  cluster label, and the recommended fix — a distinct `cnpatroni.io/scope` key —
  "deviates from specification section 8.5" and must be recorded as such; and
  the inherited primary Lease is a deletion target. None of them is decided
  here, and the first two depend on `ADR-001`, which is not written.
- **The cheap M0 guard is not implemented.** `naming-policy.md` section 3
  proposes a CI check asserting that no new `cnpg.io/` string literal is
  introduced outside `pkg/utils/labels_annotations.go` and
  `pkg/utils/finalizers.go`. No such check exists in `.github/workflows/` or in
  the `Makefile`. Until it does, the deferral is enforced by review alone.
