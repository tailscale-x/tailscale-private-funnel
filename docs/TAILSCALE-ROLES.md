# Tailscale-native runtime authorization

This is the development architecture. It changes runtime contracts and storage;
there is no enrollment compatibility or automatic live-installation migration.

## Installation boundary

Choose one immutable DNS-label installation slug (default `main`, at most 24 characters).
The control UI and `manage settings role-policy` generate the same copyable JSON
policy. Review and merge it in Tailscale; Funnel never applies ACL changes.

| Tag suffix after `tag:tailscale-private-funnel-<installation>-` | Purpose |
|---|---|
| `issuer` | OAuth credential tag; owns device roles, with no runtime role or application-Service grant |
| `control` | Control device role |
| `loadbalancer` | Public HTTP edge device role |
| `docker-gateway` | Docker discovery and direct backend forwarding |
| `tailnet-gateway` | Selected tailnet backend forwarding |
| `k8s-gateway` | Kubernetes agent and node-local forwarders |
| `control-service` | Stable private management Service |
| `application-service` | Application Services |

Tag owners include `autogroup:admin`. The installation control role also owns both Service tags so its separate Service-management OAuth client can assign them. All tailnet peers can reach the control Service's configured TCP port for tunnel
phone-home bootstrap. Human management RPCs still enforce their separate roles.
Load balancers can reach application Service TCP ports. Control hosts automatically
advertise the control Service; gateway roles automatically advertise application
Services through `autoApprovers.services`.

