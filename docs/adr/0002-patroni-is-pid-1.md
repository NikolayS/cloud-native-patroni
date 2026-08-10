# 0002. Patroni is PID 1 in the database container

- Status: Accepted by owner. The literal-PID-1 proof of concept and its eight
  container contracts are merged in merge commit `35d05cb2`, and the recorded
  live kind measurements passed for the cases exercised. Repository CI is
  green but does not run a Patroni container; the supervisor comparison and
  client-acknowledgement evidence remain unproven
  ([`ci-status.md`](../cnpatroni/ci-status.md)).
- Date: 2026-08-10 05:53:23 UTC
- Sources: [`poc/image/Dockerfile`](../../poc/image/Dockerfile);
  [`poc/image/entrypoint.sh`](../../poc/image/entrypoint.sh);
  [`poc/manifests/cluster/20-config.yaml`](../../poc/manifests/cluster/20-config.yaml);
  [`poc/manifests/cluster/50-instances.yaml`](../../poc/manifests/cluster/50-instances.yaml);
  [`poc/image/test/run-contract-tests.sh`](../../poc/image/test/run-contract-tests.sh);
  [`poc/scripts/verify.sh`](../../poc/scripts/verify.sh);
  [`poc/chaos/README.md`](../../poc/chaos/README.md);
  [`README.md`](../../README.md);
  [`ROADMAP.md`](../../ROADMAP.md);
  [`failure-modes.md`](../cnpatroni/failure-modes.md)

## Context

The process model carried in the repository's top-level `README.md` permits
Patroni either to be PID 1 or to be the direct child of a transparent init such
as `tini`, limited to signal forwarding and reaping. It also records that
specification section 21 requires an experiment comparing the process models.
The owner selected the stricter form without that comparison: Patroni runs as
literal PID 1.

This distinction is about the container lifecycle, not merely a numeric
process identifier. Patroni must receive termination signals, reap orphaned
children, and make its own exit terminate the container. A resident shell,
supervisor or init shim would be a different process model.

## Decision

Patroni runs as literal PID 1 in the database container. There is no `tini`,
`dumb-init`, supervisor or resident shell. Every startup script performs finite
setup and hands off with `exec patroni`. If Patroni exits, the container exits;
nothing inside the container loops or restarts it.

The image in `poc/image/Dockerfile` runs as `USER 999:999` and declares
`/usr/local/bin/cnpatroni-entrypoint` as its image `ENTRYPOINT`.
`poc/image/entrypoint.sh` ends with
`exec patroni "$CNPATRONI_CONFIG_FILE"`.

That image entrypoint is not the path the proof-of-concept Pods run. Each Pod's
`command` in `50-instances.yaml` overrides the image `ENTRYPOINT` with
`/bin/bash /etc/cnpatroni/config/entrypoint.sh`. The referenced ConfigMap script
is defined in `20-config.yaml`, and it ends with
`exec patroni /run/cnpatroni/patroni.yml`. Both paths obey the decision, but
only the ConfigMap path is live in the cluster manifests.

## Consequences

The following fault-matrix results are measurements from a live three-node
cluster on kind, not general guarantees:

- Reading `/proc/1/comm` on all three instances returned `patroni`. Process
  inspection found no init shim and no resident shell.
- `/proc/1/status` reported `SigCgt=0x14a27`, which shows installed handlers for
  SIGHUP, SIGINT, SIGQUIT, SIGABRT, SIGUSR1, SIGUSR2, SIGTERM and SIGCHLD. PID 1
  does not receive the kernel's default terminating signal dispositions: a
  signal with no installed handler is discarded. The SIGTERM handler is
  therefore necessary for normal Pod termination to initiate graceful
  shutdown instead of reaching the grace-period SIGKILL, and SIGCHLD handling
  is necessary for child reaping.
- Orphan reaping was demonstrated rather than inferred. A process was
  reparented to PID 1 and then reaped, with no zombie left. This is work a
  transparent init would normally perform and is why literal PID 1 required
  measurement.
- Sending SIGKILL to PID 1 produced exactly one container restart with exit
  code 137, one replacement leader, a timeline change from 1 to 2, and both
  replicas following. Across the fault, `pg_is_in_recovery()` was sampled on
  all three instances every 0.5 seconds; none of 288 samples found two writable
  instances.

The last measurement establishes single writability at the observed sample
times. It is not evidence that only one node ever acknowledged a commit.

## Enforcement

`poc/image/test/run-contract-tests.sh` builds the production image and runs
eight container contracts:

| Contract | What it asserts |
|---|---|
| `entrypoint-ends-with-exec-patroni` | The image entrypoint's last instruction is `exec patroni`; it contains no listed privilege wrapper, init, supervisor, loop, trap or background command; the image uses `STOPSIGNAL SIGTERM`, a digest-pinned base and final user `999:999`. |
| `patroni-is-pid-1` | `/proc/1/comm` is `patroni`, the PID 1 command line names the rendered test configuration, and no listed init, supervisor or bare shell is present. |
| `pid-1-catches-sigterm` | The caught-signal mask includes SIGHUP, SIGINT, SIGTERM and SIGCHLD. |
| `sigterm-shuts-postgres-down-gracefully` | SIGTERM stops the container with exit code 0 and the logs show both a fast-shutdown request and completed database shutdown. |
| `container-exits-when-patroni-exits` | Killing Patroni's child stops the container, and no second high-availability loop starts. |
| `no-postgres-survives-container-exit` | After SIGKILL of the container, none of the recorded host PIDs and no process matching the test Patroni scope survives. |
| `postmaster-kill-leaves-no-zombies` | With three sleeping Postgres backends, SIGKILL of the postmaster leaves no observed zombie while Patroni starts a replacement postmaster that answers SQL within 30 seconds. |
| `rendered-config-is-valid-and-0600` | Patroni validates the rendered configuration, its mode is 0600, output exposes no password, and empty required variables and colliding credentials fail without exposing the secret. |

The cluster verifier in `poc/scripts/verify.sh` separately reads `/proc/1/comm`
from all three database containers. `20-config.yaml` enforces the live exec
handoff, and `50-instances.yaml` supplies the overriding command and a kubelet
liveness probe against Patroni's `/liveness` endpoint.

## Known gaps

- The 288 samples establish single writability only at their sample times. The
  direct-Pod write oracle could not be built when the fault was injected, so
  there is no evidence about client acknowledgements and no basis for claiming
  that only one node ever acknowledged a commit.
- The crash restarted within about one second against the configured
  thirty-second TTL. As `poc/chaos/README.md` states, the leader lock did not
  expire and the scenario did not exercise a leader election. It demonstrates
  restart recovery, not failover.
- Fencing a hung Patroni process depends on the kubelet liveness probe in
  `50-instances.yaml`. If kubelet is stopped or hung, or the node is
  unschedulable, that fence does not act. `20-config.yaml` explicitly configures
  the watchdog mode as `off`; `failure-modes.md` places these combined failures
  outside the current supported fault model.
- The supervisor alternative was neither prototyped nor measured. The
  comparison that the top-level `README.md` says specification section 21
  requires was not performed. The decision rests on the owner's instruction
  and the measurements above, not on comparative evidence.
- No raw artifact bundle for the listed live measurements is checked into this
  repository, so the run cannot be reconstructed from this tree alone.
