# 0006. Upstream integration is gated by a boundary manifest, not by git's merge result

- Status: Accepted. The manifest, the tool and the workflow are merged; the gate
  is not yet a required status check. See "Known gaps".
- Date: 2026-08-10 02:10:32 UTC
- Sources: [`fork-maintenance.md`](../cnpatroni/fork-maintenance.md);
  `hack/cnpatroni/upstream/boundary.yaml`;
  `hack/cnpatroni/upstream/` (the `cnpatroni-upstream` tool);
  `.github/workflows/cnpatroni-upstream-sync.yml`;
  [`CLAUDE.md`](../../CLAUDE.md) rule 4;
  [`ci-status.md`](../cnpatroni/ci-status.md);
  `authority-audit.md` (generated)

The generated `authority-audit.md` and `responsibility-map.md` are not checked
in. Produce them from the repository root with
`go -C hack/cnpatroni/audit run . doc`; CI publishes both as the
`cnpatroni-authority-audit` artifact.

## Context

A fork that keeps most of upstream's operator surface has to absorb upstream
changes continuously. Git will happily help, and that is the problem.

`fork-maintenance.md` opens with the measurement that motivates everything here:
"upstream added 87 lines to `pkg/management/postgres/instance.go` between the
v1.30.0 tag and the fork base, and a trial merge reported a clean tree. A clean
merge is not the same as a safe merge." That file is not incidental. The
authority audit names `pkg/management/postgres/instance.go` as the largest
single site of forbidden calls and as "a file upstream edits regularly", so the
ongoing integration cost "is dominated by a small number of hot files"
(`authority-audit.md`).

If this fork absorbs a new upstream code path that starts, stops, promotes or
demotes Postgres, it has two authorities over the database process again — the
failure mode [0003](0003-patroni-is-the-single-ha-authority.md) exists to
remove. Git's silence is therefore not evidence.

## Decision

The fork keeps a machine-readable declaration of what it does with every path,
and refuses to let an upstream change to a declared path pass without a human
reading it.

Three artefacts, per `fork-maintenance.md`:

| Artefact | Path |
|---|---|
| Boundary manifest | `hack/cnpatroni/upstream/boundary.yaml` |
| Baseline record | `hack/cnpatroni/upstream/upstream-baseline.yaml` |
| Tool: `setup`, `validate`, `report` | `hack/cnpatroni/upstream/` |

Every path falls into exactly one ownership class, and a trailing `**` catch-all
absorbs everything not declared: `upstream-untouched` (absorbed with no review),
`adapted` (a human must read the upstream hunks), `disabled` (reported, because
upstream can add a live path into a file whose old paths were cut),
`cnpatroni-owned` (upstream touching it is a name collision, not a merge), and
`deleted` (a modify/delete conflict is expected and classified as such).

Supporting decisions, all recorded in `fork-maintenance.md` and
`boundary.yaml`:

- **The tool is a separate Go module**, and the manifest and baseline live
  beside it rather than in a top-level dot-directory, so that the operator's own
  `go.mod` and `go.sum` — "the two most merge-hostile files in the repository" —
  are never touched by it.
- **Exit codes carry the verdict**: 0 nothing needs a human, 1 upstream changed
  an adapted, disabled or deleted path, 2 a textual conflict is predicted, 3 a
  path is not classified, 4 the manifest or baseline is malformed or contradicts
  git, 5 a bad command line, 6 the clone is not set up.
- **A vocabulary scan backs the classification.** `boundary.yaml` `audit_terms`
  lists the regular expressions that name high-availability authority
  (`TargetPrimary`, `pg_ctl`, `pg_rewind`, `synchronous_standby_names`,
  `switchover` and the rest); any Go file matching one and classified
  `upstream-untouched` without `audit_reviewed: true` is a finding.
- **The scan is known to be insufficient, so `required_paths` backs it up.**
  Twenty real authority paths match none of the terms, including the PID-1
  supervisor, the isolation self-fence probes and the read-write Service
  selector; validation fails if a rule stops covering one.
- **Per-rule `gate` fields turn the prose requirement into a mechanical one.**
  Rules carry `gate: [authority-audit, chaos]`, `adr-review` or `regenerate`,
  and `report` emits the union as `gate.requires_*` for the files a given
  upstream range actually touched.
