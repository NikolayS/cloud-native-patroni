# The CloudNativePatroni fork baseline

This document records the exact point in upstream CloudNativePG's history that
CloudNativePatroni was forked from, why that point is not the one the
specification names, and what that costs.

The machine-readable form of the same record is
`hack/cnpatroni/upstream/upstream-baseline.yaml`. That file is validated
against git on every run, so it cannot claim an integration that did not happen.

CloudNativePatroni is an independent project derived from CloudNativePG. It is
not affiliated with or endorsed by CloudNativePG, CNCF, LF Projects, or the
Patroni maintainers.

## The baseline

| Field | Value |
|---|---|
| Upstream repository | `https://github.com/cloudnative-pg/cloudnative-pg` |
| Tracked branch | `main` |
| Fork base commit | `b22682115c5f3ad1bfe69bed719151d0db918e39` |
| `git describe` | `v1.30.0-69-gb22682115` |
| Fork base date | 2026-08-06 |
| Nearest upstream tag | `v1.30.0` = `4b5e244a7d031f67e025c83c1555e7726ecbbfa1` |
| Distance from that tag | 69 commits ahead, 0 behind |
| Last integrated commit | `b22682115c5f3ad1bfe69bed719151d0db918e39` (the fork is byte-identical to upstream at the base) |

The distance was measured on a full clone of upstream `main`: 69 commits,
195 files changed, 7,718 insertions and 1,090 deletions between the `v1.30.0`
tag and the fork base. Those numbers are reproduced by
`cnpatroni-upstream report --from v1.30.0 --to b226821`.

`main` is the branch this fork tracks, not a release branch. Upstream backports
nearly everything from `main` onto `release-1.28`, `release-1.29` and
`release-1.30`, so `main` is the superset; the release branches are fetched for
provenance queries and never merged.

## The deviation from the specification

Specification section 6.1 mandates that the fork be based on the `v1.30.0` tag.
It is not. It is based on upstream `main` at `b226821`, which is `v1.30.0` plus
69 upstream commits.

**Why the deviation stands rather than being corrected.** The fork already
exists at `b226821`, and that commit is already published. Moving the baseline
onto the tag would mean rewriting history that other people may have fetched.
The project's git rules forbid rewriting published history and forbid force
pushes, and there is no way to reconcile the two requirements without breaking
one of them. Correcting the specification is the cheaper and safer of the two,
so the specification is amended here rather than obeyed by rewriting.

The deviation is benign in one important respect: `v1.30.0` is a strict ancestor
of the fork base, and the fork is 0 commits behind it. Nothing from the 1.30.0
release is missing; the fork simply carries more than the release does.

### What it costs

1. **The fork cannot be described as "based on CloudNativePG 1.30.0."** It
   carries 69 commits that no released 1.30.x patch version contains. Anyone
   comparing CloudNativePatroni's behaviour against a released CloudNativePG
   1.30.x binary is comparing against a different tree. The honest phrasing,
   which the README and the release notes must use, is "based on the
   CloudNativePG 1.30 pre-release trunk (`v1.30.0-69-gb22682115`)".

2. **Security-advisory mapping is less direct.** When upstream publishes a fix
   "in 1.30.4", the question "does CloudNativePatroni have it?" cannot be
   answered by comparing version numbers. It has to be answered by ancestry:
   `git merge-base --is-ancestor <fix-commit> HEAD`. The tooling's
   compatibility definition is written in exactly those terms for this reason.

3. **Those 69 commits were never reviewed as a fork decision.** They arrived
   with the initial clone rather than through an integration pull request. Of
   the 195 files they changed, 25 fall on the boundary this project declares,
   and 19 of those carry high-availability authority — the first integration
   report ever produced by this tooling. They are recorded as integrated because
   the fork is byte-identical to upstream at the base, not because anyone read
   them. If a later audit finds a problem in that window, the range to re-read
   is `v1.30.0..b226821`.

4. **The version pipeline in this fork is currently broken.** A fresh clone has
   no tags, so `git describe` fails and the build stamps an empty version. That
   is a consequence of how the fork was created, not of the deviation itself,
   but it is fixed by the same action: run `cnpatroni-upstream setup`, which
   fetches the 125 upstream version tags.

### What is not changed by this

The immutable fork base. `fork_base.commit` in the baseline file never changes
for the life of the fork. `last_integrated.commit` moves forward, once per
integration, in a reviewed commit.

## Recording the fork point durably

The fork base should also carry an annotated tag, so that it survives even if
the baseline file is edited carelessly:

```bash
git tag -a cnpatroni-fork-base b22682115c5f3ad1bfe69bed719151d0db918e39 \
  -m "CloudNativePatroni fork base: upstream/main b226821 = v1.30.0-69-gb22682115 (2026-08-06)"
git push origin refs/tags/cnpatroni-fork-base
```

That tag has not been created yet; creating it mutates the shared object store
and is a deliberate maintainer action, not something the tooling does.

## Tag namespace

Upstream's 125 `v*` tags are useful locally, for provenance and for `git
describe`. They must not be pushed to this fork's origin: publishing them would
show CloudNativePG's releases under the CloudNativePatroni name, which conflicts
with this project's requirement to present an independent identity.

The rules are therefore:

- CloudNativePatroni's own release tags are prefixed `cnpatroni-v`.
- `git push --tags` to origin is forbidden. Push explicit refs instead:
  `git push origin refs/tags/cnpatroni-v0.1.0`.
- Upstream `v*` tags stay local. Any that have already been published to this
  fork's origin should be deleted there.

## Verifying the baseline

Every claim on this page is checkable from a clone that has been through
`cnpatroni-upstream setup`:

```bash
git rev-parse HEAD                                  # b22682115c5f3ad1bfe69bed719151d0db918e39
git describe --tags HEAD                            # v1.30.0-69-gb22682115
git merge-base --is-ancestor v1.30.0 HEAD           # exit 0
git rev-list --left-right --count v1.30.0...HEAD    # 0    69
git diff --shortstat v1.30.0 HEAD                   # 195 files changed, 7718 insertions(+), 1090 deletions(-)
```

The tooling asserts the first, third and a stronger form of the second on every
run: `validate` fails with exit code 4 if either commit recorded in the baseline
is not an ancestor of `HEAD`.
