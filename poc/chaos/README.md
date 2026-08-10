# Chaos scenario driver

The host-side driver can remove availability from a kind cluster. Its
allowlisted primitives signal a database container's namespace init, constrain
that container's cgroup v2 memory, or install a dedicated iptables partition
chain. It never promotes or starts Postgres and never writes Patroni DCS state.

`--context` has no default. Before startup and immediately before every fault,
the driver lists nodes with that explicit context and requires kind provider IDs
and names derived from the context's cluster name. A rejected guard exits 3.

The example deployment files use namespace `database` and placeholder database
Pod names. Edit them for the run, build and publish the oracle image at a pinned
tag, and create the named credential Secret without committing its contents.
The driver expects an already running oracle Pod labelled
`cnpatroni.io/component=oracle`.

`s1-primary-crash` exercises restart in place while the leader still holds the
DCS session lock because kubelet restart latency is far below `ttl: 30`. It
does not exercise a leader election, so a passing `s1` is not evidence that
failover works. A scenario that forces an election must keep the leader down
longer than `ttl`, and no such scenario exists yet.

Example:

```text
go run ./cmd/chaos \
  --context kind-cnpg-sb \
  --namespace database \
  --scenario s3-partition \
  --partition-duration 120s \
  --bundle-dir runs
```

The driver records every exact command in `faults.jsonl`, brackets each fault
through the oracle's port-forwarded control port, captures previous-container
logs, copies the incrementally written oracle bundle, and returns the oracle's
0, 1, 2, or 3 exit code.
