# Kubernetes operator

One singleton Recreate operator per cluster uses controller-runtime v0.23.3 and
controller-tools v0.20.0. The OCI Helm chart installs the operator and CRDs; component
installers create namespaced `tailscale-private-funnel.io/v1alpha1` resources.
Different installations use separate `tailscale-private-funnel-<installation>-system`
namespaces. Control, the agent/broker, and operator are trusted installation infrastructure.

| Component | Workload | Durable application state |
|---|---|---|
| FunnelControl | Single-replica Recreate Deployment | Encryption/storage material Secret; SQLite PVC or external PostgreSQL |
| FunnelLoadBalancer | Single-replica Recreate Deployment and public TCP LoadBalancer Service | Caddy certificate PVC |
| FunnelGateway | Recreate agent/broker Deployment and node-local forwarder DaemonSet | No Tailscale identity persistence |

All Pods use embedded, ephemeral tsnet identities and temporary memory-backed state.
Logical component/application IDs derive from the component resource, not device IPs.
The control's canonical address is its installation Tailscale Service FQDN. Native
components remain persistent by default; Kubernetes replacement does not restore a
network identity.

## Provision

Generate and execute the operator installer first, with an explicit context and
managed namespace:

```sh
tailscale-private-funnel k8s operator create --context CLUSTER --version VERSION \
  --managed-namespace tailscale-private-funnel-main-system --output script > operator.sh
sh operator.sh
```

Provision one protected shared installation issuer Secret externally. It uses `client_id` and `client_secret`; the issuer needs only `auth_keys` for
`tag:tailscale-private-funnel-<installation>-issuer`, which directly owns all five
role tags. All component `keyIssuerRef` values reference that same Secret. Control additionally references a separate `services` + `devices:core:read`
client tagged with the control role (which owns both Service tags), and a
`users:read` client. Never put credential values in CRs, Helm values, Events, or screenshots.

```sh
tailscale-private-funnel k8s controls create main --context CLUSTER \
  --namespace tailscale-private-funnel-main-system --installation main \
  --hostname funnel-control --tailnet TAILNET --version VERSION \
  --key-issuer-secret installation-issuer --services-secret service-manager \
  --users-secret human-role-reader --wait --output script > control.sh
sh control.sh
```

The execution machine needs cluster access, not Tailscale. SQLite and load-balancer
certificates need a working StorageClass. PostgreSQL control allocates no SQLite PVC;
see [PostgreSQL](POSTGRESQL.md). The generated [CR reference](KUBERNETES_API.md) is authoritative.
Control's management listener is tailnet-only; its Kubernetes health probe is not a
management endpoint. Public load balancers use the cluster's existing LoadBalancer
implementation with `externalTrafficPolicy: Local` by default.

## Bootstrap and permissions

The operator's TLS key-issuance endpoint validates an audience-bound projected
service-account token, Pod UID, owner workload, component UID, installation,
namespace, service account, and assigned node. It derives the exact role server-side;
Pods cannot choose tags. Each response contains a fresh one-use ephemeral key, not
OAuth credentials. Referenced credential/database Secrets remain externally owned.

Control has no Kubernetes API permission or API token. A load balancer can read only
its exact public Service. Forwarders have no Kubernetes API permissions; they receive
separate projected audiences for operator bootstrap and broker configuration.
The broker reads verified Pods/nodes/DaemonSet ownership and coordinates bounded
assignment leases. It has no namespace Secret mutation or identity-restoration API.
TLS material is mounted from an operator-owned Secret.

A 60-second node assignment lease, renewed every 15 seconds, prevents competing
forwarders. This protects active assignments, not network identity restoration.
Forwarders stop advertising before a lost lease expires. An empty node does not erase
another node's healthy application host. New Service hosts require a reachable ready endpoint on their own node. Existing
hosts prefer local endpoints and can use verified ready endpoints from the same
authorized backend on another cluster node during Pod movement. No unrestricted
ClusterIP or arbitrary-address fallback is used. This can add one cross-node hop.
An already advertised listener has a two-minute endpoint-gap grace, evaluated on
successful configuration polls;
lease fencing and authorization withdrawal take precedence.

Pods remain nonroot with dropped capabilities, read-only roots, seccomp, and scoped
NetworkPolicies. There is no TUN, Docker socket, or host networking. Only the public
load balancer adds NET_BIND_SERVICE.

## Routing and lifecycle

Backend address/pool changes keep the existing Service listener and advertisement.
Established TCP connections retain their original backend; new connections use the
current pool. Moving a Pod does not create a new Tailscale Service or identity.

The pinned SDK cannot be assumed to deliver a replacement Service host to an
already-connected client promptly. Approval and configuration acknowledgment are
not client-route acknowledgments. Do not withdraw an old host merely because a
replacement is marked ready. See [handoff verification](verification/service-handoff-20261020.md) and the
[verified ICMP-only peer-discovery remedy](verification/service-peer-discovery-20261020.md).

Ordinary embedded listener shutdown quiesces the acceptor and gives existing streams up to
30 seconds (20 seconds in the forwarder shutdown budget) before final SDK Serve
and advertisement cleanup. Early advertisement withdrawal reproduced a stream
interruption in live testing, so it is deferred until streams finish. This is an
application-level quiesce, not a claim of seamless Tailscale host-election draining.
New connections to a quiescing host can fail until routing selects another host.
Revocation and assignment-lease loss cancel streams immediately. Killing a backend
Pod cannot migrate its established TCP connections; applications still need their
own readiness/termination drain for those connections.

Standard Gateway API/Ingress validation, ReferenceGrant checks, route precedence,
backend identity checks, prefix rewriting, status acknowledgments, and forwarding
header sanitization remain unchanged. Funnel does not claim full Gateway API
conformance. See the capability table in the README.

Ordinary updates preserve application IDs, DNS ledger ownership, control encryption,
and certificates. Network identities change on Pod replacement. Component deletion
stops workloads and completes owned-resource cleanup; failures retain the finalizer
for retry. Delete dependent components before control. External databases and
referenced Secrets are never deleted. A component's Tailscale role authority remains
until its tag/device is removed in Tailscale.

Back up the control database together with matching encryption/storage material.
No database conversion, identity adoption/import, or live migration is performed by
this greenfield runtime refactor. Build and verify development artifacts locally;
deploy only when explicitly authorized.
