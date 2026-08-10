# Continuous integration status

This document records which checks CloudNativePatroni runs, why some checks are skipped, and what
has to exist before dormant publication and delivery paths can be enabled.

## What a green board proves

There is no Kubernetes cluster in these checks. No container runtime runs the operator, and no
operator image is published. The container contract workflow builds the production database image
and exercises a single container running Patroni and Postgres; it does not form a Patroni cluster.
No CI job exercises failover or a network partition, and no job injects a fault into the fencing
path. Since a working Patroni prevents a concurrent timeline fork by construction, the evidence
that would matter is entirely in the fencing-failure cases, and none of it exists yet.

A fully green board is therefore not evidence that the operator works, that Patroni holds
high-availability authority, or that any cluster behaves correctly. It is evidence only that the
code compiles, the unit tests pass, the generated artefacts match their sources, the documentation
builds, the fork's boundary, authority, and code-hygiene gates are satisfied, and the production
database image satisfies eight container contracts and the harness self-test on `linux/amd64`.

The three fork-owned gate workflows now run on every push to every branch. Previously, a push to a
branch outside their filters was unguarded unless an open pull request supplied a `pull_request`
run. The fork's authority and process-lifecycle claims depend on these gates, so filtering branch
names would make a cost saving into a correctness risk.

The `divergence-report` job is different: it runs only on the weekly schedule and
`workflow_dispatch`. Skipping it during ordinary development is correct for its purpose, but also
means ordinary development never exercises it. Unless someone dispatches it manually, a break in
the job surfaces on the weekly schedule or not at all.

The authority-audit and container-contract workflows set `cancel-in-progress: true`, so a rapid
second push to the same ref cancels the first run. The most recent run being green therefore does
not establish that a given commit was verified; a green can belong to a superseded commit. The
upstream-sync workflow cancels only `pull_request` runs, not push runs.

The end-to-end suite would provision Kubernetes clusters and exercise a running operator and its
managed clusters. That would provide behavioural evidence for the scenarios it covers. The suite
lives in `continuous-delivery.yml` and never runs for a pull request. Its requested runs enter
through `issue_comment` and `workflow_dispatch`; the inherited workflow also declares a daily
schedule. Every path needs cloud credentials this fork does not hold and an operator image this
fork does not publish, so the suite does not run successfully here.

## Open container-exit question

`no-postgres-survives-container-exit` records the container's host PIDs, sends SIGKILL to the
container, and asserts that none of those PIDs and no process matching the test cluster survives.
From a clean start with nothing else running, it passes on `linux/amd64` and fails reproducibly on
`linux/arm64` at its 30-second deadline. The processes do eventually exit; the cause is not
established.

Neither local result is authoritative. Both were measured on macOS through the `desktop-linux`
Docker context, whose server reports Docker Desktop with kernel `6.12.54-linuxkit`. Docker runs
inside that LinuxKit virtual machine: `--pid host` sweeps the virtual machine's PID namespace, not
the macOS host's, and container teardown crosses a virtualisation layer absent from a plain Linux
host. The arm64 failure may be teardown lag in that virtual machine, while the amd64 pass may be
luck under emulation.

The container-contract CI job is consequently the first measurement of this property on a
representative host. A consistent pass there is better evidence than either local platform can
produce. A failure there matters more than a local flake: it would mean Postgres can outlive its
container on an ordinary Linux host. If the container is reported stopped while its postmaster
lingers and Kubernetes starts a replacement, the lingering process is an unsupervised writable
postmaster.

This question is open, not resolved. A green board must not be read as closing it.

## Code hygiene gate

The `Check CloudNativePatroni code hygiene` job gives its three analyses distinct scopes.
Duplication scans non-test Go files in the declared scan directories: `hack/cnpatroni/upstream/`,
`hack/cnpatroni/audit/`, `internal/cnpatroni/`, `poc/oracle/`, and `poc/chaos/`. It excludes
`testdata/` and vendored code. Dead-code analysis does not use that directory union as its call
graph. Its effective scope is the packages reachable from each declared executable root:
`./cmd/...` in the upstream-boundary module, `.` in the audit module, the filtered test executable
for `internal/cnpatroni/`, and `./cmd/...` in both PoC modules. The orphan-package check examines
packages containing scanned non-test files and rejects a non-`main` package that nothing in its
module imports. A package's own test file exempts it only when its scan unit is analysed with
`-test`; `internal/cnpatroni` is the only such unit today.

