# Kubernetes operator

Install one stateless, singleton Recreate operator per cluster. The operator uses
controller-runtime v0.23.3; controller-tools v0.20.0 generates CRDs, deepcopy code,
RBAC, and the [resource reference](KUBERNETES_API.md).

The public OCI Helm chart installs **only the operator and CRDs**. Components are
namespaced `tailscale-private-funnel.io/v1alpha1` resources created by the CLI.
The control database (SQLite by default, or PostgreSQL) remains authoritative for application authorization, providers, policy,
enrollment, reports, DNS ownership, and audit.

## Install and create control

Generate installers without cluster connectivity, then execute them on a machine
with the selected kubeconfig. Control/operator generation requires no existing
Funnel control. Every Kubernetes operation requires an explicit context.

```sh
tailscale-private-funnel k8s operator create \
  --context CLUSTER --version VERSION \
  --managed-namespace tailscale-private-funnel-main-system --wait --output script > install-operator.sh

tailscale-private-funnel k8s controls create main \
  --context CLUSTER --namespace tailscale-private-funnel-main-system \
  --version VERSION --hostname funnel-control --tailnet TAILNET \
  --oauth-client-id-file ./ts-client-id \
  --oauth-client-secret-file ./ts-client-secret --wait --output script > install-control.sh

# Transfer these scripts to the execution machine, with OAuth files provisioned there.
sh install-operator.sh
sh install-control.sh
```

SQLite controls and load balancers require a working dynamic StorageClass.
PostgreSQL controls reference existing database Secrets and allocate no PVC; see
[CloudNativePG and Argo CD](POSTGRESQL.md). If the cluster
has no default, pass `--storage-class NAME --storage-size 1Gi` at creation.
Provisioning reports `StoragePending` until the PVC binds; it does not silently
choose an arbitrary storage provider.

Credential files must be private regular files (0600). Credentials are sent only
to a dedicated bootstrap Secret, never CR specs, Helm values, Events, logs, or
status. The operator deletes its owned bootstrap Secret after initialization.
The private setup URL is exposed in `k8s controls get main --context CLUSTER`.
Management remains tailnet-only; no public Kubernetes Service exposes control.

## Component resources

| CRD | Owned resources | Persistent state |
|---|---|---|
| FunnelControl | Recreate Deployment, exact-Secret role, identity Secret; PVC for SQLite only | SQLite on PVC or external PostgreSQL; tsnet and encryption key in Secret |
| FunnelLoadBalancer | Recreate Deployment, identity Secret, certificate PVC, TCP LoadBalancer Service | tsnet/signing key in Secret; Caddy certificates on PVC |
| FunnelGateway | Gateway-agent Deployment, broker, leases, identities, forwarder DaemonSet, classes | Agent and per-node tsnet identities in broker-managed Secrets |

The UI generates complete `k8s gateways create` and `k8s loadbalancers create`
commands using the selected context, namespace, image version, and settings.
Commands execute locally through Kubernetes credentials; the control service
never receives kubeconfigs or remote-execution permissions.

Use `k8s <resource> get NAME` for observed generation, conditions, identity,
forwarder counts, and public Service allocation. Workload readiness alone does
not prove public DNS/ACME/backend readiness; also inspect enrolled health in the
control UI. Additional load balancers receive independent identities and storage.

`GatewayClass.parametersRef` references the owning FunnelGateway infrastructure
resource. Gateway API and Ingress resources remain the routing interface; their
class selection, allowedRoutes, ReferenceGrant, backend validation, node-local
forwarding, and status isolation are enforced by the gateway agent. No full
Gateway API conformance is claimed.

## Isolation

Operator namespace: `tailscale-private-funnel-k8s-operator-system`. Installation
namespace: `tailscale-private-funnel-<installation>-system`. Explicit
`--managed-namespace` grants workload/Secret permissions only to selected
namespaces. The operator is trusted infrastructure with cluster-scoped routing
and delegated RBAC permissions; do not expose its ServiceAccount to tenants.

Control/load-balancer runtime roles address exact identity Secrets. Brokers have
namespace-scoped identity/lease permissions and identity-validation reads, with
no workload creation, Pod exec, or Pod patch permissions. Forwarders get only
short-lived audience-bound broker tokens and no Kubernetes RBAC bindings.
Different installations use separate namespaces. Pods run nonroot with dropped
capabilities, read-only filesystems and seccomp. Only load balancers add
NET_BIND_SERVICE. No TUN, Docker socket, or host networking is used in Kubernetes.

Forwarders use only ready Pod endpoints on their own node. Each identity is
lease-fenced; losing the lease stops advertising before another Pod takes over.
Public LoadBalancer allocation comes from the cluster implementation. A private
or pending address does not satisfy WAN/ACME acceptance. Client-IP preservation
must be verified with that implementation and externalTrafficPolicy Local.

## Updates, deletion, and recovery

