# Architecture decision records

`docs/adr/` is the directory CLAUDE.md rule 5 reserves for architecture decision
records. [`GOVERNANCE.md`](../../GOVERNANCE.md) makes those records the binding
instrument of this project: "Architectural decisions are recorded as
architecture decision records under `docs/adr/` and are binding once accepted."

This index explains the format, the numbering, and what is recorded so far.

CloudNativePatroni is an independent project derived from CloudNativePG. It is not affiliated
with or endorsed by CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.

## What these records are, and what they are not

Every record here documents a decision this fork has **already** made, and cites
the file in this repository where the decision is stated or enforced. A record
does not create a decision, and it does not restate the architecture spike
specification, which is maintained outside this repository. Where a rationale is
not recoverable from a file in this tree, the record says so under "Known gaps"
rather than supplying one.

Specification section numbers appear only where a file already in this
repository quotes or cites them. Nothing here paraphrases a specification
section that this repository does not carry.

## Format

Each record is one file, `NNNN-kebab-title.md`, with this structure:

```text
# NNNN. Sentence-case title

- Status:  Accepted | Open | Reserved | Superseded by NNNN
- Date:    YYYY-MM-DD HH:mm:ss UTC
- Sources: the files in this repository that back the record

## Context
## Decision
## Consequences
## Enforcement      (only where code or CI enforces the decision)
## Known gaps       (only where something is unresolved or unverified)
```

Status values mean:

- **Accepted** — the decision is already binding in this repository, through
  CLAUDE.md, through merged code, or through a CI gate. The record names which.
- **Open** — recorded, but not settled. The record says who has to settle it.
- **Reserved** — a number held for a record that is named elsewhere in the
  repository but has not been written.
- **Superseded by NNNN** — replaced. The superseding record links back.

The `Enforcement` section is what stops a record from being aspirational: it
names a path a reader can run or read to check that the decision is real. Where
a decision has no enforcement, the record says that too.

## Numbering

Numbers are four digits, allocated in order, and never reused. A number is
claimed by the commit that adds the file. Do not renumber an existing record;
supersede it instead, and leave the old file in place with its status changed.

**0001 and 0002 are reserved and must not be reused.** The specification and
several files already in this repository refer to `ADR-001` and `ADR-002` by
those numbers, for decisions that have not been made yet:

- `ADR-001` decides Patroni's `scope` value, which
  [`naming-policy.md`](../cnpatroni/naming-policy.md) records as proposed to be
  `<cluster>-rw`, and which pairs the read-write Service suffix with that scope.
- `ADR-002` decides what runs in the database container, and therefore whether
  the instance manager survives as a process. It is cited as a gate by
  [`naming-policy.md`](../cnpatroni/naming-policy.md),
  `authority-audit.md` (generated),
  `hack/cnpatroni/audit/policy/authority-classification.yaml`,
  `hack/cnpatroni/upstream/boundary.yaml`, and
  `internal/cnpatroni/guard/guard.go`.

Neither can be written from this repository alone, because both are decisions
the specification frames and the specification is not in this tree. Records
written here therefore start at 0003.

## The records

| # | Title | Status |
|---|---|---|
| [0001](0001-patroni-scope-pairs-with-write-service.md) | Patroni scope pairs with the write Service, and Patroni owns its Endpoints | Accepted |
| [0002](0002-patroni-is-pid-1.md) | Patroni is PID 1 in the database container | Accepted |
| [0003](0003-patroni-is-the-single-ha-authority.md) | Patroni is the single high-availability authority | Accepted |
| [0004](0004-cnpatroni-identifiers-rename-deferred.md) | New identifiers use `cnpatroni`, and the broad rename is deferred | Accepted |
| [0005](0005-fork-base-deviates-from-the-tag.md) | The fork base is upstream `main` at `b226821`, not the `v1.30.0` tag | Open |
| [0006](0006-boundary-manifest-gates-upstream-merges.md) | Upstream integration is gated by a boundary manifest, not by git's merge result | Accepted |
| [0007](0007-static-authority-audit-enforces-rule-1.md) | A static authority audit with a ratchet baseline enforces the single-authority rule | Accepted |
| [0008](0008-fail-closed-runtime-lifecycle-guard.md) | Forbidden lifecycle operations are severed by a fail-closed runtime guard | Accepted |
| [0009](0009-fork-tag-namespace-and-build-version.md) | The fork tags `cnpatroni-v*` and declares its build version in the Makefile | Accepted |
| [0010](0010-park-upstream-workflows-by-moving.md) | Unrunnable inherited workflows are parked by moving, not deleting | Accepted |

## Writing a new record

1. Read [`CLAUDE.md`](../../CLAUDE.md) first. A record that contradicts a rule
   there is a change to that rule, and has to be proposed as one.
2. Take the next free number. Do not take 0001 or 0002.
3. Cite files by path for every load-bearing claim. If a claim cannot be
   supported from a file in this repository, either drop it or move it to
   "Known gaps" and say what would settle it.
4. Add the row to the table above in the same commit.
5. `docs/adr/**` is already declared in `hack/cnpatroni/upstream/boundary.yaml`
   under the rule `owned.decision-records`, so a new record does not trip the
   drift gate. That rule still carries `state: planned`; it should lose that
   field in the change that first makes the operator code depend on a record
   here.
