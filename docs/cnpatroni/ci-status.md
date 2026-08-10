# Continuous integration status

What CloudNativePatroni's CI runs, what it does not run, what is legitimately red, and — the part
that matters most — what a green run does and does not prove.

CloudNativePatroni is a fork of CloudNativePG. It inherited the whole upstream CI suite. Most of
that suite encodes upstream's release process, container registries, organisation secrets and
issue governance, and cannot pass in this repository. This document records the triage applied to
it.

Every statement below is labelled as measured or inferred. Measured means observed in a workflow
run or read back from the GitHub API in this session. Inferred means derived from documented
GitHub Actions semantics or from repository state, without a run confirming it.

## What CloudNativePatroni CI proves today

The evidence base is three generations of checks: 34 check runs on commit `c8a2194` started
2026-08-10 00:30:04 UTC, 28 check runs on commit `7ea055d`, the first commit carrying this
triage, started 2026-08-10 00:50:21 UTC, and 28 check runs on commit `bbf4f6b`, started
2026-08-10 01:10:12 UTC, of which 27 are non-failing. Measured.

CI proves, on those commits:

- The Go code compiles and passes `golangci-lint` in both modules, and `go mod tidy` leaves no
  pending changes (`Run linters`, success).
- `govulncheck` reports no known vulnerability in the dependency set (`Run govulncheck`, success).
- The generated API documentation matches the API types (`Verify API doc is up to date`, success).
- The documentation site builds (`Verify documentation builds`, success).
- The prose under `docs/src/` spells correctly against the project wordlist, and the tree passes
  the `woke` inclusive-language rule (`Run spellcheck`, `Run woke`, both success).
- The unit test matrix passes on both Kubernetes versions, and the CRD manifests match the API
  (`Run unit tests (1.31.x)`, `Run unit tests (1.36.x)`, `Verify CRD is up to date`, all success on
  commit `bbf4f6b`). That is `make test`, which runs `./api/...`, `./cmd/...`, `./internal/...` and
  `./pkg/...` against envtest. It does not include the `tests/` end-to-end module.

CI does **not** prove any of the following, and no reader should conclude otherwise from a green
run:

- **That the operator works.** No end-to-end test has ever run in this repository. The e2e suite
  lives in `continuous-delivery.yml`, fires only on a `/test` comment or a schedule, and needs
  cluster credentials the fork does not hold. Measured: it has produced no check run on this
  pull request.
- **That a container image can be built.** `Build containers` has never reached the image build.
  No image has been produced or published by this fork's CI. Measured, up to and including commit
  `bbf4f6b`.
- **That the shell scripts are clean.** `Run shellcheck linter` was skipped, because the
  `shell-script-changed` path filter did not match. Measured. A skipped check is not a passing
  check.
- **Anything at all about Patroni.** M0 contains no Patroni integration code. The single
  high-availability authority rule is asserted by the authority audit and its guardrails, which are
  static analysis over CloudNativePG's own code. No CI job exercises a running Postgres, a running
  Patroni, a failover, or a network partition.
- **That the fork is correct against upstream.** The upstream divergence report is reported as
  skipped on pull requests. Measured.

Conclusion, labelled as such: at M0 a fully green CI run would mean the fork still compiles, still
lints, and still documents itself. It would carry no evidence about high-availability behaviour.
The measurements that would carry such evidence need a container runtime and a Kubernetes cluster,
neither of which exists yet, and are gated on M1.

## Active workflows

Eight workflows remain in `.github/workflows/`.

| Workflow | Triggers | Disposition | Note |
|---|---|---|---|
| `continuous-integration.yml` | push, pull_request, dispatch, cron | Adapted | Lint, govulncheck, unit tests, CRD and apidoc drift, docs build, container build. The fork's primary gate. Adaptations listed below. |
| `continuous-delivery.yml` | issue_comment, dispatch, cron | Kept, unused | The e2e matrix. Produces no pull-request check. Its cloud engines need AWS, Azure and GCP secrets the fork does not hold; trimming it to kind and k3d is unclaimed work, recorded under residue. |
| `codeql-analysis.yml` | push, pull_request, cron | Kept | Works in a fork with the default token. Green on commit `bbf4f6b`. |
| `spellcheck.yml` | push, pull_request, dispatch | Kept | `woke` plus `pyspelling`. Both green. |
| `registry-clean.yml` | dispatch, cron | Kept as inherited | Deletes old `*-testing` images from GHCR. Still carries upstream's image name; harmless while the fork publishes no images. Unclaimed, recorded under residue. |
| `refresh-licenses.yml` | dispatch, cron | Kept as inherited | Needs `REPO_GHA_PAT` to open its pull request and will fail on its schedule. Unclaimed, recorded under residue. |
| `cnpatroni-authority-audit.yml` | push, pull_request, dispatch | Fork-authored | Owned by the guardrails track. Not modified here. |
| `cnpatroni-upstream-sync.yml` | push, pull_request, cron, dispatch | Fork-authored | Owned by the upstream-compatibility track. Not modified here. |

