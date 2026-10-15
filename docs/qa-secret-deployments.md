# Control and load-balancer Secret-backed Deployments

Development migration performed on 2026-10-15. No stable tag or GitHub release.

## Implementation and placement

- Control: local PC k3s, Helm release `control`.
- Public load balancer: xvps k3s, Helm release `edge`.
- Namespace on both clusters: `tailscale-private-funnel-main-system`.
- Each workload is one Deployment replica with `strategy: Recreate`.
- The controller/broker remains a singleton Recreate Deployment; gateway forwarders
  remain a DaemonSet. Existing 5090 and local gateways were retained.
- Precreated identity Secrets contain tsnet state and application identity keys.
  Identity material is not rendered in Helm values or release manifests.
- Control's retained PVC contains only SQLite, its journals, and a storage-binding
  marker. Load-balancer storage contains Caddy certificates, keys, ACME state, and
  its marker. Runtime files use an ephemeral Pod volume.
- Missing, corrupt, or mismatched durable storage fails startup instead of creating
  a replacement identity. Native filesystem execution remains supported.

## Immutable development artifacts

- Version: `dev-secret-deployments-20261015-r2`.
- Image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-secret-deployments-20261015-r2`.
- Image digest: `sha256:7b3334018fabf88248696e3eca26288b901766f619364d0f27c6e506ae3e3c8e`.
- Linux amd64 binary SHA256: `746a8ee7b72da26919e3e35a6bbb45663b33d6088ec0ca34b75d263e451dd593`.
- Chart: `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`.
- Chart version: `0.0.0-dev-secret-deployments-20261015-r2`.
- Chart digest: `sha256:4f7f5cd660f5d0048dad3cf452a918d6550348fb2ff57e0b272a9ae39d1fee75`.

Artifacts were built on the development PC. Neither xvps nor 5090 compiled code.

## Stopped-state migration

Stopped both native processes before taking protected state/data snapshots. Imported
the original identity and matching encrypted database or Caddy storage using temporary
migration Jobs. The new Deployments then started with those Secrets and PVCs. Control's
private URL remained unchanged. The administrator explicitly bound the stopped load
balancer to its new Kubernetes public Service; signed Service observations established
the new Service UID without changing node or DNS ownership.

Provider and zone API snapshots matched before and after migration. Original machine
keys, control signing key, database encryption key, load-balancer report signing key,
and enrollment binding were preserved. Native systemd units were archived and masked
to prevent accidental duplicate identity startup. Protected offline snapshots remain
under `/var/backups/tailscale-private-funnel`.

## Security and networking

Live RBAC checks allow control/load balancer get/update/patch only on their exact
identity Secret. Secret listing, unrelated Secret access, and workload creation are
denied. Load balancer can get only its named public Service. Forwarders cannot read
Kubernetes Secrets. No TUN, host networking, or Docker socket is used by these Pods.

Enabled Kubernetes Secrets encryption at rest on local k3s and xvps. Rewrote only the
local Funnel namespace's Secrets and verified the stored identity record is encrypted;
unrelated namespaces were not bulk rewritten. Cluster encryption keys must be backed up
separately from the encrypted datastore.

The xvps Service exposes TCP 80/443 with `externalTrafficPolicy: Local` and its routable
WAN VIP. Initial installation preserved client IP, but Pod replacement exposed a CNI
host-port NAT ordering problem. On this dedicated cluster, disabled the CNI `portmap`
plugin and retained flannel/bandwidth so kube-proxy handles the direct VIP. This is not
a generic NAT-only ServiceLB setup or a chart default. Repeated public tests compare
`X-Real-IP` and `X-Forwarded-For` against the client's actual WAN address.

VPS firewall permits public 80/443 while blocking WAN Kubernetes API, kubelet, flannel,
and NodePorts. Private administration remains on Tailscale. Firewall loading precedes
k3s at boot. Existing network-online waiting can delay k3s startup after reboot.

## Verification

Passed locally: both Go test suites, `go vet ./...`, Helm lint/package/render contracts,
container startup, and diff checks. New tests cover stopped-state import, encrypted
credential and signing-key preservation, storage binding, wrong/corrupt identity
rejection, singleton Deployment rendering, retained storage, exact-Secret RBAC, and
administrator-only public-Service rebinding with stopped-state confirmation.
Manual zone preview/retry now uses the same signed Service-address source as automatic
reconciliation. Tests reject stale, mismatched, and private-only observations without
falling back to native endpoint discovery.

Live Pod replacement preserved identity Secrets, SQLite configuration, PVC binding,
and the existing Voice certificate. A disposable Kubernetes HTTPRoute obtained a
public certificate and exercised real HTTPS, trusted forwarding headers with malicious
input removed, flushed streaming, and WebSocket echo. Voice's public WSS TTS completed
with 48 audio packets. Flue and Metube remained reachable over validated HTTPS.
After a full xvps reboot, the same machine/application keys and certificate were
preserved; public header, streaming, WebSocket, Voice, Flue, and Metube checks passed
again. Local control was tested through k3s restart and Pod replacement, not a reboot
of the development PC.

Removed the disposable HTTPRoute and workload. Control withdrew the owned DNS record;
the public resolver returned NXDOMAIN and the zone ledger retained the existing
load-balancer A record. Temporary migration Jobs and their RBAC were removed. Provider
and managed-zone snapshots remained equal throughout the cutover.

The final `r2` image is pinned by digest on both Deployments. Its live manual zone
preview resolved the signed Kubernetes Service VIP with zero additions/removals.
Voice WSS TTS, Flue, and Metube passed again after this final upgrade. Temporary source
snapshots and Secret JSON files were removed from the working temporary directory;
protected offline recovery snapshots were retained.

This is singleton recovery, not active/active HA or uninterrupted connections during
Pod or host replacement. Recreate intentionally stops the old process before reusing
its identity. Helm rollback does not restore SQLite; restore a consistent database and
its matching identity Secret when schema rollback is necessary.

final result: passed
