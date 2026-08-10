# 0009. The fork tags `cnpatroni-v*` and declares its build version in the Makefile

- Status: Accepted. The `Makefile` and the `Build meta` step are changed and
  measured; the fork has cut no tag yet.
- Date: 2026-08-10 02:10:32 UTC
- Sources: [`ci-status.md`](../cnpatroni/ci-status.md), sections "Where the build
  version comes from" and "Residue";
  [`upstream-baseline.md`](../cnpatroni/upstream-baseline.md), section "Tag
  namespace"; `Makefile`;
  `.github/workflows/continuous-integration.yml`

## Context

The inherited `Makefile` line was
`VERSION := $(shell git describe --tags --match 'v*' | sed ...)`. It assumes the
repository carries upstream CloudNativePG release tags. This fork carries none:
it fetches `v*` tags from upstream and never pushes them, and it had not started
tagging itself.

`git describe` therefore exited 128, printed `fatal: No names found`, and left
`VERSION` empty — which emptied the `-ldflags` build version linked into
`manager` and `kubectl-cnpg`, the OLM bundle version, and the `buildVersion`
build argument passed to the container build. The same defect failed CI: under
`pipefail` the `Build meta` step failed the whole `Build containers` job before
anything was built, at every commit including `main` (`ci-status.md`).

An empty version does not fail loudly. It silently mislabels every artefact.

There is a second, independent reason not to reach for upstream's tags.
`upstream-baseline.md`: upstream's 125 `v*` tags are useful locally for
provenance and for `git describe`, but publishing them to this fork's origin
"would show CloudNativePG's releases under the CloudNativePatroni name, which
conflicts with this project's requirement to present an independent identity".

## Decision

**Tag namespace** (`upstream-baseline.md`):

- CloudNativePatroni's own release tags are prefixed `cnpatroni-v`.
- `git push --tags` to origin is forbidden; push explicit refs instead, for
  example `git push origin refs/tags/cnpatroni-v0.1.0`.
- Upstream `v*` tags stay local. Any already published to this fork's origin
  should be deleted there.

**Build version** (`ci-status.md`). The fork version is declared in exactly one
place, the `Makefile`:

```make
CNPATRONI_VERSION ?= 0.1.0-dev.0
CNPATRONI_GIT_VERSION := $(shell git describe --tags --match 'cnpatroni-v*' 2>/dev/null | sed ...)
VERSION := $(or $(CNPATRONI_GIT_VERSION),$(CNPATRONI_VERSION))
```

Three properties are deliberate:

1. **`VERSION` is never empty.** With no tag it is `0.1.0-dev.0`. Measured:
   `make -s print-version` prints `0.1.0-dev.0` in this tagless checkout, and
   `make -n build` shows `buildVersion=0.1.0-dev.0` in the link flags.
2. **The match is `cnpatroni-v*`, not `v*`.** Matching `v*` would describe an
   upstream CloudNativePG tag that happens to be reachable after a fetch, and
   label a CloudNativePatroni artefact with an upstream release number.
3. **`CNPATRONI_VERSION` is `?=`**, so a caller can override it, and a
   command-line `make VERSION=...` still overrides everything, which is what
   `hack/setup-cluster.sh` relies on.

`pkg/versions/versions.go` is **not** touched. `versions.Version` stays at
`1.30.0`, the inherited API and operand compatibility constant;
`CNPATRONI_VERSION` is the fork's own build version alongside it and only ever
reaches a binary through `-ldflags`.

The `Build meta` step in `continuous-integration.yml` follows the same order —
describe `cnpatroni-v*`, else read `CNPATRONI_VERSION` from the `Makefile` — so
the workflow and a local build agree on one value with one declaration.

## Consequences

One inherited line changed as a direct consequence: `docker-build` used an empty
`VERSION` as a proxy for "this repository has no tags" when deciding to pass
`--snapshot` to GoReleaser. `VERSION` is never empty now, so the switch keys off
`CNPATRONI_GIT_VERSION` instead, which is empty exactly when no fork tag can be
described.

A reader of `kubectl cnpg version` output now sees two different numbers with
two different meanings — `1.30.0` from `pkg/versions/versions.go` and the fork
build version. `ci-status.md` records reconciling them as "an open question for
the version track, not a defect of this fix".

`0.1.0-dev.0` is a declared constant rather than a described one. It has to be
raised by hand until CloudNativePatroni cuts its first `cnpatroni-v*` tag, and
nothing in CI checks that it was.

This decision is downstream of
[0005](0005-fork-base-deviates-from-the-tag.md): the tagless checkout is a
consequence of how the fork was created.

## Enforcement

| Mechanism | Path |
|---|---|
| Single declaration of the fork version | `Makefile`, `CNPATRONI_VERSION` |
| The same order in CI | `.github/workflows/continuous-integration.yml`, step `Build meta` |
| Tag-namespace rule | [`upstream-baseline.md`](../cnpatroni/upstream-baseline.md), by convention only |

## Known gaps

- **The tag-namespace rule has no automated enforcement.** Nothing in CI or in a
  hook prevents `git push --tags`. It is a documented rule that a human must
  follow.
- **`Build containers` has not been observed passing.** `ci-status.md`: the job
  now fails further along, at GoReleaser rather than at `Build meta`, and the
  `--snapshot` change addressing it "has **not** been observed in a run".
- **`Makefile` line 26 still computes `IMAGE_TAG` from `git symbolic-ref` with
  `git describe --tags --exact-match` as its fallback**, which is the same
  tagless-repository trap one variable over. It is harmless today because every
  CI path sets `CONTROLLER_IMG` explicitly, and was left alone rather than fixed
  opportunistically (`ci-status.md`, "Residue").
- **The annotated `cnpatroni-fork-base` tag does not exist**; see
  [0005](0005-fork-base-deviates-from-the-tag.md).