### Changes applied to `continuous-integration.yml`

- **`security-insights-linter` job deleted**, together with the `security-insights-changed` path
  filter and the job output that fed it. See the next section.
- **`smoke_test_release_branches` job deleted.** It fanned a scheduled run out to `release-1.28`,
  `release-1.29` and `release-1.30`. None of those branches exists in this fork.
- **`Build meta` no longer depends on upstream release tags.** The step ran
  `git describe --tags --match 'v*'` and, under `pipefail`, failed the whole `Build containers` job
  with `fatal: No names found` before anything was built. The repository has zero tags, so this
  failed at every commit including `main`. The step now describes the fork's own `cnpatroni-v*`
  tags and, while none exists, reads `CNPATRONI_VERSION` from the `Makefile`. See the next section.
  Measured on commit `7ea055d`, with the earlier commit-count fallback: the step succeeded and
  exported `VERSION=0.0.0-dev.5134`. Measured again on commit `bbf4f6b`: success,
  `VERSION=0.0.0-dev.5140`.
- **GoReleaser runs in snapshot mode.** With `Build meta` fixed, `Build containers` reached the
  next expression of the same root cause: GoReleaser exits 1 with `git doesn't contain any tags -
  either add a tag or use --snapshot`. Measured on commit `7ea055d`. `--snapshot` is added to
  `BUILD_MANAGER_RELEASE_ARGS` and `BUILD_PLUGIN_RELEASE_ARGS`, which resolves the version from
  `.goreleaser.yml`'s `snapshot.version_template` instead. These are CI builds and never a release;
  the release path is parked. Not yet observed passing.
- **`provenance` gated on the repository variable `ENABLE_SLSA_PROVENANCE`**, and **`olm-bundle`
  gated on `ENABLE_OLM`**, both default off. These jobs were previously unreachable only because
  `Build containers` failed first. Now that it can succeed, the gate has to be explicit: OLM and
  OperatorHub publishing are not fork deliverables, and the bundle carries upstream's package name
  and maintainer metadata. `preflight` needs no separate gate — it already requires `olm-bundle` to
  have succeeded.

## Where the build version comes from

The fork version is declared in exactly one place, `Makefile`:

```make
CNPATRONI_VERSION ?= 0.1.0-dev.0
CNPATRONI_GIT_VERSION := $(shell git describe --tags --match 'cnpatroni-v*' 2>/dev/null | sed ...)
VERSION := $(or $(CNPATRONI_GIT_VERSION),$(CNPATRONI_VERSION))
```

The inherited line was `VERSION := $(shell git describe --tags --match 'v*' | sed ...)`. It assumes
the repository carries upstream CloudNativePG release tags. This fork carries none: it fetches `v*`
tags from upstream and never pushes them, and it has not started tagging itself. `git describe`
therefore exited 128, printed `fatal: No names found` on stderr, and left `VERSION` empty — which
emptied the `-ldflags` build version linked into `manager` and `kubectl-cnpg`, the OLM bundle
version, and the `buildVersion` build argument passed to the container build.

Three properties of the replacement are worth stating explicitly.

- **`VERSION` is never empty.** With no tag it is `0.1.0-dev.0`. Measured:
  `make -s print-version` prints `0.1.0-dev.0` in this tagless checkout, and `make -n build` shows
  `-X ...pkg/versions.buildVersion=0.1.0-dev.0` in the link flags.
- **The fork's tag prefix is `cnpatroni-v`, not `v`.** Matching `v*` would describe an upstream
  CloudNativePG tag that happens to be reachable after a fetch, and label a CloudNativePatroni
  artefact with an upstream release number. Once the fork cuts `cnpatroni-v0.1.0`, `git describe`
  wins over the declared default and produces the inherited `0.1.0-dev24` shape.