```sh
tailscale-private-funnel k8s gateways update site --context CLUSTER \
  --namespace tailscale-private-funnel-main-system --version VERSION --wait
tailscale-private-funnel k8s gateways delete site --context CLUSTER \
  --namespace tailscale-private-funnel-main-system --yes --wait
```

Ordinary restart/update preserves identity. Corrupt or missing initialized state
fails closed. Bootstrap expiry requires fresh credentials. There is no adoption,
import, migration, resume, or compatibility CLI.

Deletion stops identity owners before a cleanup Job uses the same identity.
Finalizers revoke node/Service access, complete owned DNS cleanup, and remove
owned Secrets, leases, certificates, and PVCs. Failure retains state and the
finalizer with diagnostics. External referenced resources are not implicitly
owned. Deleting a PVC follows its StorageClass reclaim policy: a `Retain`
PersistentVolume may remain for storage-administrator cleanup. PVC deletion is
not a claim of physical data erasure. A control cannot be deleted while active enrolled components remain;
operator deletion refuses while any component CR remains. Never remove a
finalizer to bypass a cleanup failure.

Back up the control database and its matching identity Secret before planned upgrades. Rollback
uses the prior pinned image and matching compatible state. Replacing an existing
installation requires explicit cleanup and fresh provisioning; never run two
processes with one tsnet identity.

Gateway-agent RBAC reads routing and backend metadata at cluster scope, including
when `watchNamespaces` limits processing. This supports cross-namespace reference
validation without granting workload or Secret permissions in application
namespaces. Broker identity permissions remain restricted to its installation
namespace. Forwarders have no API bindings. Namespace watch filters are not an
RBAC isolation boundary; `allowedRoutes`, ReferenceGrant, policy, and backend
validation remain mandatory.

For clusters with tainted control-plane nodes, supply explicit Pod placement:

```json
{"tolerations":[{"operator":"Exists","effect":"NoSchedule"}]}
```

Save this as `placement.json` and use `--placement-file ./placement.json` on
operator or component creation. Gateway forwarders have a separate
`--forwarder-placement-file`; the agent's placement does not grant forwarders
permission to run on those nodes. Placement files contain only node selectors,
affinity, and tolerations, never credentials or arbitrary Helm values.

Component deletion uses a two-phase shutdown. Control first confirms owned route,
Service-host, and DNS withdrawal, then queues device deletion. Cleanup reconnects
the existing identity without bootstrap credentials to confirm revocation before
removing local state. Failures retain state for retry; cleanup never reenrolls.
Control deletion is blocked by active nodes or pending cleanup tasks.

## Control updates and bounded forwarding continuity

Forwarders keep their existing listeners during short control outages. The broker
may reuse only a previously verified signed configuration and known child
membership, for at most five minutes from their last successful verification.
It never refreshes an old signature or grants a new identity during an outage.
Explicit authorization denial and invalid signatures invalidate the cache.
Kubernetes token/Pod validation and the per-node ownership lease remain mandatory;
failing to renew that lease still fences forwarding before it expires.

Forwarder reports do not wait behind provider reconciliation. Report acceptance
and backend updates reject revoked nodes and revoked parents transactionally.
Local readiness describes forwarding with a valid configuration and lease;
a temporary inability to deliver a report does not tear down healthy listeners.
An expired configuration, lost lease or missing local backend stops forwarding.
Control outages longer than the freshness window can withdraw traffic and DNS.
This is bounded continuity during maintenance, not a highly available control plane.

Control also distinguishes upstream authorization API failure from an explicit
revocation. An unavailable lookup does not approve new hosts or disapprove a
known healthy host; local enrollment, report freshness, policy and acknowledgments
remain required. Errors are reported for retry. OAuth tokens use their provider
expiry with standard in-memory renewal, independently of the forwarding lease.

The canonical token endpoint is `https://api.tailscale.com/api/v2/oauth/token`,
as specified in [Tailscale OAuth documentation](https://tailscale.com/docs/features/oauth-clients#tailscale-oauth-token-endpoint).
Expiry-aware token sources are shared by matching credentials; rotation starts a
new source. Token caching is in memory and does not change node identity persistence.

## Separate authorization from cluster execution

`k8s ... create --context TARGET --output script` generates an installer without
reading kubeconfig or contacting the cluster. Component generation requires
authorized control access; control/operator generation does not. Execute the
output on a machine with the selected kubeconfig. It needs no Tailscale route to
control: deployed components establish their own embedded tsnet connection.
See [installer workflow](INSTALLER-WORKFLOW.md) for expiry and Secret handling.

## Offline control transfer

Use [`transfer-remote`](CONTROL_TRANSFER.md) to move control between native and Kubernetes or change SQLite/PostgreSQL. The command requires explicit source Kubernetes access, fences the old control through the operator, and saves its destination installer with `--out` before removing owned source resources. Suspend GitOps reconciliation of the source declaration first. Transfer preserves the existing Tailscale identity and DNS ledger; it does not invoke ordinary deletion or adopt external databases.
