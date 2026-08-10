# CloudNativePatroni — working rules for agent sessions

A fork of CloudNativePG that replaces the inherited PostgreSQL
high-availability and process-control layer with upstream Patroni. The fork is
derived from CloudNativePG at commit `b226821` (2026-08-06), which is
post-1.30.0 development on upstream `main`; `pkg/versions/versions.go` still
reports version `1.30.0` because upstream raises that constant at the next
release. See rule 4 for the recorded deviation from specification section 20.1.
Read this file before changing anything.

Before proposing or making an architectural change, read the architecture spike
specification and the owner directives. Both are maintained outside this
repository; if they are not in your context, ask for them rather than inferring
the architecture from the code. Section numbers below refer to that
specification.

## Rule 1 — single high-availability authority (non-negotiable)

At all times exactly one component decides whether PostgreSQL may be writable:
**Patroni**. Specification section 4.1, verbatim:

```text
For every instance and every time t:

PostgreSQL may be primary and accept writes
only if local Patroni considers itself primary
and holds valid write authority under Patroni's DCS rules.
```

The operator, the integration agent, kubelet, the container runtime, and any
minimal init may observe state or remove availability by terminating a process
or container. They must never grant write authority, promote PostgreSQL, or
restart PostgreSQL independently of Patroni. Nothing outside Patroni may run
`pg_ctl promote` or call `pg_promote()`, start/stop/restart PostgreSQL for a
high-availability decision, create or remove `standby.signal` or
`recovery.signal`, write `primary_conninfo`, `primary_slot_name`, or
`synchronous_standby_names`, run `pg_rewind`, select a promotion candidate,
acquire the inherited primary Lease, or set `TargetPrimary`. See specification
section 7.4.

Do not add a second controller "temporarily". Any change that reintroduces a
competing decision-maker is rejected regardless of how small the diff is.

## Rule 2 — identifiers, and no broad rename yet

- New identifiers that CloudNativePatroni introduces from scratch use
  `cnpatroni` (or `cnpatroni.io`, `CNPATRONI_`, `cnpatroni_`) immediately.
- Existing `cnpg` / `cloudnative-pg` identifiers are **not** renamed yet.
  Specification section 20.2 forbids broad mechanical renames until the
  architecture is stable, because they obscure the diff against upstream; the
  compatibility sequencing is in section 6.2.
- The full policy, the frozen surfaces, and the rename inventory are in
  [`docs/cnpatroni/naming-policy.md`](docs/cnpatroni/naming-policy.md). Read it
  before adding any label, annotation, metric, env var, or container name.
- Identifiers Patroni owns are never written by operator reconcilers.

## Rule 3 — engineering rules

CloudNativePatroni uses the following engineering rules as its own conventions:

- Sentence-case headings; no emoji anywhere, including code comments; em dashes
  are spaced (`word — word`); absolute timestamps as `YYYY-MM-DD HH:mm:ss UTC`;
  KiB/MiB/GiB in prose, while Postgres and Kubernetes config values keep their
  own literal formats. Never `latest` image tags — pin versions, and digests
  where the specification requires them.
- Tests first. Write the failing test before the code, and cover negative,
  boundary, and error paths — not only the happy path.
- Surgical changes only. Do not reformat or improve adjacent inherited code, and
  do not create files that are not necessary.
- Shell: `#!/usr/bin/env bash`, `set -Eeuo pipefail`, `IFS=$'\n\t'`, quoted
  expansions, `main "$@"` last, shellcheck-clean at `-S style`.

Where these meet inherited CloudNativePG conventions:

| Question | Which wins |
|---|---|
| Heading case in files we author or rewrite | CloudNativePatroni: sentence case. Do not mass-rewrite inherited headings. |
| "Postgres" vs "PostgreSQL" in new prose | CloudNativePatroni: Postgres. API identifiers, CRD groups, field names, and quoted text stay as they are. |
| Commit subject length, present tense, no `--amend`, no force push | CloudNativePatroni. |
| DCO `Signed-off-by` on every commit | CloudNativePG. It is additive, so keep `git commit -s`. |
| Apache-2.0 headers on every Go and shell file | CloudNativePG and specification section 20.3. Copy the header from a neighbouring file, keeping the CloudNativePG copyright line intact. |
| Emoji in push or pull-request summaries | No emoji. The professional-communication rule has an explicit precedence clause. |

Never send `.claude/`, `.cursor/`, or `CLAUDE.md` in a patch aimed upstream.

## Rule 4 — branches and upstream

Specification section 20.1, verbatim:

```text
upstream/main          CloudNativePG upstream
upstream/release-*     Upstream release branches/tags
origin/main            CNPatroni main
origin/spike/patroni   Architecture spike integration branch
```

That block is quoted as written, including the specification's working name
`CNPatroni`; the project name is CloudNativePatroni. The same section requires:
"The first fork base is the `v1.30.0` tag. Do not begin from a moving upstream
commit."

**Recorded deviation.** This repository does not satisfy that requirement. Its
base is commit `b226821` ("chore: update osps baseline version (#11298)",
2026-08-06), a `main` commit carrying roughly five weeks of upstream development
after the 1.30.0 release of 2026-06-29. There are no tags in this checkout. The
deviation is open with the project owner; do not describe the base as the
`v1.30.0` tag anywhere, and do not rebase the fork onto the tag without the
owner's decision, because that would discard inherited upstream work already
present here.

Upstream changes are merged through the integration branch, reviewed, then
merged into main; preserve upstream commits and copyright notices. Re-run the
authority audit and the safety suite after any upstream integration touching
operator reconciliation, the instance manager, probes, Services, configuration,
bootstrap, or upgrades.

## Rule 5 — where things live

- `docs/cnpatroni/` — documentation this fork owns; upstream never touches it.
- `docs/adr/` — architecture decision records.
- `pkg/patroni/`, `internal/patroni/` — new Patroni integration code
  (specification section 9.4). Keep the fork diff concentrated behind explicit
  adapters; leave unrelated inherited packages close to upstream.
- `api/`, `internal/`, `pkg/`, `config/`, `tests/` — inherited CloudNativePG
  code, not modified at M0.
- `LICENSE`, `docs/LICENSE`, `licenses/` — never edit these. `NOTICE` records
  the derivation and the attribution the licences require.
- The documentation website is not built from this repository — there is no
  site configuration in this tree — so the website footer that owner directive
  D-1 requires cannot be satisfied here. It is an outstanding item for whoever
  owns the site repository, and it is not silently closed by the README carrying
  the same text.

## Rule 6 — commits

Conventional Commits, subject under 50 characters, present tense, body wrapped
at 72 characters, scope encouraged: `feat(patroni):`, `fix(instance):`,
`docs(adr):`, `ci(fork-sync):`. Breaking changes use `!` and a
`BREAKING CHANGE:` footer. Sign off every commit. Do not `git commit --amend`
and do not force push without explicit confirmation from the user.

## Rule 7 — how this project describes itself

Use this text verbatim wherever the project's relationship to CloudNativePG is
stated to a reader:

> CloudNativePatroni is an independent project derived from CloudNativePG. It is not affiliated
> with or endorsed by CloudNativePG, CNCF, LF Projects, or the Patroni maintainers.