The job rejects every unreachable function that `deadcode` reports from those roots; it does not
claim that every function in every scan directory is independently rooted and checked. In
particular, `internal/cnpatroni` is analysed with `-test`, so a function reached only from a test
counts as reachable. This deliberate M0 compromise exists because `internal/cnpatroni/guard` is
production-dead by design — nothing in the repository calls it yet. Revisit it at M1, when the
production call sites land and a production executable root becomes viable.

A coverage cross-check runs before the analyses. It loads
`hack/cnpatroni/upstream/boundary.yaml` with the boundary package's parser and matcher, selects Go
files whose matching rule has `ownership: cnpatroni-owned`, and also treats a file as fork-owned
when its nearest `go.mod` is neither the inherited operator module nor the `tests/` module. A file
selected by either signal must have declared coverage through a scan directory or an explicit
coverage exclusion. The check treats a manifest that classifies no Go file as `cnpatroni-owned` as
a misconfiguration. In the other direction, every scanned non-test Go file must be classified as
`cnpatroni-owned` by the boundary manifest, so a disagreement between the scan-unit table and the
manifest also fails and names the files. Candidate ownership includes test files but excludes
`testdata/` and vendored code. Cover a new tree by adding its module, directory, and appropriate
dead-code roots to the scan-unit table in `hack/cnpatroni/check-code-hygiene.sh`. If code genuinely
should not be scanned, it instead needs an explicit coverage exclusion there with a written
reason; there are currently no exclusions.

The gate derives the files it must cover from the ownership classes in
`hack/cnpatroni/upstream/boundary.yaml`, so it trusts that manifest. The
`ownership-ratchet` step compares every `cnpatroni-owned` and `disabled` rule — its id,
class and path patterns — with the manifest as it stood at the merge base. A rule may gain paths
freely, but it may not lose paths, change class or disappear without an
`ownership_ratchet_allow` entry carrying a reason; that acknowledgement appears in the same diff
as the narrowing. Comparison is on literal patterns, so replacing several patterns with one
broader pattern also needs an acknowledgement. The code-hygiene job must be a required status
check on `main`, otherwise the ratchet is advisory. The fork-owned Go file count in the coverage
cross-check's output is informational; it does not ratchet or guard coverage.

Declared coverage is not proof that every analysis reads every counted file. A nested Go module
inside a declared scan directory is prefix-matched as covered, but `go list ./...` in the parent
module does not descend into it, so dead-code and orphan-package analysis skip it. Duplication
still scans it because file collection is `find`-based. The `poc/` scan directories already have
this nested-module shape, so this is a live gap. The declared-coverage count also includes
`_test.go` files; duplication excludes them, and dead-code reads tests only for the unit analysed
with `-test`. A fork-authored tree inside the inherited root operator module, at a path no
`cnpatroni-owned` rule enumerates, is matched by the manifest's trailing `**` catch-all and reads as
upstream code, so neither ownership signal covers it. Boundary drift validation rejects that
landing as D1. Declaring the new path `adapted` does not evade the gate: D3 resolves the rule to
each concrete changed path and proves that the path existed at the recorded fork base. A new path
must instead be classified `cnpatroni-owned` for the hygiene coverage cross-check to accept it.

There is no dead-code or duplication baseline file or allowlist. When the gate fires, use the paths
and line numbers in its output to remove unreachable code or extract the duplicated behaviour into
one implementation. The legitimate escape is to make the code no longer duplicated, not to raise
the threshold or add an analysis exclusion.

## Active workflow disposition

Exactly nine workflow files remain active under `.github/workflows/`.

