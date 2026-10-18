# Operating Tailscale Private Funnel

## Roles and network boundaries

The public interface separates deployment infrastructure from installation management:

```text
tailscale-private-funnel native controls|gateways|loadbalancers ...
tailscale-private-funnel k8s operator|controls|gateways|loadbalancers ...
tailscale-private-funnel manage ...
```

Native gateways use `--type docker` or `--type tailnet`. Kubernetes gateways use
an operator-managed agent and forwarder DaemonSet. Runtime serve/bootstrap
commands are internal to generated systemd units and Pod specifications.

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
OAuth-file references through `native controls create NAME --output script`.
Execute its generated installer on the target with the protected files present.
Creation only generates output; it never installs on the invoking machine.

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
only explicit deployment deletion or access revocation deletes a persistent identity.

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

See [Kubernetes installations](docs/KUBERNETES.md) and the generated
[CRD reference](docs/KUBERNETES_API.md). Install one singleton Recreate operator
per cluster, explicitly granting it installation namespaces. The public OCI chart
installs only that operator; `k8s controls/gateways/loadbalancers create` generates
installers that create component CRs when executed. Every operation requires `--context`.

Components live in `tailscale-private-funnel-<installation>-system`. Control and
load balancers use Recreate Deployments with separate identities. SQLite and
Caddy certificates live on component PVCs. A FunnelGateway owns a gateway-agent
Deployment, broker, and forwarder DaemonSet. The agent and per-node identities
persist in namespace-scoped Secrets; enable Kubernetes Secrets encryption and
include these Secrets in cluster backups. Never share an identity concurrently.

Each installation has independent classes, credentials, Secrets, and leases.
GatewayClass parameters reference its FunnelGateway. HTTPRoutes can explicitly
attach to multiple installations while retaining isolated status entries.

Forwarders have no Kubernetes API permissions, Docker socket, TUN, host network,
or host mounts. Audience-bound Pod tokens authenticate to the TLS broker. The
broker verifies Pod UID, DaemonSet ownership, installation and Node UID before
releasing identity/configuration and renewing its 60-second fencing lease. Leases
renew every 15 seconds; a lost lease withdraws advertisements before expiry.

Only verified ready Pod endpoints on the forwarder's own node are selected.
No local backend means no Service advertisement, with no cross-node fallback.
Multiple healthy forwarders advertise the same installation-owned Service.
Tailscale selects the Service host; geographic affinity is not guaranteed.
Broker Secret/lease permissions are installation-scoped. Routing/backend metadata
reads are cluster-scoped; watch namespace filters do not replace allowedRoutes,
ReferenceGrant, exposure policy, or backend validation. Brokers cannot modify
workloads, execute in Pods, or patch control Pods.

Use the web UI to generate commands, then run `k8s … create --output script` from an
authorized workstation and transfer its output to a machine with cluster access.
Control never receives kubeconfigs. Credentials enter
owned bootstrap Secrets rather than CRs or Helm values; successful bootstrap
removes those Secrets. See [operator verification](docs/qa-kubernetes-operator.md).

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

## Updates, deletion, and troubleshooting

Build on the development machine or CI, never the VPS. Use matching pinned
binaries, images, and operator charts. Native deployments use system-level units
and explicit Linux configuration/state paths.

```text
native <resource> get|update|start|stop|restart|logs NAME
native <resource> delete NAME --yes
k8s <resource> get|update|delete NAME --context CLUSTER --namespace NAMESPACE
```

Native stop preserves identity and data. Delete performs owned remote cleanup
before removing local state. Revoke node access withdraws authorization; it does
not prove the host or cluster deployment was removed. Failed cleanup retains
state with diagnostics. Kubernetes finalizers stop workloads, finish cleanup,
then remove only owned Secrets, leases, certificates, and PVCs. External references
are not implicitly owned. Control deletion is blocked while components or cleanup
tasks remain; operator deletion refuses while component CRs remain.

`get` inspects actual service status without starting a competing tsnet identity.
Updates preserve identity; lost or corrupt initialized state fails closed. There
are no adoption, import, resume, public join/reset, or compatibility workflows.
Replace old installations by explicit cleanup followed by fresh provisioning.

For Kubernetes, inspect component CR conditions plus Gateway/HTTPRoute status
and Events. For Docker, check label syntax, selected user-defined networks,
deny patterns and relay backend probes. For tailnet backends, verify effective
tags, peer freshness and the selected HTTP port. Visibility does not prove access.

For rollback use the previous pinned artifact and a matching SQLite/identity
backup when schemas require it. Keep private backup credentials out of logs.

## Removing temporary administrator access

Access roles default to `autogroup:owner`. A tagged diagnostic machine does not
inherit a human account role. Remove temporary node selectors after provisioning.
Ordinary saves reject removing your own administrator access. To intentionally
remove it, use the Access Roles confirmation checkbox or
`manage access save --confirm-self-removal --file access.json`.
The replacement must retain `autogroup:owner`; control verifies a real active or
idle Owner through Tailscale `users:read` before saving and auditing self-removal.
Failure leaves roles unchanged. This confirmation is not stored as an access
setting. Later requests from the removed identity are denied.

## v3 release and recovery

See [v3.0.0 release notes](docs/releases/v3.0.0.md) for the breaking CLI/operator
contract and [release acceptance](docs/qa-v3.0.0-release.md) for measured limits.
If an installation database is lost, a fresh control cannot prove ownership of old
DNS records or enrollments. Re-enroll nodes and explicitly approve only the exact
DNS previews you intend to reclaim. Do not reconstruct ownership from a hostname
prefix or remove unrelated records. A removed cluster is not a recoverable control
backup; keep SQLite and its encryption/signing keys together.

## Installer generation

All public native and Kubernetes `create` commands now generate installers.
Run non-control creation from an authorized machine, then transfer its secret
15-minute installer to the execution host. Targets join embedded tsnet before
contacting control. See [the installer workflow](docs/INSTALLER-WORKFLOW.md).
