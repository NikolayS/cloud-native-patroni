# 0010. Unrunnable inherited workflows are parked by moving, not deleting

- Status: Accepted. The move landed and its effect was measured on commit
  `7ea055d`; the boundary manifest was realigned in commit `bbf4f6b`.
- Date: 2026-08-10 02:10:32 UTC
- Sources: [`ci-status.md`](../cnpatroni/ci-status.md);
  `.github/workflows-upstream/`;
  `hack/cnpatroni/upstream/boundary.yaml` (rules `disable.upstream-workflows`,
  `upstream.parked-workflows`)

## Context

The fork inherited CloudNativePG's whole CI suite. Most of it encodes upstream's
release process, container registries, organisation secrets and issue
governance, and cannot pass in this repository (`ci-status.md`). Several
workflows failed on every commit for reasons no change to this fork's code could
fix: `backport.yml` needs labels and release branches that do not exist and a
`REPO_GHA_PAT` the fork does not hold; `require-labels.yml` requires a label
that does not exist, and in a single-owner fork "a human approval gate expressed
as a required check has no reviewer to satisfy it".

One was worse than noise. `release-tag.yml` fires on any closed pull request
that touches `pkg/versions/versions.go`, then creates a `vX.Y.Z` tag with
`REPO_GHA_PAT` and dispatches to `cloudnative-pg/api`, a repository this project
does not own. `ci-status.md` calls it "the active hazard" and records that it was
parked first, before any edit to that file.

## Decision

Fifteen inherited workflows are moved with `git mv` to
`.github/workflows-upstream/`. GitHub Actions reads only `.github/workflows/`,
so files in the parked directory never run.

**Moving rather than deleting is the decision, and the reason is upstream
integration** (`ci-status.md`): it "keeps the files available as the starting
point for CloudNativePatroni's own release story, and keeps the diff against
upstream readable: an upstream merge that edits one of these files still applies
to a file that exists, instead of raising a delete-versus-modify conflict on
every release."

Three qualifications belong to the same decision:

- **A `pull_request_target` workflow is read from the base branch**, so parking
  it on a feature branch does not stop it until that branch merges. For
  `pr_verify_linked_issue.yml` the fix that takes effect now is the `no-issue`
  label, which the workflow's own failure comment names as the sanctioned escape
  hatch. Measured: applying it turned a failing run into a successful one.
- **The label `ok to merge :ok_hand:` was deliberately not applied.** It is a
  human maintainer's approval signal, "and an agent applying it to its own
  change would be self-approving a merge". `require-labels.yml` is parked
  instead.
- **Deletion is still right where the fork must not restate an upstream claim.**
  `SECURITY-INSIGHTS.yml` was deleted rather than parked, because it makes
  machine-readable assertions about the CloudNativePG project — its maintainers,
  its security contacts, its release and vulnerability-handling process — "which
  a fork must not restate as its own". Its linter job in
  `continuous-integration.yml` was deleted with it, rather than kept alive for
  an input the project deliberately removed.

## Consequences

Eight workflows remain in `.github/workflows/`, two of which are fork-authored:
`cnpatroni-authority-audit.yml` and `cnpatroni-upstream-sync.yml`.

Six check runs stopped existing, because the job or workflow producing them left
the active path: `Security Insights Linter`, `Require labels`, `Add labels to
PR`, `Backport to release branches`, `Create tickets for failures`, and the
scheduled release-branch smoke test. Measured: 34 check runs with 11 failures on
commit `c8a2194`, 28 check runs with 7 failures on commit `7ea055d`.

The parked files are still upstream's, and are declared as such on both sides.
`boundary.yaml` keeps the original `.github/workflows/*` paths under
`disable.upstream-workflows` with `mechanism: moved-aside` and
`moved_to: .github/workflows-upstream/`, so "the drift gate verifies both sides
of every move", and declares the fifteen parked copies `upstream-untouched`
under `upstream.parked-workflows` "so later upstream changes remain visible".
`ci-status.md` predicted this realignment as unfinished work for another track;
it landed in commit `bbf4f6b`, including `snyk.yml`, which that prediction noted
was missing.

`.wokeignore` was edited outside the stated file ownership, on purpose:
`.github/workflows/release-publish.yml` was listed there for four occurrences of
a term the `woke` rule set rejects, and moving the file would have de-ignored it
and turned a green check red. The entry now names the parked directory.

## Enforcement

| Mechanism | Path |
|---|---|
| GitHub Actions ignores the parked directory | `.github/workflows-upstream/` |
| Both sides of every move declared | `hack/cnpatroni/upstream/boundary.yaml` |
| Drift gate on the declarations | `.github/workflows/cnpatroni-upstream-sync.yml` |

## Known gaps

- **Parked is not the same as decided.** `ci-status.md` records the parked
  workflows as unclaimed work rather than as a finished release story, and
  several active ones as residue: `continuous-delivery.yml` is untouched and its
  cloud e2e engines need credentials the fork lacks; `registry-clean.yml` still
  targets upstream's image name and runs daily, and "should become dispatch-only
  before the chaos work starts"; `refresh-licenses.yml` still uses
  `REPO_GHA_PAT`.
- **Two unpinned image and tool references remain in
  `continuous-integration.yml`** — `renovate@latest` and
  `ghcr.io/cloudnative-pg/docs:latest` — which `CLAUDE.md` rule 3 forbids. They
  are inherited lines, named in `ci-status.md` rather than fixed, to keep that
  change reviewable.
- **`make checks` fails locally** because the `checks` target still lists
  `validate-threat-model` and `validate-security-insights`, which now validate
  files the project has deleted (`ci-status.md`, "Residue"). That belongs to the
  make-targets track.
- **No branch protection is configured**, so none of the surviving checks is
  required to merge anything.