- **`CNPATRONI_VERSION` is `?=`, so a caller can override it**, and a command-line
  `make VERSION=...` still overrides everything, which is what `hack/setup-cluster.sh` relies on.

`pkg/versions/versions.go` is not touched. `versions.Version` stays at `1.30.0`, the inherited API
and operand compatibility constant; `CNPATRONI_VERSION` is the fork's own build version alongside
it, and only ever reaches a binary through `-ldflags`.

One inherited line changed as a direct consequence. `docker-build` passed `--snapshot` to GoReleaser
when `VERSION` was empty, using emptiness as a proxy for "this repository has no tags". `VERSION` is
never empty now, so the switch keys off `CNPATRONI_GIT_VERSION` instead, which is empty exactly when
no fork tag can be described. Measured: `make -n docker-build` still emits `--snapshot`.

The `Build meta` step in `continuous-integration.yml` follows the same order — describe
`cnpatroni-v*`, else read `CNPATRONI_VERSION` from the `Makefile` — so the workflow and a local
build agree on one value with one declaration. Its final guard, a commit-count version, exists only
so that renaming that `Makefile` line can never re-introduce an empty version; an empty version does
not fail loudly, it silently mislabels every artefact.

## Deleted, not parked: the security-insights linter

`SECURITY-INSIGHTS.yml` was deleted by the project-identity track. That file makes
machine-readable assertions about the CloudNativePG project — its maintainers, its security
contacts, its release and vulnerability-handling process — which a fork must not restate as its
own.

`dorny/paths-filter` counts a deletion as a change, so the `security-insights-linter` job fired on
the very pull request that removed its input, `sed`-ed a schema version out of a file that no
longer existed, and failed at `Makefile:320`. Measured: job `93324166152`,
`sed: can't read SECURITY-INSIGHTS.yml`.

That is our own inconsistency, not an inherited one, so the job is deleted rather than parked.
A linter kept alive for an input the project has deliberately removed would only reappear as a
failure the next time anyone touched that path.

## Parked workflows

Fifteen workflows were moved to `.github/workflows-upstream/` with `git mv`. GitHub Actions reads
only `.github/workflows/`, so files in the parked directory never run. Measured on commit
`7ea055d`: the four check runs that `require-labels.yml` and `backport.yml` used to produce are
absent from that commit's check set.

One exception applies until this branch merges. A `pull_request_target` workflow is read from the
**base** branch, so `pr_verify_linked_issue.yml` and `backport.yml` keep running from `main`'s copy
regardless of what this branch does to the file. `backport.yml` fires only on `opened`, `reopened`
and `closed`, so a push does not re-trigger it; `pr_verify_linked_issue.yml` does re-trigger, and is
handled by the label described below rather than by the move.

Moving rather than deleting keeps the files available as the starting point for CloudNativePatroni's
own release story, and keeps the diff against upstream readable: an upstream merge that edits one of
these files still applies to a file that exists, instead of raising a delete-versus-modify conflict
on every release.