| File | Disposition |
|---|---|
| `continuous-integration.yml` | Runs on pull requests and is active and required. GoReleaser now uses `--snapshot` because the fork has no tags. Publication is gated by the `ENABLE_IMAGE_PUSH` repository variable. The gate is event-agnostic: while the variable is unset, it disables publication for pull requests, pushes to `main`, and the nightly schedule alike. The image build still builds the `distroless` and `ubi` targets for `linux/amd64` and `linux/arm64` on pull requests that change operator, test, shell-script, or Go code, per the `change-triage` gate; documentation-only pull requests skip `buildx` entirely. |
| `codeql-analysis.yml` | Active and passing. It was briefly red for an unrelated reason: a deliberately malformed Go test fixture under `hack/cnpatroni/audit/testdata/` broke `make generate` during the CodeQL build step. Commit `20e49a0e` fixed the fixture, after which run `31346382287` concluded `success`. |
| `spellcheck.yml` | Active and passing. It provides the `Run spellcheck` and `Run woke` checks. Its spellcheck sources cover `docs/src/` Markdown and `config/olm-manifests/bases/*.yaml`, so `docs/cnpatroni/` is not spellchecked; woke covers the wider tree according to its own configuration. |
| `cnpatroni-authority-audit.yml` | Fork-owned, active, and passing. Its authority and code-hygiene jobs run on every pull request and every push to any branch. |
| `cnpatroni-contract-tests.yml` | Fork-owned and active. Its job runs on every pull request and every push to any branch, builds the production image, and runs the container contracts on `linux/amd64`. It is the only CI job that runs a Patroni process at all. |
| `cnpatroni-upstream-sync.yml` | Fork-owned and contains three jobs. `boundary-guard` runs on every pull request and every push to any branch, and is passing. `divergence-report` runs only on the weekly schedule and `workflow_dispatch`, so its pull-request skip is correct. |
| `continuous-delivery.yml` | Never runs on pull requests. Requested runs use `issue_comment` or `workflow_dispatch`; the inherited file also has a daily schedule. It provisions clusters and structurally needs cloud credentials, a Kubernetes cluster, and a published operator image, none of which exist here. It remains unmodified and dormant for this fork. |
| `registry-clean.yml` | Runs on a daily schedule and through `workflow_dispatch`. It prunes the `cloudnative-pg-testing` package and, only when the repository owner is `cloudnative-pg`, the `pgbouncer-testing`, `postgresql-testing`, and `postgis-testing` operand packages. With `ENABLE_IMAGE_PUSH` unset, this fork publishes nothing, so there is nothing to prune. It remains in place and dormant in effect. |
| `refresh-licenses.yml` | Runs weekly and through `workflow_dispatch`. It does not run on pull requests and remains in place. |

## Checks that correctly report as skipped

These skips are conditional behaviour or explicit fork policy, not broken checks.

- `Run shellcheck linter` is gated by `change-triage` on changes to `**/*.sh`. This change set
  touches no shell script, so the skip is correct. Run `git diff --name-only origin/main...HEAD` to
  check the changed paths. It will run as soon as a shell script changes.

- `Renovate Linter` is gated on `.github/renovate.json5`, which this pull request does not change.

- `Report the divergence from upstream CloudNativePG` runs only on the weekly schedule and
  `workflow_dispatch`, by design.

- `Create OLM bundle and catalog`, `Run openshift-preflight test`, `Run OLM scorecard test`, and
  `Run OLM ${{ matrix.test }} test` are gated on the `ENABLE_OLM` repository variable. This is
  structural to the fork: the bundle carries upstream's OperatorHub package name, channels, and
  maintainer metadata, while publication needs registry and Red Hat credentials the fork does not
  hold.

- `Provenance for images` is gated on the `ENABLE_SLSA_PROVENANCE` repository variable. It attests
  a published image, and this fork publishes none.

- `Output images`, `Dockle scan distroless image`, `Dockle scan UBI image`, `Sign images`, and
  `Prepare images matrix` are steps in `buildx` guarded by `env.PUSH == 'true'`. They skip while
  `ENABLE_IMAGE_PUSH` is unset. Setting that repository variable to `true` re-enables the image
  push together with both Dockle scans, cosign signing, and the images matrix.