Load balancers also receive **ICMP-only** access to this installation’s Docker,
Tailnet, and Kubernetes gateway devices. This keeps gateway peers discoverable
before their Service advertisement changes; it opens no device TCP/UDP backend
ports. [Tailscale documents ICMP as sufficient for peer discovery](https://tailscale.com/docs/features/app-connectors).
The Service-specific requirement here is verified by [live host-replacement tests](verification/service-peer-discovery-20261020.md) with the pinned runtime.
Keep this grant alongside the application-Service TCP grant and auto-approvers;
Service approval alone does not guarantee an existing client receives the host route.
There is no general component-device TCP/UDP connectivity. Additional Tailnet
backend access needs explicit administrator-managed grants.

For installation `main`, the application peer-discovery grant is:

```json
{
  "src": ["tag:tailscale-private-funnel-main-loadbalancer"],
  "dst": [
    "tag:tailscale-private-funnel-main-docker-gateway",
    "tag:tailscale-private-funnel-main-tailnet-gateway",
    "tag:tailscale-private-funnel-main-k8s-gateway"
  ],
  "ip": ["icmp:*"]
}
```

Runtime consumers (load balancers and gateways) also need ICMP-only visibility
of replacement **control** Service hosts. Keep this second grant so control updates
do not strand existing clients on a missing Service route:

```json
{
  "src": [
    "tag:tailscale-private-funnel-main-loadbalancer",
    "tag:tailscale-private-funnel-main-docker-gateway",
    "tag:tailscale-private-funnel-main-tailnet-gateway",
    "tag:tailscale-private-funnel-main-k8s-gateway"
  ],
  "dst": ["tag:tailscale-private-funnel-main-control"],
  "ip": ["icmp:*"]
}
```

Use your own installation slug consistently. This rule grants neither management
access nor application TCP/UDP access to gateway device addresses. It is not a
health-server port and requires no additional listener. Do not replace it with
`tcp:*` access to gateway devices. When diagnosing Service turnover, verify the
existing load balancer's peer map and test actual replacement hosts; a newly
joined diagnostic client can mask missing updates on an older client.


Different installations have different tags. Existing broad allows remain effective;
the generated policy cannot subtract access already allowed by your tailnet.

## Authority and coordination

Operational runtime RPCs authenticate the connection with Tailscale WhoIs and check
its exact installation/role tag. Correctly tagged devices register without a
Funnel approval ceremony. Logical component IDs and instance IDs are selectors,
not credentials. Devices sharing a role share that role's authority. The role is
not a per-process security boundary.

A 60-second active-instance lease prevents concurrent writers to one logical
component. An old instance cannot overwrite a replacement. Configuration generations,
report freshness, Docker inventory revisions, backend validation, hostname policy,
and exact DNS-ledger ownership still apply. Removal or failed discovery alone does
not release a Docker claim; confirmed authoritative inventory does.

Runtime tags never grant human viewer/operator/admin permissions. Separate
`users:read` credentials verify human Owner/Admin roles. Disabling a component
withdraws its Funnel resources; removing its tag/device in Tailscale revokes its
role authority. Cleanup never deletes a borrowed device or disapproves another
component sharing that device.

There are no Funnel enrollment codes, signing-key binding APIs, request signatures,
signed configuration envelopes, manual component approval, or child enrollment.
Database credential encryption and Kubernetes TLS/Pod authentication remain.

## Scoped OAuth clients

Use one shared issuer per installation, separate from other credential purposes:

| Purpose | Scope and resource restriction |
|---|---|
| Shared installation issuer | `auth_keys` write, only the installation issuer tag; direct ownership of five device roles |
| Service management | `services` write plus `devices:core:read` for Service-host inventory; separate credential tagged with the installation control role, which directly owns both Service tags |
| Human role lookup | `users:read`; no write access |
| Native load-balancer endpoint discovery | Read-only `devices:core`, load-balancer role tag |

Control stores one installation issuer encrypted. Native control bootstrap and
installer key generation use the same pair. Rotation replaces this pair atomically. Kubernetes CRs reference
existing namespace Secrets with `client_id` and `client_secret`; the operator
never adopts or deletes these credentials. Workloads receive only temporary,
preauthorized, single-use, 15-minute auth keys, never OAuth credentials.

## Shared issuer setup

Create one OAuth client with **Auth Keys write** and exactly
`tag:tailscale-private-funnel-<installation>-issuer`. The generated `tagOwners`
template makes that issuer a direct owner of each of the five device-role tags.
The issuer has no runtime role, application-Service grant, Service tag ownership, or Service
advertisement approval. Existing Service-tag grants and auto-approvers are unchanged.

Every key request specifies one server-selected workload role. Auth keys are
preauthorized, one-use, expire after 15 minutes, and never carry the issuer tag.
`AdvertiseTags` matches that role. Only trusted control/operator infrastructure
holds OAuth credentials; installers and workloads receive temporary auth keys.

Native bootstrap reads protected issuer files and saves the pair encrypted.
The Tailscale settings page configures **Installation key issuer** once, with no
role selector. Kubernetes components share one namespace Secret referenced through
`keyIssuerRef`. Different installation namespaces need their own issuer credentials.

Keep Service management, human role lookup and optional endpoint discovery separate.
The issuer needs no `devices:core`, `services`, `users:read`, `oauth_keys`, or
`policy_file` scope. Client authentication testing does not prove tag delegation;
the first actual key issuance validates that authority with Tailscale.

A shared issuer can mint all five roles in its installation. Its compromise affects
that installation's runtime authority; do not distribute it to gateways or relay
its issuer tag to workload devices. Host reuse does not consume a bootstrap key.

References: [OAuth delegation](https://tailscale.com/docs/features/oauth-clients),
[tag ownership](https://tailscale.com/docs/features/tags),
[scope restrictions](https://tailscale.com/docs/reference/trust-credentials).

## Network transports

`--tailscale embedded` is the native default. It retains tsnet state across native
restarts and discards the bootstrap auth key after joining. Kubernetes uses
embedded ephemeral identities and memory-backed Pod-local state; replacing a Pod
creates a new device without changing logical application/exposure IDs.

`--tailscale host` supports native load balancers and Docker/Tailnet
gateways. It requires a connected, correctly tagged daemon and appropriate
LocalAPI permissions. `--tailscale-socket` selects a non-default socket. Funnel
never joins, renames, retags, logs out, changes operator settings, or deletes that
device. Prepare daemon permissions externally.

Host Service changes use scoped ownership manifests, host locking, and Serve ETags.
Foreign Service configuration is rejected rather than adopted. Unrelated Serve,
SSH, routes, and DNS configuration remain outside Funnel's ownership. Host dialing
resolves MagicDNS through LocalAPI and permits only Tailscale addresses; ordinary
host-network fallback is refused.

The canonical endpoint is the returned tailnet FQDN for
`svc:tailscale-private-funnel-<installation>-control`. Device IPs are diagnostic.
Embedded control uses `ListenService` with mandatory PROXY v2 metadata, resolving
WhoIs against the preserved peer address. HTTP forwarding headers never authenticate
callers. Control remains single-replica.

Host-reused control is currently unavailable. Tailscale 1.102.2 explicitly rejects
PROXY v2 with Unix backend targets; a real-daemon test confirmed the failure. Funnel
rejects this mode before modifying the host rather than using an unprotected TCP or
HTTP-header fallback. Use embedded control. A future protected source-preservation
path must pass real-daemon acceptance before this restriction can be removed.


## Docker packet path

```text
Tailscale Service → tsnet or host tailscaled → approved container IP:port
```

The native supervisor is the Docker API client. It selects allowed user-defined
networks, applies local deny patterns and CIDR checks, validates container identity,
and tests the actual address before advertising. It revalidates replacement and
network/IP changes. Unsupported or unreachable topology produces diagnostics;
there is no fallback to published ports.

No relay image, container, command, or published host-port forwarding is involved.
The earlier truncation was demonstrated on Docker's published-port forwarding path,
not on `0.0.0.0` itself; see the historical [direct-backend record](qa-direct-backend.md).
Caddy remains the sole HTTP proxy and preserves the existing rewriting, trusted
forwarding headers, streaming, and WebSocket behavior.

## Verification boundary

See the [local verification record](qa-tailscale-native-roles.md) for passing gates
and the real-tailnet acceptance checks still required before production use.

## Foreground local tunnels

The default `tunnel` mode phones home through an existing host connection without
requiring a host tag or human management role. Any verified peer allowed to reach
control may obtain a single-use, ephemeral, 15-minute Tailnet gateway key; bootstrap
therefore deliberately grants that installation runtime authority to those peers.
A separate embedded identity serves the tunnel, using memory-backed state. The
borrowed daemon is never retagged, logged out or modified. `--tailscale host` remains
an explicit option for already role-tagged hosts and requests no key.

No additional issuer scope or tag is needed. The generated control-Service grant
uses `*`; application-Service grants and auto-approvers remain role-specific.
Funnel never applies these policy changes automatically. Distinct installation tags
still isolate application traffic, but cannot prevent a reachable peer from
requesting bootstrap from another installation under this chosen trust model.
See [usage and lifecycle](foreground-tunnel.md).