| Workflow | Triggers | Why it cannot run here |
|---|---|---|
| `backport.yml` | pull_request_target | Labels every pull request `release-1.28`, `release-1.29`, `release-1.30` and cherry-picks to those branches. The labels do not exist, the branches do not exist, and the labelling step needs `REPO_GHA_PAT`. Measured failure: `Parameter token or opts.auth is required`. |
| `require-labels.yml` | pull_request | Requires the label `ok to merge :ok_hand:` on every pull request. The label does not exist and, in a single-owner fork, a human approval gate expressed as a required check has no reviewer to satisfy it. Measured failure: `required any of 'ok to merge :ok_hand:', but found 0`. |
| `pr_verify_linked_issue.yml` | pull_request_target | Requires a linked issue. This fork has no issue tracker. See the next section. |
| `chatops.yml` | issue_comment | Applies the same `ok to merge` label on a slash command; needs `REPO_GHA_PAT`. |
| `release-tag.yml` | pull_request | The active hazard. Fires on any closed pull request that touches `pkg/versions/versions.go`, which the version track will edit, then creates a `vX.Y.Z` tag with `REPO_GHA_PAT` and dispatches to `cloudnative-pg/api`, a repository this project does not own. Parked first, before any edit to that file. |
| `release-pr.yml` | push | Opens a release pull request through `REPO_GHA_PAT`. |
| `release-publish.yml` | push tag `v*` | The full upstream release: GoReleaser, GPG signing, SLSA provenance, OLM bundle, an OperatorHub pull request. Needs `GPG_PRIVATE_KEY`, `GPG_PASSPHRASE`, `REPO_GHA_PAT` and OperatorHub access. |
| `sync-api.yml` | push | Dispatches to `cloudnative-pg/api`. Self-gates on the owner and skips cleanly, but there is no CloudNativePatroni API repository for it to target. |
| `sync-docs.yml` | push, dispatch | Dispatches to `cloudnative-pg/docs`. Its manual-dispatch job does not self-gate and fails. |
| `ossf_scorecard.yml` | push, cron, dispatch, branch_protection_rule | Publishing results needs `SCORECARD_TOKEN`. |
| `osps_security_assessment.yml` | cron, dispatch | Needs `PVTR_OSPS_TOKEN`. Fails weekly without it. |
| `k8s-versions-check.yml` | cron, dispatch | Refreshes the Kubernetes version files and opens a pull request with `REPO_GHA_PAT`; also churns the cloud engines the fork is dropping. |
| `latest-postgres-version-check.yml` | cron, dispatch | Tracks `ghcr.io/cloudnative-pg/postgresql` tags. CloudNativePatroni will ship its own Patroni-enabled operand image, so this tracks the wrong registry, and its pull request needs `REPO_GHA_PAT`. |
| `close-inactive-issues.yml` | cron | Stales issues after 60 days. Harmless, and pointless without an issue tracker. |
| `snyk.yml` | push, dispatch | Gated on `SNYK_TOKEN` and skips cleanly without it. Parked for tidiness rather than because it fails; the reconnaissance recommended keeping it, and restoring it is a one-file move if a token is ever configured. |

## The linked-issue check and the `no-issue` label

`pr_verify_linked_issue.yml` fails any pull request whose body does not reference a valid issue.
This fork has no issue tracker, so the requirement cannot be met on its own terms. The workflow's
own failure comment names the sanctioned escape hatch: apply the `no-issue` label.

The workflow triggers on `pull_request_target`, which means GitHub reads the workflow file from the
**base** branch, not from the pull request's head. Parking the file on a feature branch therefore
does not stop it running until that branch merges. Verified rather than assumed: the copy of
`pr_verify_linked_issue.yml` on `main` at `b226821` was read back from the API and is byte-identical
in its logic to the head-branch copy, including the `hasNoIssueLabel` early return.

So the label, not the file move, is the fix that takes effect now. It was applied, and the effect
was measured:

- The `no-issue` label did not exist (`get_label` returned not found), and now does
  (`{"name":"no-issue","color":"ededed","description":""}`, node id
  `LA_kwDOSnu0F88AAAACvoeRVQ`).
- Applying it re-triggered `VerifyIssue` through the workflow's `labeled` event type. Run
  `31345389249`, event `pull_request_target`, started 2026-08-10 00:47:31 UTC, **conclusion:
  success**. The previous run `31344614663` on the same head commit had concluded failure.

The label `ok to merge :ok_hand:` was deliberately **not** applied. It is a human maintainer's
approval signal, and an agent applying it to its own change would be self-approving a merge.
`require-labels.yml` is parked instead, which is what the reconnaissance recommends for a
single-owner fork. If the project later gains reviewers, restoring that workflow and creating both
labels is the correct move, and the file is still in the tree for it.

## Measured effect of this triage

Commit `c8a2194` produced 34 check runs and 11 failures. Commit `7ea055d`, the first commit
carrying this triage, produced 28 check runs and 7 failures. Both measured, from the GitHub checks
API.

Six checks stopped existing, because the job or the workflow that produced them is gone from the
active path:

| Check that disappeared | Removed by |
|---|---|
| `Security Insights Linter` | job deleted from `continuous-integration.yml` |
| `Require labels` | `require-labels.yml` parked |
| `Add labels to PR` | `backport.yml` parked |
| `Backport to release branches` | `backport.yml` parked |
| `Create tickets for failures` | `backport.yml` parked |
| `smoke test release-* branches when it's a scheduled run` | job deleted from `continuous-integration.yml` |

One check went from failure to success: `Ensure Pull Request has a linked issue.`, through the
`no-issue` label.

Three checks stayed skipped and are now skipped for a stated reason rather than by accident:
`Provenance for images`, `Create OLM bundle and catalog`, `Run openshift-preflight test`.

One check was at risk of regressing and did not: `Run woke` stayed green because the `.wokeignore`
entry was repointed at the parked directory in the same change.

