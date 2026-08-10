# Parked upstream workflows

GitHub Actions reads workflow definitions only from `.github/workflows/`. Every file in this
directory is therefore inert: it is present in the tree, it is still tracked by git, and it never
runs.

These are CloudNativePG workflows that CloudNativePatroni cannot run. They encode upstream's
release process, its container registries, its organisation secrets and its issue governance.
Running them in this fork produces failures that say nothing about the fork's own code.

They are moved rather than deleted for two reasons. First, an upstream merge that touches one of
these files still applies cleanly to a file that exists, so the merge does not have to be resolved
as a delete-versus-modify conflict on every release. Second, several of them are the starting point
for CloudNativePatroni's own release story, which does not exist yet; keeping the text is cheaper
than reconstructing it.

The reason for each file, and the conditions under which it would return to
`.github/workflows/`, are recorded in
[`docs/cnpatroni/ci-status.md`](../../docs/cnpatroni/ci-status.md).

Do not re-enable a file here by copying it back without also supplying what it needs: the branches,
labels, secrets and registries it references do not exist in this repository.
