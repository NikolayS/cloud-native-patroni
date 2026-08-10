# Fork maintenance: adopting changes from upstream CloudNativePG

CloudNativePatroni is a fork of CloudNativePG. It is not a snapshot: upstream
keeps improving the operator surface that this project keeps, so the fork has to
absorb upstream changes continuously, and it has to do that without quietly
absorbing changes to the high-availability code that this project replaces.

This document is the process for doing that. It is backed by a tool, so most of
it is mechanical.

CloudNativePatroni is an independent project derived from CloudNativePG. It is
not affiliated with or endorsed by CloudNativePG, CNCF, LF Projects, or the
Patroni maintainers.

## The problem this system solves

Git reports a conflict only when both sides changed a file. Once this project
has taken authority over a path, an upstream change that lands beside the fork's
own edit merges cleanly and silently; and while the fork still holds upstream's
code verbatim, an upstream change to a declared path is taken wholesale with no
signal at all. A clean merge is not the same as a safe merge. If upstream adds a
new code path that starts, stops, promotes or demotes PostgreSQL, and this fork
absorbs it silently, the project has two authorities over the database process
again, and loses the property that rules out the
[concurrent timeline fork](failure-modes.md#3-concurrent-timeline-fork) the
architecture exists to prevent.

So the fork keeps a machine-readable declaration of what it does with every
path, and refuses to let an upstream change to a declared path pass without a
human reading it.

## The artefacts

| Artefact | Path | What it is |
|---|---|---|
| Boundary manifest | `hack/cnpatroni/upstream/boundary.yaml` | Which paths CloudNativePatroni adapts, disables, deletes or owns |
| Baseline record | `hack/cnpatroni/upstream/upstream-baseline.yaml` | Which upstream commit the fork started from, and which one it has integrated up to |
| Tool | `hack/cnpatroni/upstream/` | A separate Go module: `setup`, `validate`, `report`, `gitattributes`, `merge-driver` |
| Generated attributes | `.gitattributes` | Generated from the manifest; routes every boundary path through the always-conflict merge driver |

The manifest and the baseline live beside the tool rather than in a top-level
dot-directory, so that one directory holds the whole system and the operator's
own `go.mod` and `go.sum` — the two most merge-hostile files in the repository —
are never touched by it.

The prose record of the baseline, including where it deviates from the
specification, is [upstream-baseline.md](upstream-baseline.md).

## Ownership classes

Every path in the repository falls into exactly one class. The manifest declares
them; the trailing `**` rule absorbs everything not declared.

| Class | Meaning | What happens on an upstream change |
|---|---|---|
| `upstream-untouched` | Taken from upstream verbatim | Absorbed automatically, no review |
| `adapted` | Exists upstream, CloudNativePatroni edits it in place | Reported; a human must read the upstream hunks |
| `disabled` | Present but severed: guarded, unreferenced, or moved aside | Reported, because upstream can add a live path into a file whose old paths were cut |
| `cnpatroni-owned` | Created by CloudNativePatroni; upstream has no counterpart | Upstream touching it is a name collision, not a merge |
| `deleted` | Removed in CloudNativePatroni | A modify/delete conflict is expected and classified as such |

Two manifest-level fields qualify all of the above:

- `classification_state: target` says the classes record the end state the M0
  authority audit decided on, not the current state of the code. At milestone M0
  no CloudNativePG Go file has been modified, so a path marked `adapted` still
  holds upstream's code verbatim. The classification is still what makes the
  gate useful: it says which upstream changes have to be read. Flip the field to
  `applied` when the code matches the declaration.
- `provisional: true` downgrades the undeclared-vocabulary check from an error
  to a warning. Flip it to `false` in the change that accepts the M0 authority
  audit.

## One-time setup of a clone

A fresh clone of this fork cannot answer any upstream question: it has no
`upstream` remote, and a clone made by CI or by a build agent is often shallow.
The tool refuses to guess and tells you what to run.

```bash
cd hack/cnpatroni/upstream
go run ./cmd/cnpatroni-upstream setup --dry-run   # print the git commands
go run ./cmd/cnpatroni-upstream setup             # apply them
```

`setup` is additive and idempotent. It adds the `upstream` remote with a narrow
refspec, so that upstream's development branches are not mirrored; disables
automatic tag following; fetches the tracked branches, backfilling a truncated
history with `git fetch --unshallow` when the clone is shallow; fetches the
upstream version tags explicitly; enables `git rerere` without
`rerere.autoUpdate`, so a remembered conflict resolution is still shown to you;
and registers the boundary merge driver described below.

It never rewrites a commit, never moves a branch, never pushes and never touches
the worktree. The merge driver it installs goes into the clone's git directory,
not into `bin/`, so it cannot show up as an untracked file in the merges it is
meant to police. Run setup again whenever you want; a second run reports every
configuration step as skipped.

The equivalent commands, if you would rather run them by hand:

```bash
git remote add --no-tags upstream https://github.com/cloudnative-pg/cloudnative-pg
git config --replace-all remote.upstream.fetch '+refs/heads/main:refs/remotes/upstream/main'
git config --add remote.upstream.fetch '+refs/heads/release-*:refs/remotes/upstream/release-*'
git config remote.upstream.tagOpt --no-tags
git fetch --unshallow --no-tags upstream    # drop --unshallow on a complete clone
git fetch upstream 'refs/tags/v*:refs/tags/v*'
git config rerere.enabled true
git config rerere.autoUpdate false

driver="$(git rev-parse --path-format=absolute --git-common-dir)/cnpatroni/cnpatroni-upstream"
go build -C hack/cnpatroni/upstream -o "$driver" ./cmd/cnpatroni-upstream
git config merge.cnpatroni-boundary.name "CloudNativePatroni boundary guard"
git config merge.cnpatroni-boundary.driver "'$driver' merge-driver '%O' '%A' '%B' '%L' '%P'"
```

The driver command has to be an absolute path and must not change directory:
git substitutes `%O %A %B` with temporary file names relative to the repository
root, so a driver wrapped in a `cd` cannot open them. Git runs the command
through a shell, so the path and placeholders are single-quoted; a clone path
containing a single quote or a newline is not supported.

In CI, check out with `fetch-depth: 0` and then run `setup`; a shallow checkout
cannot answer the drift question and the tool will say so rather than reporting
a clean result.

## The commands

```text
cnpatroni-upstream [--repo DIR] [--manifest FILE] [--baseline FILE] [--exit-zero] <command>

  setup          configure this clone
  validate       check the manifest against the worktree and the history
  report         compute the divergence from upstream
  gitattributes  render .gitattributes from the boundary manifest
  merge-driver   git's always-conflict driver for a boundary path; git runs it, you do not
  version        print the tool version
```

Exit codes are shared across the commands:

| Code | Meaning |
|---|---|
| 0 | Nothing needs a human |
| 1 | Upstream changed a path this fork adapts, disables or deletes |
| 2 | A textual conflict is predicted |
| 3 | A path is not classified: new high-availability code upstream, a collision with an owned path, or a file this fork edited without declaring |
| 4 | The manifest or the baseline is malformed, or claims something git contradicts |
| 5 | The command line was wrong |
| 6 | The clone is not set up: it is shallow, or the upstream remote is missing |

`--exit-zero` forces 0 while still printing everything, for informational runs.
`merge-driver` is the exception: it always exits 2, and `--exit-zero` cannot
suppress that, because a zeroed exit code would tell git that a boundary file
merged cleanly.

### validate

```bash
go run ./cmd/cnpatroni-upstream validate --drift          # the CI gate
go run ./cmd/cnpatroni-upstream validate --drift --strict # before accepting the audit
```

`validate` answers three questions:

1. Is the manifest well formed? The schema is right, the catch-all rule is last,
   no two rules claim the same literal path, every glob is legal, and
   `state: planned` appears only on a `cnpatroni-owned` rule.
2. Does it match the worktree? Every declared path exists, every `deleted` path
   is genuinely gone and genuinely existed at the fork base, every moved-aside
   path really moved, and every path in `required_paths` is classified by
   something other than the catch-all.
3. Is it honest about history? Both commits in the baseline are ancestors of
   `HEAD`, so the baseline cannot claim an integration that did not happen.

With `--drift` it also reports files this fork has changed since the fork base
whose declaration does not account for the change. That is the check that stops
the boundary from eroding one pull request at a time. Two things are reported:

- `D1`, a changed path no rule classifies, or one that only the catch-all
  classifies. The remedy is to declare it.
- `D2`, a changed path that does not hold the content its rule claims. The
  remedy is to restore the upstream content or to reclassify the path, which is
  a different problem from an undeclared one, so the two are reported and
  remedied separately.

Only `adapted`, `disabled`, `cnpatroni-owned`, and `deleted` while the path is
genuinely absent explain a diff on their own. Matching a specific rule is not
itself an explanation, or any edit could be laundered past the gate by writing
one line of YAML.

### What upstream-untouched means operationally

`upstream-untouched` is proved, not asserted. The gate compares the blob the
path holds at `HEAD` against the set of blobs in the fork base tree, so the
claim it settles is "these are upstream's bytes", not "this path looks
untouched":

- Content that was already in the fork base is clean **wherever it now sits**.
  That is why the verbatim copies parked in `.github/workflows-upstream/` pass:
  they are byte-identical to the files they were moved from.
- Content that was not in the fork base is drift, however new or specific the
  path is. Absence of proof is drift; there is no way to declare around it.
- A path the fork removed is drift too. The honest declaration is `deleted`.

The operational consequence for a maintainer: **do not edit a file declared
`upstream-untouched` in place**, including a parked one. Changing the content
changes the blob, and the gate will fail. If the fork genuinely needs to change
such a file, reclassify it first — `adapted` for an edit in place, `disabled`
with a mechanism for a severed one — so that the change is declared before it
lands rather than explained afterwards.

The check worth understanding is the vocabulary scan. The manifest lists the
regular expressions that name PostgreSQL high-availability authority —
`TargetPrimary`, `pg_ctl`, `pg_rewind`, `synchronous_standby_names`,
`switchover` and the rest. Any Go file matching one of them that is classified
`upstream-untouched` without `audit_reviewed: true` is a finding. Four files
match the terms and carry no authority; they are declared reviewed, by name,
with the reason.

The scan is necessary but not sufficient. Twenty real authority paths match none
of the terms, including the whole PID-1 supervisor, the isolation self-fence
probes and the read-write Service selector. Those are listed in
`required_paths`, and validation fails if a rule stops covering one.

### report

```bash
go run ./cmd/cnpatroni-upstream report --fetch --stdout
```

`report` diffs the recorded baseline against upstream, intersects the changed
paths with the manifest, and writes two files into
`docs/cnpatroni/upstream-integration/<date>-<sha12>/`:

- `report.json`, the machine-readable form specification section 20.2 requires;
- `report.md`, the same content for a human, and for the tracking issue body.

The fields worth reading first:

- `gate.verdict` and `gate.exit_code` — the decision, in one line.
- `gate.requires_*` — the union of the gates of every rule that matched a
  changed file. This is what turns the prose requirement into a mechanical one.
- `authority_surface_delta.undeclared` — files that carry high-availability
  vocabulary at the far end of the range and are classified only by the
  catch-all. A non-empty list means upstream grew a decision point this fork
  does not track. Stop and classify it before merging anything.
- `volume.concentration_ratio` and `volume.authority_concentration_ratio` — how
  much of upstream's change lands on the boundary, and how much of that carries
  actual failover authority rather than branding or build files. The second
  number is the one that predicts long-term integration cost.
- `manifest_stability.declared_but_missing_upstream` — a rule that has stopped
  classifying anything because upstream deleted the file.

## The boundary merge driver

`validate` and `report` both run before a merge. The merge driver is the guard
that runs during one.

The reason it exists is the first case at the top of this document: an upstream
change landing beside this fork's own edit in a boundary file. Git's own
conflict signal is not evidence that a merge was safe, so the fork adds a signal
of its own.

`.gitattributes` at the repository root is generated from `boundary.yaml`. Every
path whose ownership obliges a human to read the upstream hunks — `adapted`,
`disabled`, `deleted` — is routed through a merge driver named
`cnpatroni-boundary`, which reports every such merge as unresolved:

```text
pkg/management/postgres/instance.go cnpatroni-boundary=adapted merge=cnpatroni-boundary
```

Paths CloudNativePatroni owns outright carry the attribute but no driver:
upstream has no counterpart to merge, so a file arriving there is a name
collision, which the divergence report already reports. The catch-all rule is
never rendered — an attribute line for `**` would route the whole repository
through the driver. `git check-attr` explains any path:

```bash
git check-attr -a pkg/management/postgres/instance.go
# pkg/management/postgres/instance.go: merge: cnpatroni-boundary
# pkg/management/postgres/instance.go: cnpatroni-boundary: adapted
```

### Regenerating the attributes

The file is generated, and a hand edit is overwritten without warning. After any
change to `boundary.yaml`:

```bash
cd hack/cnpatroni/upstream
go run ./cmd/cnpatroni-upstream gitattributes           # write it
go run ./cmd/cnpatroni-upstream gitattributes --stdout  # print it instead
go run ./cmd/cnpatroni-upstream gitattributes --check   # exit 4 if it is stale
```

A stale file is not cosmetic: it guards a boundary that has moved. Regenerate it
in the same commit that changes the manifest.

### Resolving a forced boundary conflict

When the driver fires, git stops the merge, records the three stages in the
index and prints which path stopped it. `git status` shows the path as `UU`.

1. Read what upstream changed, which is the whole point of the stop:

   ```bash
   git diff --merge-base HEAD MERGE_HEAD -- pkg/management/postgres/instance.go
   ```

2. Look at the file. It comes in one of two shapes.

   - **It has conflict markers.** The two sides really did collide. The markers
     are diff3-style and labelled `ours (CloudNativePatroni)`, `base (last
     integrated upstream)` and `theirs (upstream)`, so the middle section tells
     you what upstream started from. Resolve them by hand.
   - **It has no markers.** This is the case the guard exists for: git merged the
     file cleanly and the driver stopped the merge anyway. The worktree already
     holds the merged text and nothing was lost. There is nothing to edit — the
     work is the review in step 3.

3. Answer, in the pull request, the only question that matters under rule 1 of
   `CLAUDE.md`: does the upstream change add a code path that starts, stops,
   promotes, demotes or reconfigures Postgres? If it does, it belongs to Patroni
   and it must not be absorbed as it stands.

4. Stage the file and continue:

   ```bash
   git add pkg/management/postgres/instance.go
   git merge --continue
   ```

5. Record what you decided in the merge commit body. `git rerere` caches the
   resolution locally only, so the commit message is the only durable record.

### What the driver does not catch

Git invokes a merge driver only when both sides changed the file. While
`classification_state` is still `target` and the fork holds upstream's code
verbatim, an upstream change to a boundary file this fork has not yet edited is
resolved by taking upstream's version outright, and the driver never runs. The
guard becomes load-bearing as the adaptations land; until then the divergence
report and the drift gate are what make those changes visible.

The registration is also per clone, because git will not take a merge-driver
definition from a tracked file — that would let any branch run a command on the
machine that merges it. A clone that has never run `setup` has the attributes
but no guard. This is why the driver is one of three independent mechanisms
rather than the only one.

## The per-integration checklist

Copy this into the integration pull request as a task list.

- [ ] 1. `cnpatroni-upstream setup` — or `git fetch --no-tags upstream && git fetch upstream 'refs/tags/v*:refs/tags/v*'`.
- [ ] 2. `cnpatroni-upstream report --stdout`. Read the verdict line before anything else.
- [ ] 3. If `authority_surface_delta.undeclared` is non-empty, stop. Upstream introduced
      high-availability vocabulary in a file this fork does not track. Classify it in
      `boundary.yaml`, regenerate `.gitattributes`, get that change reviewed on its own,
      and restart at step 2.
- [ ] 4. If `manifest_stability.collisions_with_cnpatroni_owned` is non-empty, stop.
      Upstream has taken a path this project owns; rename ours before merging.
- [ ] 5. Create the integration branch and merge:
      `git checkout -b upstream-integration/<date>-<sha12> && git merge --no-ff --no-commit upstream/main`.
      Never rebase, never force-push: the merge commit is what preserves upstream authorship.
- [ ] 6. For every path the report lists as `adapted` or `disabled`, read the upstream hunks
      and answer in the pull request: does this add a code path that starts, stops, promotes,
      demotes or reconfigures PostgreSQL? Do this even when git merged the file cleanly.
      The merge driver stops the merge on these paths precisely so that this step cannot be
      skipped; see "Resolving a forced boundary conflict" above.
- [ ] 7. If `gate.requires_regeneration`, run `make fmt vet generate manifests apidoc
      wordlist-ordered` and commit the regenerated files as a separate commit.
- [ ] 8. If `gate.requires_authority_audit`, re-run the high-availability authority audit and
      add any new rows to the audit document.
- [ ] 9. If `gate.requires_chaos_gate`, run the chaos safety gate and attach the results.
      Until that gate exists, record "chaos gate not yet implemented — deferred" explicitly
      in the pull request. Do not skip it silently.
- [ ] 10. If `gate.requires_adr_review`, tag the owning decision record and get an explicit
      acknowledgement from its author.
- [ ] 11. `cnpatroni-upstream validate --drift`, `cnpatroni-upstream gitattributes --check` and
      the repository's own `make checks && make test` pass.
- [ ] 12. Open the pull request with `report.md` as the body. Do not squash.
- [ ] 13. After the merge lands, update `last_integrated` in
      `hack/cnpatroni/upstream/upstream-baseline.yaml`, commit the report directory so the
      integration history is auditable, and close the tracking issue with a link.

### Merge commit message

```text
chore: integrate upstream CloudNativePG

Merge upstream/main <sha> (<describe>, <date>).

Upstream commits: N. Files changed: N (+N/-N).
Boundary files touched: N (+N/-N). Concentration ratio: 0.0NN.
Gates run: authority-audit, chaos.

Conflict resolutions:
- <path>: <what was kept, what was taken, and why>

Report: docs/cnpatroni/upstream-integration/<date>-<sha12>/report.md
```

The conflict-resolution block is required. `git rerere` caches resolutions
locally only, so the merge commit body is the only durable record of why a
boundary hunk was resolved a particular way.

## What "compatible with upstream CloudNativePG X.Y" means

CloudNativePatroni claims compatibility with upstream minor `X.Y` only when all
six of the following hold. Each is mechanically checkable.

1. **Ancestry.** `git merge-base --is-ancestor vX.Y.0 <last_integrated.commit>`
   exits 0: the integrated upstream point is at or after the `X.Y.0` tag.
2. **Completeness.** `git rev-list --count <last_integrated.commit>..<latest vX.Y.Z tag>`
   is 0, or every commit it lists appears in `not_adopted` with a written
   reason. The count is recorded as `compatibility.unadopted_upstream_commits`.
3. **Landed.** The recorded `last_integrated.commit` is an ancestor of `HEAD`
   — validation check V11.
4. **Boundary is honest.** `validate --drift --strict` exits 0 on the commit
   being claimed.
5. **Build and tests.** The repository's own `make checks` and `make test` pass,
   and the tooling's tests pass.
6. **Safety.** The authority audit shows no forbidden call reachable, and the
   chaos safety gate is green. Before that gate exists, the claim must say so.

What the claim does **not** mean: this fork's base is upstream `main`, not the
`release-X.Y` branch, so it carries changes upstream has not shipped in any
patch release. The honest phrasing is "based on the CloudNativePG 1.30
pre-release trunk (`v1.30.0-69-gb22682115`)", never "based on CloudNativePG
1.30.0".

## Cadence

| Rhythm | Trigger | What runs |
|---|---|---|
| Every pull request and push | `pull_request`, `push` | `validate --drift` and the tooling's unit tests |
| Weekly, Mondays 06:00 UTC | `schedule` | `report`, into one tracking issue that is edited in place |
| On demand | `workflow_dispatch` | The same report, against any ref |
| Monthly, or on an upstream release day | A human | The full integration checklist |

Both jobs live in `.github/workflows/cnpatroni-upstream-sync.yml`.

Two things a maintainer must do once, or the scheduled job fails on its first
run:

1. Create the label:
   `gh label create upstream-integration --color 1D76DB --description "Upstream CloudNativePG integration"`.
2. Make `boundary-guard` a required status check in branch protection. Without
   that, the gate is advisory and the boundary will erode.

Two GitHub behaviours worth knowing: scheduled workflows fire only from the
default branch, and GitHub disables schedules after 60 days of repository
inactivity — a scheduled run does not itself count as activity.

## Escalation

If a single integration's `authority_concentration_ratio` exceeds 0.15, or the
mean over the last three integrations exceeds 0.10, escalate with the report
attached. That number is the fork's own prediction of its long-term maintenance
cost, and a rising trend means the diff is spreading into packages upstream also
changes, which is the condition the architecture is supposed to avoid.

## Extending the manifest

New CloudNativePatroni code must be declared before it lands, or `validate
--drift` fails the pull request. Add the path to an existing rule, or add a new
rule above the catch-all, then re-run the gate:

```bash
cd hack/cnpatroni/upstream
go run ./cmd/cnpatroni-upstream --repo ../../.. validate --drift
```

Glob syntax is deliberately small: `**` matches whole path segments and is legal
only as a complete segment, `*` matches within one segment and never crosses
`/`, `?` matches one character, and a trailing `/` is shorthand for `/**`. There
are no character classes, no brace expansion and no negation — express a
negation by declaring a more specific rule. When two patterns match a path, the
one with the longer literal prefix wins; an exact literal path beats every glob;
the catch-all always loses.
