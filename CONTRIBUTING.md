# Contributing to CloudNativePatroni

CloudNativePatroni is a pre-alpha architecture spike, not released software.
Contributions are welcome, but the project is small, the architecture is still
being decided, and review capacity is limited. Before writing code, read
[`CLAUDE.md`](CLAUDE.md) — it is written for agent sessions but it is the
shortest accurate statement of the rules that apply to everyone.

## Before you open a pull request

1. Read [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).
2. Read the single-authority rule in [`CLAUDE.md`](CLAUDE.md). Any change that
   lets a component other than Patroni start, stop, promote, demote, or
   reconfigure Postgres for a high-availability decision is rejected, however
   small the diff.
3. Read [`docs/cnpatroni/naming-policy.md`](docs/cnpatroni/naming-policy.md)
   before introducing any new label, annotation, metric, environment variable,
   container name, or Postgres identifier.
4. For build, test, and local environment mechanics, use the inherited developer
   documentation in [`contribute/README.md`](contribute/README.md). It is
   CloudNativePG's, it is still largely accurate for this fork, and where it is
   not, say so in your pull request.

## What is currently out of bounds

Milestone M0 is fork hygiene, the high-availability authority audit, and
ADR-002. Until ADR-002 is accepted:

- Do not modify inherited Go code that carries high-availability or
  process-lifecycle authority. The audit has to describe it before it is
  changed.
- Do not perform mechanical `cnpg` to `cnpatroni` renames. The specification
  forbids broad renames until the architecture is stable, because they make
  every subsequent upstream diff unreadable. New identifiers use `cnpatroni`
  immediately; existing ones wait.
- Do not add badges, affiliations, adopter claims, or support channels that do
  not exist.

## Commits and pull requests

- Conventional Commits. Subject under 50 characters, present tense, body wrapped
  at 72 characters. A scope is encouraged: `feat(patroni):`, `fix(instance):`,
  `docs(adr):`, `ci(fork-sync):`.
- Breaking changes use `!` after the type and a `BREAKING CHANGE:` footer.
- The pull-request title matches the main commit subject.
- No emoji in commit messages, code comments, or documentation.
- Sign off every commit with `git commit -s`. This certifies the
  [Developer Certificate of Origin](contribute/developer-certificate-of-origin),
  which this project keeps from CloudNativePG: provenance matters more in a fork
  than anywhere else.
- Do not use `git commit --amend`, and do not force push, without explicit
  agreement in the pull request. Squashing before the first push of a branch is
  fine.

## Code

- Tests first. Write the failing test, see it fail, then write the code. Cover
  negative cases, boundary values, and error paths, not only the happy path.
- Surgical changes. Do not reformat or improve adjacent inherited code, and do
  not create files that are not necessary. A concentrated diff is what keeps the
  fork mergeable with upstream (specification section 4.4).
- Every new Go file carries the Apache-2.0 header used by its neighbours,
  including the CloudNativePG copyright line, which Apache-2.0 requires derived
  work to retain.
- Shell scripts: `#!/usr/bin/env bash`, `set -Eeuo pipefail`, `IFS=$'\n\t'`,
  quoted expansions, `main "$@"` as the last line, and shellcheck-clean at
  `-S style`.
- Never reference a `latest` image tag. Pin versions, and digests where the
  specification requires them.
- Documentation uses sentence-case headings, spaced em dashes, absolute
  timestamps in `YYYY-MM-DD HH:mm:ss UTC`, and KiB/MiB/GiB in prose.

## Writing about CloudNativePG

This project exists because it disagrees with one architectural decision in
CloudNativePG. Describe that as a design difference and state the trade-offs
honestly. Do not disparage CloudNativePG, its maintainers, or any other project.
Attribute without implying endorsement.

> CloudNativePatroni is an independent project derived from CloudNativePG. It is not affiliated
> with or endorsed by CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.