- `Run Snyk to check Docker image for vulnerabilities` is guarded by `env.SNYK_TOKEN != ''`. This
  fork has no `SNYK_TOKEN` secret, so the skip is correct. `SNYK_TOKEN` must not be added while
  `ENABLE_IMAGE_PUSH` is unset, because `CONTROLLER_IMG` is populated only when the push is enabled.

## Parked upstream workflows

The 15 upstream workflow files and their directory README remain under
`.github/workflows-upstream/`. GitHub Actions does not run files from that directory. They are
parked instead of deleted to preserve an easy upstream merge path and possible starting points for
a future fork-owned release process. See
[`workflows-upstream/README.md`](../../.github/workflows-upstream/README.md) for the directory
policy.

Each row's reason is also its return condition: a parked file can return to `.github/workflows/`
once the branches, labels, secrets, credentials, or downstream repositories it names exist for
this fork, and not before.

| File | Why it cannot run in this fork |
|---|---|
| `backport.yml` | Targets upstream release branches and labels that do not exist here, and needs an upstream-style repository token. |
| `chatops.yml` | Implements upstream issue-comment commands and approval labels and needs an upstream-style repository token. |
| `close-inactive-issues.yml` | Encodes upstream issue-governance policy, for an issue process this fork has not established. |
| `k8s-versions-check.yml` | Updates upstream Kubernetes-version inputs and opens pull requests with credentials and cloud-engine assumptions this fork does not have. |
| `latest-postgres-version-check.yml` | Tracks the upstream operand registry rather than a fork-owned Patroni operand image and needs credentials to open pull requests. |
| `osps_security_assessment.yml` | Publishes an upstream security assessment and requires `PVTR_OSPS_TOKEN`. |
| `ossf_scorecard.yml` | Publishes supply-chain results to the OpenSSF REST API on behalf of the upstream repository and assumes upstream's default branch and branch-protection configuration. |
| `pr_verify_linked_issue.yml` | Requires upstream's linked-issue policy, but this fork has not established that issue-governance process. |
| `release-pr.yml` | Opens upstream-style release pull requests and requires release branches, version policy, and credentials this fork has not established. |
| `release-publish.yml` | Implements upstream's signed release, image, provenance, OLM, and OperatorHub publication process and needs its signing and publication credentials. |
| `release-tag.yml` | Creates upstream-style `v*` tags and dispatches to an upstream repository the fork does not own. |
| `require-labels.yml` | Requires upstream review labels that do not exist in this single-owner fork. |
| `snyk.yml` | Requires the `SNYK_TOKEN` secret, which this fork does not hold. |
| `sync-api.yml` | Dispatches to the upstream API repository; there is no fork-owned API repository to receive it. |
| `sync-docs.yml` | Dispatches to the upstream documentation repository; there is no fork-owned documentation repository to receive it. |
| `README.md` | Documents why this directory is inert and why the files were moved rather than deleted. |

## Why the upstream tag was not copied

Pushing upstream's `v1.30.0` tag into the fork was considered as a way to satisfy GoReleaser and
rejected. It would give the fork a version identity it has not decided on and change what
`git describe` reports for every future build. The workflow's `Build meta` step already computes a
version without a tag. Adding `--snapshot` is the narrower change.

## Re-enabling publication and delivery paths

These controls are GitHub Actions repository variables set under the repository's Settings. None
of them is currently set.

- `ENABLE_IMAGE_PUSH=true` enables the operator image push, the two Dockle scans, cosign signing,
  and the images matrix. Enable it only after the fork has an explicit image publication policy,
  a stable registry destination, and a signing identity. `SNYK_TOKEN` must not be added while
  `ENABLE_IMAGE_PUSH` is unset, because `CONTROLLER_IMG` is populated only when the push is enabled.

- `ENABLE_OLM=true` enables the OLM bundle and catalog plus the downstream preflight, scorecard,
  and OLM tests. Enable it only after replacing upstream package, channel, and maintainer metadata
  and supplying the required registry and Red Hat credentials.

- `ENABLE_SLSA_PROVENANCE=true` enables provenance for published images. Enable it only when image
  publication is enabled and the fork has a release policy and signing identity for those
  attestations.
