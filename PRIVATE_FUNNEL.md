# Operating Tailscale Private Funnel

## Roles and network boundaries

The executable has five top-level modes:

```text
tailscale-private-funnel control ...
tailscale-private-funnel loadbalancer ...
tailscale-private-funnel docker-gateway ...
tailscale-private-funnel tailnet-gateway ...
tailscale-private-funnel k8s-gateway ...
```

Control owns enrollment, policy, application Services, signed configuration, and DNS.
Load balancers terminate public HTTPS. Gateways forward TCP through application
Tailscale Services; they do not add an HTTP proxy hop. All nodes use persistent,
independent tsnet identities and `tag:tailscale-private-funnel`.

Configure the existing shared-tag grant in Tailscale. Local Funnel never writes ACLs.
It cannot reach unrelated machines unless your Tailscale policy permits that traffic.
Management remains tailnet-only and separately authorized through verified WhoIs.
Human Owner access defaults to `autogroup:owner`; role verification requires OAuth
`users:read`. Tagged nodes never inherit the human owner's management role.

OAuth needs `auth_keys`, `devices:core`, and `services` for the shared tag, plus
`users:read` for human role verification. Credentials stay encrypted in control SQLite.
For native control, back up both `funnel.db` and `master.key`. In Kubernetes, back up
the SQLite PVC and matching identity Secret; the encryption key is in that Secret.

## Bootstrap control, then use the UI

Download the built release binary using the README installer. Bootstrap with protected
OAuth files through `control join`; use its wizard for credential, tag, and grant guidance.
Only control has an interactive setup flow.

In the control UI:

1. Configure Tailscale and administrator roles.
2. Add a DNS provider, discover existing zones, and add zone assignments.
3. Add hostname exposure rules. Optional gateway/role selectors restrict which gateways
   may publish matching names. Empty selectors allow any enrolled gateway; no matching
   enabled rule means deny.
4. Open **Gateways → Add gateway** and choose Docker, Tailnet, or Kubernetes.
5. Follow the generated install instructions and wait for identity and signed reports.

Gateway setup never asks for a load-balancer ID or OAuth secret. Control automatically
assigns each exposure to all eligible public load balancers. Health and verified public
IPv4 discovery determine DNS publication. DNS multi-address publication is not a client
failover guarantee.

Bootstrap keys are one-use, preauthorized, non-ephemeral, and expire after 15 minutes.
The installer keeps them out of saved configuration and systemd units. Enrollment codes
are consumed at signing-key binding. Revoke lost/expired enrollments and create fresh ones.
Ordinary stop/reboot preserves identity. Stale reports withdraw traffic after five minutes;
only explicit reset or revocation deletes a persistent Tailscale identity.

## Docker gateway

Install the generated native systemd service on a Docker host. The supervisor is the only
Docker API client and needs the host user's Docker permissions.

Before starting or updating a native Docker gateway, pull its matching versioned Funnel
image into **Docker** for relay creation. A k3s/containerd image cache does not satisfy
this requirement. Verify the published digest before tagging or starting it:

```sh
docker pull ghcr.io/tailscale-x/tailscale-private-funnel:<version>
```

The supervisor uses this image for credential-free relays; it does not build images.

Label HTTP applications:

```yaml
labels:
  tailscale-private-funnel.expose: >-
    8080:checkout.example.com:http,
    8443:admin.example.com:http
```

Use user-defined application networks. The default bridge, `host`, and `none` are excluded.
Local deny patterns and CIDR-overlap validation apply before forwarding. Alias label keys
are configurable; conflicting alias values are rejected. Generic Caddy labels are not used.

One isolated relay is created per target/backend assignment, attached only to selected
application networks, with a dynamically allocated loopback port. Relays run raw TCP,
nonroot, read-only, with dropped capabilities. They receive no Docker socket, signing key,
Tailscale auth key, or tsnet state. Backend reachability is checked inside each relay.
Removing a label or replacing the target withdraws its listener and relay.

## Tailnet gateway

Docker is not required. After enrollment, assign additional tags/grants externally in
Tailscale if this gateway needs access to other machines.