- **Merges are merges.** `CLAUDE.md` rule 4 and the checklist in
  `fork-maintenance.md`: create an integration branch,
  `git merge --no-ff --no-commit upstream/main`, never rebase, never force-push,
  do not squash — "the merge commit is what preserves upstream authorship". The
  merge commit body must carry a conflict-resolution block, because `git rerere`
  caches resolutions locally only and the message is the only durable record.
- **Reading the hunks is a checklist item, not a judgement call.** Step 6 of the
  13-step per-integration checklist requires answering, for every `adapted` or
  `disabled` path, whether the change adds a code path that starts, stops,
  promotes, demotes or reconfigures Postgres — "Do this even when git merged the
  file cleanly."

## Consequences

New CloudNativePatroni code must be declared in the manifest before it lands, or
`validate --drift` fails the pull request. That is the check that stops the
boundary from eroding one pull request at a time.

The manifest is a statement of intent at M0, not of fact:
`classification_state: target` says the classes record the end state the M0
authority audit decided on, while no CloudNativePG Go file has been modified
yet, and `provisional: true` downgrades the undeclared-vocabulary check from an
error to a warning. Both fields have a stated flip condition:
`classification_state` becomes `applied` when the code matches the declaration,
and `provisional` becomes `false` in the change that accepts the M0 authority
audit.

The tooling also produces the fork's own estimate of its long-term cost.
`fork-maintenance.md` requires escalation, with the report attached, if a single
integration's `authority_concentration_ratio` exceeds 0.15 or the mean over the
last three exceeds 0.10.

Compatibility with an upstream minor is defined mechanically rather than
claimed: six conditions, covering ancestry, completeness, a landed baseline, a
clean `validate --drift --strict`, passing builds and tests, and a green safety
result, with the explicit instruction that a claim made before the chaos gate
exists must say so.

## Enforcement

| Mechanism | Path |
|---|---|
| Drift gate on every pull request and push | `.github/workflows/cnpatroni-upstream-sync.yml`, `validate --drift` |
| Weekly divergence report, Mondays 06:00 UTC | the same workflow, `report` |
| Manifest schema, glob and ancestry validation | `hack/cnpatroni/upstream/internal/boundary/validate.go` |
| Textual conflict prediction | `hack/cnpatroni/upstream/internal/report/build.go`, `MergeTreeConflicts` |
| Per-integration checklist and merge message template | [`fork-maintenance.md`](../cnpatroni/fork-maintenance.md) |

## Known gaps

- **There is no custom merge driver.** The mechanism that catches a silent
  auto-merge is the manifest plus the report's `git merge-tree` conflict
  prediction and the mandatory human read of every `adapted` or `disabled` hunk
  — not a `.gitattributes` driver that forces a conflict on boundary paths. No
  such driver exists in this repository; the only merge driver in the tree is
  the inherited no-op `keep-ours` driver for `go.mod` and `go.sum` inside the
  parked `.github/workflows-upstream/backport.yml`. Anyone who expects git
  itself to stop on a boundary file today will be disappointed, which is exactly
  why step 6 of the checklist is worded the way it is.
- **The gate is advisory today.** `fork-maintenance.md` requires
  `boundary-guard` to be a required status check, "Without that, the gate is
  advisory and the boundary will erode". `ci-status.md` records that no branch
  protection is configured, "so none of these checks is required to merge
  anything".
- **The report does not run on pull requests.** `ci-status.md`, measured: "The
  upstream divergence report is reported as skipped on pull requests."
- **The `upstream-integration` label must be created by hand** or the scheduled
  job fails on its first run (`fork-maintenance.md`).
- **The chaos gate does not exist.** Checklist step 9 requires recording "chaos
  gate not yet implemented — deferred" explicitly rather than skipping it
  silently.
- **The recon document that measured the 87-line auto-merge is not in this
  repository.** `boundary.yaml` cites `recon-01`, `recon-02` and `recon-03` by
  section, and `fork-maintenance.md` reports the measurement without naming its
  source document. The measurement is recorded here on the strength of
  `fork-maintenance.md` alone.
