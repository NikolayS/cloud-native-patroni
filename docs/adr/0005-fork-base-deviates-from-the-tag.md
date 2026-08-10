# 0005. The fork base is upstream `main` at `b226821`, not the `v1.30.0` tag

- Status: **Open.** `CLAUDE.md` rule 4 records the deviation as "open with the
  project owner". This record does not close it, and nothing here should be read
  as closing it.
- Date: 2026-08-10 02:10:32 UTC
- Sources: [`CLAUDE.md`](../../CLAUDE.md) preamble and rule 4;
  [`upstream-baseline.md`](../cnpatroni/upstream-baseline.md);
  `hack/cnpatroni/upstream/upstream-baseline.yaml`;
  [`NOTICE`](../../NOTICE) section 1

## Context

`CLAUDE.md` rule 4 quotes the specification: "The first fork base is the
`v1.30.0` tag. Do not begin from a moving upstream commit."

This repository does not satisfy that. Its base is
`b22682115c5f3ad1bfe69bed719151d0db918e39` ("chore: update osps baseline version
(#11298)", 2026-08-06), a commit on upstream `main`. `upstream-baseline.md`
records the measurements: `git describe` gives `v1.30.0-69-gb22682115`; the
nearest upstream tag `v1.30.0` is `4b5e244a7d031f67e025c83c1555e7726ecbbfa1`;
the distance is 69 commits ahead and 0 behind; the diff across that range is 195
files changed, 7,718 insertions and 1,090 deletions. There are no tags in a
fresh checkout of this fork.

`pkg/versions/versions.go` still reports `1.30.0`, because upstream raises that
constant at the next release (`CLAUDE.md` preamble; `NOTICE` section 1). The
derivation point is the commit, not that release.

## Decision

The deviation is **recorded, not corrected**, and remains open with the project
owner. Concretely, from `CLAUDE.md` rule 4:

1. Do not describe the base as the `v1.30.0` tag anywhere.
2. Do not rebase the fork onto the tag without the owner's decision, because
   that would discard inherited upstream work already present here.

The honest phrasing, which `upstream-baseline.md` and `fork-maintenance.md`
require of the README and the release notes, is "based on the CloudNativePG 1.30
pre-release trunk (`v1.30.0-69-gb22682115`)", never "based on CloudNativePG
1.30.0".

## Consequences

`upstream-baseline.md` states four costs, and this record carries them
unchanged:

1. **The fork cannot be described as based on CloudNativePG 1.30.0.** It carries
   69 commits that no released 1.30.x patch version contains, so anyone
   comparing CloudNativePatroni's behaviour against a released 1.30.x binary is
   comparing against a different tree.
2. **Security-advisory mapping is less direct.** "Does CloudNativePatroni have
   the fix released in 1.30.4?" cannot be answered by comparing version numbers;
   it is answered by ancestry, `git merge-base --is-ancestor <fix-commit> HEAD`.
   The compatibility definition in `fork-maintenance.md` is written in exactly
   those terms for this reason.
3. **Those 69 commits were never reviewed as a fork decision.** They arrived
   with the initial clone rather than through an integration pull request. Of
   the 195 files they changed, 25 fall on the declared boundary and 19 of those
   carry high-availability authority. They are recorded as integrated because
   the fork is byte-identical to upstream at the base, not because anyone read
   them. The range to re-read, if a later audit finds a problem in that window,
   is `v1.30.0..b226821`.
4. **The version pipeline broke as a side effect.** A fresh clone has no tags,
   so `git describe` fails and the build stamped an empty version. That is
   addressed separately by [0009](0009-fork-tag-namespace-and-build-version.md).

One property limits the damage: `v1.30.0` is a strict ancestor of the fork base
and the fork is 0 commits behind it, so nothing from the 1.30.0 release is
missing. The fork carries more than the release, not less.

`fork_base.commit` in `hack/cnpatroni/upstream/upstream-baseline.yaml` never
changes for the life of the fork. `last_integrated.commit` moves forward once
per integration, in a reviewed commit.

## Enforcement

- `hack/cnpatroni/upstream/upstream-baseline.yaml` is the machine-readable
  record, and `cnpatroni-upstream validate` fails with exit code 4 if either
  commit it names is not an ancestor of `HEAD`, so the baseline cannot claim an
  integration that did not happen (`fork-maintenance.md`, check V11).
- `NOTICE` section 1 states the derivation point as the commit, for
  Apache-2.0 section 4(b).
- The audit workflow pins the bootstrap base to
  `b22682115c5f3ad1bfe69bed719151d0db918e39`
  (`.github/workflows/cnpatroni-authority-audit.yml`).

## Known gaps

- **The repository contradicts itself on whether this is open.**
  `CLAUDE.md` rule 4 says "The deviation is open with the project owner".
  `upstream-baseline.md` presents it as settled: "Correcting the specification
  is the cheaper and safer of the two, so the specification is amended here
  rather than obeyed by rewriting." `CLAUDE.md` is the binding instruction file
  and is listed in `boundary.yaml` as "Binding contributor instructions", so
  this record follows it and keeps the status Open. The two pages should be
  reconciled by whoever owns the specification.
- **The two pages cite different specification sections for the same
  requirement.** `CLAUDE.md` rule 4 attributes it to section 20.1;
  `upstream-baseline.md` attributes it to section 6.1. The specification is not
  in this repository, so which is right cannot be determined here.
- **The durable fork-point tag does not exist.** `upstream-baseline.md` gives
  the exact `git tag -a cnpatroni-fork-base ...` command and states that the tag
  "has not been created yet", because creating it mutates the shared object
  store and is a deliberate maintainer action.