`Build containers` is still red. It now fails further along, at GoReleaser rather than at
`Build meta`, and the `--snapshot` change above addresses that. Whether it then reaches the image
build has not been observed.

## Current status: commit `bbf4f6b`

Measured 2026-08-10, from the GitHub Actions API, on commit `bbf4f6b`, the head of pull request 7
before the version fix described above was applied.

Six workflow runs exist on that commit. Five concluded success: `spellcheck-woke`,
`cnpatroni-upstream-sync`, `CNPatroni authority audit`, `CodeQL`, `VerifyIssue`. One concluded
failure: `continuous-integration`, run `31346382565`.

Inside that run, 19 jobs. Exactly one failed: **`Build containers`**, job `93329009987`. Across the
28 check runs on the commit, 27 are non-failing and `Build containers` is the only failure.

Everything the earlier known-red table listed has since gone green, which is why that table is
replaced by this section:

| Formerly red | Now |
|---|---|
| `Verify CRD is up to date` | success — the unparseable audit fixture was renamed out of the Go path |
| `Run unit tests (1.31.x)`, `Run unit tests (1.36.x)`, `Unit tests` | success |
| `Analyze` and `CodeQL` | success |
| `Run the authority audit` | success — the baseline ratchet gained a first-introduction guard |
| `Validate the CloudNativePatroni boundary manifest` | success — the manifest was realigned with the moved workflows in `bbf4f6b` |

### The one remaining failure

`Build containers` fails at step 11, `Run GoReleaser`. Measured from the job log:

```text
getting and validating git state
build failed after 0s   error=git doesn't contain any tags - either add a tag or use --snapshot
```

`Build meta`, step 6, **succeeded** and exported `VERSION=0.0.0-dev.5140` — the version defect that
originally killed this job at step 6 is already gone; what remains is GoReleaser's own independent
requirement for a tag, which `--skip=validate` does not waive. The subsequent
`##[error]The template is not valid ... Error reading JToken` is collateral: the `Output images`
step `fromJSON`-parses bake metadata that the skipped build never produced.

Two changes bear on it. The `--snapshot` arguments described above address the GoReleaser failure
directly. The version fix changes what the job labels the artefacts with: `0.1.0-dev.0`, the
declared fork version, instead of `0.0.0-dev.5140`, a commit count that carries no intent.

### Can `Build containers` pass in this fork at all?

Nothing after GoReleaser needs a credential this fork lacks — stated as an analysis of the job
definition, not as an observation:

- `REGISTRY_PASSWORD` is `secrets.GITHUB_TOKEN`, always present, and the job requests
  `packages: write`.
- `PUSH` is `true` because the pull request's head and base repositories are the same. **This job
  really does publish images**, to `ghcr.io/nikolays/cloudnative-pg-testing`, under the repository
  owner's account. That is a consequence of making it pass, not a reason to weaken it, but it should
  be a deliberate choice: the alternative is to set `PUSH=false` for pull requests, which trades the
  registry write for losing the Dockle, cosign and image-matrix steps that only run when `PUSH` is
  true.
- `Run Snyk` self-gates on `SNYK_TOKEN` and skips without it. `Provenance for images` and
  `Create OLM bundle and catalog` are gated off by repository variable.

Unverified, and named rather than assumed: the two Dockle scans run with `exit-code: 1` and
`failure-threshold: WARN`, so a single warning on either image fails the job, and `cosign sign`
performs a keyless signature that has never run in this repository. Whether the job goes green after
`--snapshot`, or stops at one of those, has not been observed. It has deliberately **not** been made
to pass by disabling it, marking it `continue-on-error`, or narrowing its path filter.

## Consequences this change creates for other tracks

Stated here because they are real and this triage cannot close them.

- **The boundary manifest gets worse before it gets better.** `boundary.yaml` rule
  `upstream.workflows-to-disable` declares fourteen workflows at their `.github/workflows/` paths.
  This change moves them, so those declared paths no longer exist and sixteen new paths appear
  under `.github/workflows-upstream/`. That rule's own note already anticipates the move and
  prescribes the fix: change the rule to `disabled` with `mechanism: moved-aside` and repoint the
  paths. Note that `snyk.yml` is parked here but is not listed in that rule, so it needs adding.
  The manifest is not modified here because it belongs to another track.
