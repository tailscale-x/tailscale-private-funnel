# Node-local Kubernetes gateway verification — 2026-10-15

## Implementation and deployed artifacts

Kubernetes reconciliation runs in a two-replica controller Deployment with leader
election. A DaemonSet provides one persistent Tailscale identity per installation
and Kubernetes Node UID. Only verified, ready local Pod endpoints are eligible;
application ClusterIPs are never used as a fallback.

- Final control development binary: `dev-node-local-20261015-r13`.
- Control binary SHA-256: `fedce3d8451d81fea8578fda8a16ee169315893e84d591f66f7d5e13e5c353f0`.
- Development image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-node-local-20261015-r13`.
- Image digest: `sha256:b5838708d56bca69378eda7b3df22cbbc67d0f01814aeb67b4b87519aaff50a1`.
- OCI chart: `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`, version
  `0.0.0-dev-node-local-20261015-r13`.
- Chart digest: `sha256:c958c5862b6449d63b4f82ed4eb9aa6fa5401a34ae1ac4ddcc52016c28fa8f64`.
- 5090 runs the reboot-tested chart `0.0.0-dev-node-local-20261015-r12`, image digest
  `sha256:bb209d7cb15854e881d3a171aecd0e1b27a4430eaa7fef3e4465a1200d3702a3`.
  The subsequent r13 change affects control-side installation revocation only;
  controller and forwarder runtime behavior is unchanged.
- All builds ran locally. No compilation on 5090 or the VPS. No stable tag or
  GitHub release was created.

## Automated gates

Passed `go test ./...`, `go test -tags caddy ./...`, `go vet ./...`, Helm lint and
rendering, reproducible local asset generation, and `git diff --check`.

Focused coverage includes verified Pod/Node identity and exact target ports,
empty endpoint intent retention, local-only endpoint selection and balancing,
HTTPRoute status isolation with optimistic conflict retries, child enrollment
key binding, sibling report aggregation, logical installation ownership,
revocation isolation, and failed state persistence.

Broker tests reject wrong accounts/audiences, forged Pod UIDs, wrong DaemonSet
owners, unknown nodes, configuration reads without an owned lease, cross-installation
access, expired leases, and concurrent ownership. A terminating Pod can flush state
and release its lease, but cannot acquire or renew ownership. No auth key is stored
in broker identity Secrets.

CI now loads and starts its built Docker image, in addition to compiling the static
binary. A discarded local candidate without `CGO_ENABLED=0` failed this startup
check; the working deployment was restored before continuing.

## Multi-node k3d

Used a disposable three-node k3d cluster, k3s `v1.35.9-k3s1`, with Traefik disabled.
Two installations used separate namespaces, class names, controller identifiers,
credentials and Funnel controls. Both watched the same application namespace.

- A shared HTTPRoute explicitly attached to both Gateways retained exactly two
  independent controller status entries, including after leader replacement.
- Control A had the existing public load balancer and reached Programmed=True.
  Control B intentionally had no public load balancer: its Service approvals and
  routing intent were verified; Programmed=False accurately reflected that missing edge.
- Echo backends returned the serving Kubernetes node name. Moving the only ready
  backend from agent 1 to agent 0 changed both direct Service and public HTTPS
  responses to agent 0. The server node never advertised a remote backend.
- Removing every ready backend withdrew advertisements. Restoring a backend reused
  the same application Service and TailVIP rather than deleting/recreating it.
- Pod replacements and controller failover retained each child's enrolled node ID,
  signing key and Tailscale identity. Empty reports and revocation of one child did
  not erase a healthy sibling or affect the other installation.
- Taking both controllers offline stopped running forwarders before their 60-second
  leases expired. They resumed with the same identities when controllers returned.
- Kubernetes authorization denied forwarder Secret reads and denied controller A
  Secret access in installation B's namespace.
- Public echo tests preserved the original Host, actual client IP, HTTPS protocol,
  forwarded hostname and port, and X-Real-IP. Injected Forwarded and X-Forwarded-*
  metadata was removed. Public WebSocket echo and flushed HTTP streaming passed.

This verifies node-local selection, not geographic affinity between Tailscale hosts.
It is not a complete Gateway API conformance or long-duration load test.

## 5090 migration and reboot

Migrated release `private-funnel` in `tailscale-private-funnel-system`. Stopped the
combined gateway before importing its identity bundle into the controller Secret;
no concurrent processes shared its identity. Retained the logical application,
Service and DNS ownership for Voice. The forwarder gained one independent identity.
The former gateway PVC remains retained offline; no active controller or forwarder
mounts it. Historical state was not deleted.

The controller's Tailscale identity remained `nYaTtRJbh811CNTRL`; the forwarder's
identity remained `nWSW29ZgjE11CNTRL` across replacement and reboot. Signing-key
hashes, Voice model PVC and Service TailVIP were unchanged. Both controllers and
the forwarder recovered automatically with fresh signed reports and route conditions
Accepted=True, ResolvedRefs=True, Programmed=True.

Two full reboot passes were run. The final pass changed boot ID from
`2227ed36-cd51-4852-b97b-b9cd9a473f96` to
`95628890-8f26-44c7-b9fc-080d11695351`. No manual workload restart, reenrollment,
Helm operation or DNS mutation was needed after the reboot request.

- HTTPS health returned 200; HTTP redirected with 308.
- Public Voice WSS TTS completed with 48 audio packets after each recovery.
- Final WSS verification resolved the hostname through fresh public DNS and verified
  the normal TLS server name. The test machine and cluster resolver retained a stale
  NXDOMAIN from the outage; Google and Cloudflare public DNS both returned the owned
  A record. DNS negative caches can outlast application recovery.
- Existing Flue `/api/ping` remained operational.
- k3s Secrets encryption and the host firewall recovered. Original Docker Voice
  stayed stopped with restart policy `no`.
- Voice NetworkPolicy now permits the installation's forwarders, not controllers.
- Identity Secrets contained zero persisted `tskey-` bootstrap keys.

Single-node 5090 is not highly available. Host reboot interrupts Voice, existing
connections can terminate, and readiness/DNS convergence takes time. No connection
migration or zero-downtime recovery claim is made.

## UI and cleanup

Reviewed enrollment and node diagnostics in Chrome at 1440px and 390px. Invalid
node-selector JSON showed an inline error without creating enrollment. Corrected
revision wrapping and misleading controller listener counts. Diagnostics have no
horizontal overflow at 390px, distinguish controller coordination from forwarding,
and show per-node identity, health, local endpoint and Service counts. No console errors.
Screenshots stayed outside the repository because they include live infrastructure.

Revoked both disposable installations through their own control APIs, removed their
Tailscale devices and Services, verified disposable DNS NXDOMAIN, deleted the k3d
cluster, and removed the temporary second control identity. Revoked inventory/audit
history remains intentionally retained. Existing production exposures were preserved.

final result: passed
