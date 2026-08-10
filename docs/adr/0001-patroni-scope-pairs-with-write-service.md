# 0001. Patroni scope pairs with the write Service, and Patroni owns its Endpoints

- Status: Accepted. The proof-of-concept manifests and verifier are merged in
  merge commit `35d05cb2`, and the recorded live kind verification passed all
  nine checks. Repository CI is green but does not run Patroni or kind; operator
  generation and Endpoints lifecycle cases remain unproven
  ([`ci-status.md`](../cnpatroni/ci-status.md)).
- Date: 2026-08-10 05:53:23 UTC
- Sources: [`poc/manifests/cluster/20-config.yaml`](../../poc/manifests/cluster/20-config.yaml);
  [`poc/manifests/cluster/30-services.yaml`](../../poc/manifests/cluster/30-services.yaml);
  [`poc/manifests/cluster/10-rbac.yaml`](../../poc/manifests/cluster/10-rbac.yaml);
  [`poc/manifests/cluster/50-instances.yaml`](../../poc/manifests/cluster/50-instances.yaml);
  [`poc/scripts/verify.sh`](../../poc/scripts/verify.sh);
  [`poc/chaos/deploy/role.yaml`](../../poc/chaos/deploy/role.yaml);
  [`poc/chaos/README.md`](../../poc/chaos/README.md);
  [`naming-policy.md`](../cnpatroni/naming-policy.md)

## Context

Patroni's Kubernetes distributed configuration store can represent its leader
lock in an Endpoints object. The concrete proof of concept enables that mode
with `use_endpoints: true` in `20-config.yaml`. It gives Patroni the scope
`cnpatroni-poc-rw`, and `30-services.yaml` creates a Service with that exact
name. The general pairing is `<cluster>-rw`: the Patroni scope and the write
Service name are the same value.

The same configuration fixes the labels Patroni writes and consumes:
`scope_label: cnpatroni.io/cluster`, `role_label: cnpatroni.io/role`, leader
value `primary`, follower value `replica`, standby-leader value `primary`, and
the additional `cnpatroni.io/podRole: instance` label. Its Kubernetes port is
named `postgresql` on port 5432. The write Service declares the same
`postgresql` port name and port number.

## Decision

The Patroni scope is `<cluster>-rw`, paired exactly with the `<cluster>-rw`
write Service. The Service has no selector. Patroni is the only component that
may create, patch or update the matching Endpoints object, and no other
component may create, update or delete it.

`kubernetes.ports[].name` in Patroni configuration must match the write
Service's `spec.ports[].name`. The implemented contract is established by the
two identical `name: postgresql` declarations in `20-config.yaml` and
`30-services.yaml`; changing one requires changing the other.

The role label in `20-config.yaml` is Patroni-owned. Static Pod manifests do not
write it. A Service may use that label in a selector for read routing, but it
does not write it and must not treat the label as write authority. The
`scope_label` key used by the proof of concept has a separate collision with
the static Pod labels; that remains a known gap rather than an implicit
decision in this record.

## Consequences

Kubernetes does not synthesize the write Endpoints object from a selector.
Patroni publishes the current leader address to the object whose name is its
scope, behind the separately created selectorless Service.

The Patroni Role in `10-rbac.yaml` permits `get`, `list`, `watch`, `create`,
`patch` and `update` on Endpoints, but not `delete`. It permits Pod reads and
patches, grants no Service permission, and grants no ConfigMap permission. The
file records the namespace consequence honestly: Kubernetes cannot constrain
Endpoint creation, listing, watching or patching by object name or label, so
this namespace is the security boundary and must contain one Patroni cluster
and nothing that needs protection from that account. The oracle ServiceAccount
in `poc/chaos/deploy/role.yaml` has only `get`, `list` and `watch` on Endpoints
and EndpointSlices. The host-side chaos driver described by
`poc/chaos/README.md` is limited to signalling the database container's
namespace init, constraining that container's cgroup v2 memory and installing a
dedicated iptables partition chain; it does not write Patroni DCS state.

A live three-node cluster was stood up on kind. The write Service was observed
without a selector. The write Endpoints object contained one address, equal to
the sole primary Pod IP; the other two instances were replicas. SQL through the
write Service returned `pg_is_in_recovery() = false`. These observations show
the concrete routing state that was measured; they do not prove behaviour in
the unexercised lifecycle cases below.

## Enforcement

| Path | What it establishes |
|---|---|
| `poc/manifests/cluster/30-services.yaml` | Creates `cnpatroni-poc-rw` without a selector and names its port `postgresql`. No static manifest creates the matching Endpoints object. |
| `poc/manifests/cluster/20-config.yaml` | Sets scope `cnpatroni-poc-rw`, enables Endpoints mode, fixes Patroni's scope and role labels, and names Patroni's Kubernetes port `postgresql`. |
| `poc/manifests/cluster/10-rbac.yaml` | Gives the Patroni ServiceAccount Endpoints read and write verbs but no delete verb, Service permission or ConfigMap permission. |
| `poc/chaos/deploy/role.yaml` | Limits the oracle ServiceAccount to read-only verbs on Endpoints and EndpointSlices. |
| `poc/chaos/README.md` | Limits the host-side chaos driver to availability-removal primitives and states that it never writes Patroni DCS state. |
| `poc/scripts/verify.sh` | Against `kind-cnpatroni-poc`, checks for three instances with one primary and two replicas, requires the write Endpoints address to equal only the primary IP, waits for the mirrored EndpointSlice to converge, checks that static manifests leave role-label writes to Patroni, and verifies that SQL through the write Service reaches a non-recovery server. |

`verify.sh` does not query the write Service's selector. The selectorless result
above was a separate live observation, while the checked-in absence of
`spec.selector` in `30-services.yaml` is the reproducible static assertion.

## Known gaps

- These are hand-written proof-of-concept manifests. The header in
  `20-config.yaml` calls them a walking-skeleton stepping stone toward exact
  target state for later operator generation, not a substitute for the
  operator. The operator generates none of this yet.
- Endpoints behaviour across Patroni restart, distributed-configuration-store
  re-initialisation and Service recreation has not been exercised.
- EndpointSlice mirroring is asynchronous. `verify.sh` polls for as long as 60
  seconds for convergence; a check written against EndpointSlice rather than
  Endpoints can race the mirror.
- `naming-policy.md` records a separate unresolved collision: the proof of
  concept uses `cnpatroni.io/cluster` as Patroni's `scope_label`, while the
  planned operator uses that key for the logical cluster name. This record
  settles the scope and Service pairing, not that label-key collision.
- The repository states the mechanism and ownership boundary, but carries no
  further rationale for choosing the `<cluster>-rw` scope spelling. This record
  does not invent one.