- **`.wokeignore` was edited outside the stated file ownership, on purpose.**
  `.github/workflows/release-publish.yml` was listed there because it contains four occurrences of
  a term the `woke` rule set rejects. Moving the file would have de-ignored it and turned a
  currently-green check red. The entry is replaced with the parked directory
  `.github/workflows-upstream/`, which covers that file and any other parked file with the same
  problem. Measured: `grep -rniE '\bmaster\b'` over both workflow directories returns those four
  lines plus one line in `continuous-integration.yml`, which is ignored already.

## Residue: inherited CI problems this triage did not close

- The fork still has no tag of its own, so `0.1.0-dev.0` is a declared constant rather than a
  described one. It has to be raised by hand until CloudNativePatroni cuts its first `cnpatroni-v*`
  tag, and nothing in CI checks that it was. `pkg/versions/versions.go` remains at `1.30.0`, so a
  reader of `kubectl cnpg version` output sees two different numbers with two different meanings;
  reconciling them is an open question for the version track, not a defect of this fix.
- `Makefile` line 26 still computes `IMAGE_TAG` from `git symbolic-ref` with
  `git describe --tags --exact-match` as its fallback, which is the same tagless-repository trap one
  variable over. It is harmless today because every CI path sets `CONTROLLER_IMG` explicitly, and it
  was left alone rather than fixed opportunistically.
- `continuous-delivery.yml` is untouched. Its cloud e2e engines, its `smoke_test_release_branches`
  job and its `ok-to-merge` labelling all need credentials or branches the fork lacks. It produces
  no pull-request check, so it is noise on a schedule rather than a blocker.
- `registry-clean.yml` still targets upstream's image name and runs daily. It should become
  dispatch-only before the chaos work starts, so that it cannot delete a `-testing` digest a
  running cluster is pinned to.
- `refresh-licenses.yml` still uses `REPO_GHA_PAT`.
- `continuous-integration.yml` line 191 installs `renovate@latest` and line 415 pulls
  `ghcr.io/cloudnative-pg/docs:latest`. Both are unpinned tags, which the engineering rules forbid.
  They are inherited lines in inherited jobs, and the docs job is currently green; pinning them is
  named here rather than done, to keep this change reviewable.
- `Makefile` line 342 still lists `validate-threat-model` and `validate-security-insights` in the
  `checks` target, and both now validate files the project has deleted. `make checks` therefore
  fails locally for the same reason the deleted linter job failed in CI. The `checks` target belongs
  to the make-targets track, which already plans to remove both.
- No branch protection is configured, so none of these checks is required to merge anything.

## Verification performed in this session

- All 23 workflow files, in both directories, parse under `yq` and under `yaml.safe_load`.
  Measured.
- `continuous-integration.yml` has 18 jobs and no dangling `needs` reference after the two job
  deletions. Measured.
- The rewritten `Build meta` shell block is shellcheck-clean at `-S style` on the lines this change
  added. The remaining findings in that block are all on inherited lines and were left alone.
  Measured, with shellcheck 0.10.0.
- The `no-issue` label was read back from the API after creation, and the workflow it unblocks was
  observed passing. Measured.
- The parking, the two job deletions and the `Build meta` fix were observed in a real run on commit
  `7ea055d`, summarised above. Measured.
- The `--snapshot` change to GoReleaser has **not** been observed in a run. Whether `Build
  containers` goes green, or reaches a further failure, is unverified.
- The failing job log was read back before the version fix was written, not assumed: run
  `31346382565`, job `93329009987`, on commit `bbf4f6b`. `Build meta` succeeded there and
  `Run GoReleaser` failed. Measured.
- `make -s print-version` prints `0.1.0-dev.0` in this tagless checkout; `make -s print-version
  CNPATRONI_VERSION=9.9.9-test` prints `9.9.9-test`; `make -s print-version VERSION=1.2.3` prints
  `1.2.3`, which is the override `hack/setup-cluster.sh` uses. Measured.
- `make -n build` shows `buildVersion=0.1.0-dev.0` in the link flags for both `manager` and
  `kubectl-cnpg`, and `make -n docker-build` still passes `--snapshot` to GoReleaser. Measured.
- The `Makefile` line the workflow reads back is extracted by the same expression the workflow uses,
  `sed -n 's/^CNPATRONI_VERSION[[:space:]]*?=[[:space:]]*//p' Makefile | head -n 1`, which prints
  `0.1.0-dev.0`. Measured locally; not yet observed on a runner.
- `go build ./...` succeeds from the repository root after the change. Measured.
- No claim anywhere in this document asserts that a check passed on the strength of reasoning
  alone. Where a run was not observed, the text says so.
