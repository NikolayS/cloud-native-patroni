# 0007. A static authority audit with a ratchet baseline enforces the single-authority rule

- Status: Accepted. The scanner, the policy files, the generated documents and
  the CI job are merged and green on commit `bbf4f6b`
  ([`ci-status.md`](../cnpatroni/ci-status.md)).
- Date: 2026-08-10 02:10:32 UTC
- Sources: `hack/cnpatroni/audit/` (`main.go`, `scan.go`, `rules.go`,
  `classify.go`, `render.go`, and `policy/`);
  `authority-audit.md` (generated);
  `responsibility-map.md` (generated);
  `.github/workflows/cnpatroni-authority-audit.yml`;
  [`CLAUDE.md`](../../CLAUDE.md) rules 1 and 4

The generated `authority-audit.md` and `responsibility-map.md` are not checked
in. Produce them from the repository root with
`go -C hack/cnpatroni/audit run . doc`; CI publishes both as the
`cnpatroni-authority-audit` artifact.

## Context

[0003](0003-patroni-is-the-single-ha-authority.md) forbids a long, specific list
of operations to everything outside Patroni. A rule of that shape is worth
nothing if the only thing checking it is a reviewer's memory during a merge,
and the merge is precisely when it is under most pressure: `CLAUDE.md` rule 4
requires the audit to be re-run after any upstream integration touching operator
reconciliation, the instance manager, probes, Services, configuration, bootstrap
or upgrades.

At M0 the repository is unmodified CloudNativePG and is therefore full of
forbidden calls. A gate that failed on any of them would be red from the first
commit "and would be switched off within a week" (`authority-audit.md`).

## Decision

One program, `hack/cnpatroni/audit`, produces both the gate and the written
audit. `main.go` states why: "an audit written once by hand and a checker
written separately disagree within two upstream merges, and then both are
ignored." The subcommands are `scan`, `check`, `baseline`, `doc` and `stub`.

The design decisions worth recording:

- **Rules are policy data, not Go source.** `policy/authority-rules.yaml`:
  changing what counts as an authority hit "is then a review of a policy change,
  not of a code change". Severities are `forbidden` (an operation specification
  section 7.4 forbids, counted in the baseline), `inventory` (must be triaged,
  not itself a violation) and `observe` (counted for coverage, never gated).
- **Judgement lives in a separate classification file**,
  `policy/authority-classification.yaml`, cross-checked in both directions:
  every hit the scanner finds must be classified, and every classification must
  still name a symbol that exists. A code path that appears during an upstream
  merge and has no classification fails the check by name.
- **Resolution is type-aware.** Method calls are resolved through the type
  checker "so that a method sharing a name with a guarded one, such as
  `(*http.Server).Shutdown`, does not match". Test files and generated files are
  excluded.
- **The baseline is a ratchet, keyed by `(rule, enclosing symbol)`**, not by
  file and line. Line numbers churn on every upstream merge, and a file-keyed
  baseline "would let a merge add a brand new forbidden call inside a file that
  is already listed". A new function is always a new bucket, so that case fails.
  The recorded totals may fall and must never rise.
- **The generated documents are diff-checked.** `authority-audit.md` and
  `responsibility-map.md` are rendered by `audit doc` and the CI job runs
  `git diff --exit-code -- docs/cnpatroni/` after regenerating them, which "stops
  the written audit and the checker from drifting apart, which is how audits
  stop being read".
- **A second ratchet layer catches the author who regenerates the baseline** to
  make the first layer green: on a pull request the regenerated baseline is
  compared against the copy on the merge base. The bootstrap case, where no
  merge-base baseline exists, is fail-closed — it accepts only the recorded fork
  base `b22682115c5f3ad1bfe69bed719151d0db918e39` together with the exact
  inaugural baseline blob `d195581032a851eab6119da62b41dd903bd585f4`.
- **Exit code 2 is not compliance.** 0 clean, 1 policy violation, 2 tool or
  configuration error, 3 hygiene. `main.go`: "Separating 1 from 2 matters,
  because a tree that does not compile must never be reported as policy
  compliance."
- **The job lives in its own workflow file.**
  `.github/workflows/cnpatroni-authority-audit.yml` is deliberately separate
  from `continuous-integration.yml`, which is upstream's and is edited often, so
  that the fork's gate sits in a file upstream does not have and removes a
  recurring merge conflict (specification section 20.2).

## Consequences

The audit is what makes the fork-feasibility claim checkable rather than
asserted. It measures the scan surface (184 packages type-checked, 568 Go files
inspected, 250 findings, 112 classified symbols, 22 mapped responsibilities, 3
allowlist entries) and the concentration of authority (119 forbidden hits in 84
functions, 39 files and 23 packages; 113 recorded in the baseline after the
allowlist, in 88 buckets).

It also found what a literal reading of the specification's own high-risk file
list would have missed — the Lease preemption logic in
`internal/cmd/manager/instance/run/lease`, and the promotion-candidate selector
hidden in a `sort.Interface` implementation — which is the argument for running
a scan rather than transcribing a list.

Because the audit and the responsibility map are generated, `docs/cnpatroni/`
must not be hand-edited: both files carry a "Code generated ... DO NOT EDIT"
header, and the CI job fails on any difference.

The audit deliberately does not read git history. The two churn measurements in
`authority-audit.md` are marked as hand-measured and carry the command that
produced them, and one is explicitly bounded: it was taken over 48 commits in a
shallow clone, "roughly a fortnight of upstream activity and not a release
cycle", and should be repeated against a full clone before the M1 plan is fixed.

## Enforcement

| Step in `.github/workflows/cnpatroni-authority-audit.yml` | What it runs |
|---|---|
| Test the audit scanner | `go -C hack/cnpatroni/audit test ./...` |
| Test the runtime lifecycle guard | `go test ./internal/cnpatroni/guard/...` |
| Check the authority policy | `go -C hack/cnpatroni/audit run . check --format=github` |
| Verify the generated documents are current | `audit doc` then `git diff --exit-code -- docs/cnpatroni/` |
| Verify the authority baseline did not grow | `audit baseline --compare` against the merge base |

## Known gaps

`authority-audit.md`, "Known limitations", states four, and this record does not
soften them:

1. A static audit is not a proof. Reflection, templating, and any path through
   an interface whose implementation is chosen at run time can evade the
   type-aware engine. The mitigation is the chaos suite in M3, not more static
   analysis.
2. The audit does not scan Patroni, which is Python and lives outside this
   repository.
3. A baseline keyed by symbol notices a second forbidden call added to an
   already-listed function only through the bucket count. That is the intended
   sensitivity, not an oversight.
4. The scanner requires the tree to type-check, so during a difficult upstream
   merge it is unavailable until the build is fixed.

Two further gaps sit outside that list:

- **The container startup contract C1 to C7 has no checker at M0.**
  `authority-audit.md` gives the reason plainly: there is nothing to inspect —
  no entrypoint script, no image directory, no database-image Dockerfile — and
  "a checker written against those files today would assert nothing and would
  report success, which is worse than no checker: it would be cited as
  evidence". The M0 substitute is the classification of
  `pkg/specs.createPostgresContainers`.
- **No branch protection is configured** (`ci-status.md`), so the audit job is
  not a required check to merge.