Open **Exposures → Expose a tailnet machine**. Choose the gateway, a machine from its
signed peer snapshot, an HTTP port, and a policy-approved public hostname. Check policy
and run a successful connection test before saving. Peer visibility alone does not prove
port access. Selection is bound to stable device identity; address changes are resolved
through that gateway's tsnet state. There is no ordinary-network fallback.

## Kubernetes control, load balancers, and gateways

See [Kubernetes installations](docs/KUBERNETES.md) for control bootstrap, additional
public LoadBalancer Services, persistence, permissions, and migration. The OCI chart
supports `component: control | loadbalancer | k8s-gateway` and `installation: main`.

### Kubernetes gateway and Helm

Use the commands generated by **Gateways → Add gateway → Kubernetes**. They install
pinned Gateway API v1.5.1 Standard CRDs, create a bootstrap Secret, and install the chart
from `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`, pinned to the selected
image version. No repository checkout is required. Release `vX.Y.Z` uses chart version
`X.Y.Z`; the packaged chart defaults to image `vX.Y.Z`. Development image tags map to
`0.0.0-<dev-tag>` and require that matching chart to be published separately. The script
checks chart availability before creating cluster resources or bootstrap credentials.
Development chart `0.0.0-dev-oci-helm-20261015-r2` is published and verified with k3d. No stable chart or GitHub release containing these changes has been published.

Version-tag CI packages the chart, publishes it to GHCR, verifies the OCI pull against
the package, and attaches the `.tgz` to the GitHub release alongside checksums and chart
metadata. The source remains under `charts/tailscale-private-funnel`. The GHCR chart package is public and linked to this repository. Keep repository Actions
package access enabled for version-tag publishing.

For a published release, inspect its chart without installing:

```sh
helm show chart oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel --version X.Y.Z
```

For local packaging only (no publication):

```sh
scripts/package-chart.sh vX.Y.Z /tmp/private-funnel-chart
```

Credentials do not belong in Helm values. The chart references `bootstrap.existingSecret`.
Delete the bootstrap Secret after the controller joins and forwarders report healthy.
The chart runs a one-time bootstrap Job, one controller Deployment with Recreate updates,
and a node-local forwarding DaemonSet. Controller and per-node Tailscale identities
persist in installation-scoped Kubernetes Secrets; enable Kubernetes Secrets encryption
and include these Secrets in cluster backups. State loss requires explicit reenrollment.

Each installation uses `tailscale-private-funnel-<installation>-system`, with `main`
as the default installation. Trusted control, load balancers and gateways share this
namespace; different controls use separate installations.
Each installation has independent classes, credentials, Secrets and leases. The default
controller identifier includes the namespace and Helm release name. An HTTPRoute can
explicitly attach to Gateways from several installations; status entries remain separate.

Controller pods watch and validate Kubernetes resources and publish routing intent.
Forwarders have no Kubernetes RBAC bindings, Docker socket, TUN, host networking or
host mounts. They use audience-bound pod tokens to reach the controller's TLS broker.
The broker verifies Pod UID, DaemonSet ownership and Node UID; only then can that pod
load its node's Secret-backed identity and signed routing configuration, and renew its
60-second lease. Configuration remains signed by Funnel control; the broker does not
replace the control signing authority. Reports and acknowledgments use the forwarder's
own tsnet HTTP client and verified WhoIs identity. Leases are renewed
every 15 seconds; advertisements stop before lease expiry when renewal fails.
The controller's Secret/lease permissions are restricted to its own dedicated namespace;
TokenReview creation and read-only node/class discovery are cluster permissions.

Only verified ready Pod endpoints on the forwarder's own node are selected. Nodes with
no local backend withdraw the Service advertisement; there is no cross-node fallback.
Multiple healthy forwarders advertise the same installation-owned Tailscale Service.
Tailscale host selection determines the network path, so this does not promise geographic
affinity. Application Services and DNS ownership remain stable across forwarder restarts.

Use `watchNamespaces`, `forwarder.nodeSelector`, `forwarder.affinity` and
`forwarder.tolerations` in Helm values to restrict discovery and placement. Watched
namespaces must already exist. Chart NetworkPolicies isolate the identity broker and
forwarder ingress; application namespaces should separately permit backend traffic from
this installation's forwarder pods. All pods run nonroot with dropped capabilities,
RuntimeDefault seccomp and read-only root filesystems.

Default GatewayClass and IngressClass: `tailscale-private-funnel`; choose unique names
for additional installations. CLI runtime commands are `k8s-gateway controller serve`
and `k8s-gateway forwarder serve`; generated Helm instructions handle enrollment.

### Supported HTTP subset

| Capability | Support |
| --- | --- |
| Gateway HTTP listener | Port 80; HTTPS terminates at external Caddy |
| HTTPRoute matches | Explicit hosts; Exact/PathPrefix; methods; exact headers/query |
| HTTPRoute backends | Weighted internal HTTP Services; verified node-local Pod endpoints |
| Filters | Request/response header modifiers; scheme/host/port redirects |
| Namespace boundaries | Gateway allowedRoutes and backend ReferenceGrant |
| Ingress | Explicit hosts, Exact/Prefix, named/numeric Service ports |
| ImplementationSpecific path | Treated as Prefix |
| Public certificate Secrets / HTTPS listeners | Unsupported; Caddy manages public TLS |
| Wildcard/hostless routes, regex, custom backend kinds | Unsupported |
| ExternalName, headless Services, external-address backends | Unsupported |
| Backend-specific filters, path-rewrite redirects, raw Caddy snippets | Unsupported |

This is a practical subset, not a Gateway API conformance claim. Unsupported configuration
is rejected in resource conditions/Events. `Programmed=True` requires control authorization,
backend/Service readiness, and load-balancer acknowledgment.

Supported annotations on HTTPRoute/Ingress:

| Annotation | Value |
| --- | --- |
| `tailscale-private-funnel.io/request-timeout` | Go duration, e.g. `30s`; `0s` disables |
| `tailscale-private-funnel.io/backend-timeout` | Response-header timeout, e.g. `5s` |
| `tailscale-private-funnel.io/max-request-body-bytes` | Positive byte count |

Gateway annotations provide defaults; route annotations override them. HTTPRoute timeout
fields take precedence. Unknown project-prefixed annotations are rejected. Standard API
routing fields should be used rather than vendor annotations where available.

## HTTP trust and DNS ownership

Caddy strips incoming `Forwarded`, `X-Forwarded-*`, and `X-Real-IP`, then generates trusted
client metadata, preserves public Host, and sets forwarding port and protocol. Route
header modifiers cannot replace these trusted headers. Gateways and relays preserve raw
TCP, including streaming and WebSocket upgrades.

Public exposure A records contain deduplicated ready load-balancer addresses. Each load
balancer also has its owned public hostname A record. Backend addresses and Service names
are private; no public backend A or SRV records are required.

Control records exact ownership in its ledger. It never deletes unmanaged records.
Conflicts require an administrator's explicit approval bound to a specific preview. No TXT
markers or user-supplied ownership IDs are used. Missing/ambiguous endpoint discovery
retains the last owned address. Use **Zones → DNS details** for preview and recovery.

## Updates, reset, and troubleshooting

Build on the development machine or CI, never the VPS. Native services use persistent
state/data paths; Docker/Helm deployments use pinned images and persistent volumes.
Verify checksums before atomically replacing a binary. Keep image digests for rollback.

`<mode> status` reads local runtime status without opening a competing tsnet identity.
`<mode> reset --force` is explicit identity deletion; control revocation withdraws traffic
and performs scoped cleanup. Cleanup failures remain pending for retry.

For Kubernetes, inspect `kubectl describe gateway`, `kubectl describe httproute`, and Events.
Ingress exposes address status; Gateway/HTTPRoute expose conditions. For Docker, check label
syntax, selected user-defined networks, deny patterns, and relay backend probes. For tailnet
backends, verify effective tags, peer freshness, and the selected HTTP port.

The old `ingress` command and load-balancer selectors are removed. Existing Docker-only
nodes transition with their identity preserved. Tailnet exposure uses a separately enrolled
Tailnet gateway; never run two processes against one tsnet state directory.

### Namespaced Kubernetes workload example

The [Voice deployment](deploy/voice/README.md) places application pods in `voice`
and the gateway controller/Gateway in `tailscale-private-funnel-main-system`, with restricted Pod Security,
namespace-scoped controller RBAC/cache, verified Pod-backed endpoints and explicit
NetworkPolicies. [Verification](docs/qa-k3s-voice.md) records checks and limits.
Namespace placement alone does not isolate network traffic.
