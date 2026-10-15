# Development rollout: xvps, 5090, and local k3s

Date: 2026-10-15. Development artifacts only; no stable tag or GitHub release.
All compilation and fixture image builds ran on the development machine.

## Deployed

Artifact: `dev-kubernetes-components-20261015-r3`.

- Image digest: `sha256:5638ac425946637e2db0e493a1e8f13ad3c69da70526b2a63f99ba5064a71cbc`.
- Native amd64 binary SHA256: `2127d57f4c69d2d7f057dae3e259c44e3f9bddc6e44f2690b8935a647e4c066f`.
- OCI chart: `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`,
  version `0.0.0-dev-kubernetes-components-20261015-r3`.

| Host | Deployment |
| --- | --- |
| xvps | Existing native control and public load-balancer systemd services |
| 5090 | Native Docker/Tailnet gateways; Kubernetes controller StatefulSet and forwarder DaemonSet |
| This PC | New Kubernetes gateway, release `tiny-gateway`, with two controllers and a forwarder |

Native services were stopped before binary replacement and a consistent state/data
backup. xvps remains native by explicit user choice; no k3s was installed there.

5090's old controller Deployment and forwarder DaemonSet were stopped before exporting
identity Secrets. Imported controller/forwarder state into
`tailscale-private-funnel-main-system`, then started the new StatefulSet/DaemonSet.
Preserved cluster ID, enrollment IDs, report signing keys, Tailscale node IDs,
application identity, and Voice Service TailVIP. Updated Gateway references and Voice
NetworkPolicy to the new namespace. The previous Deployment, DaemonSet and Gateway
were removed after successful recovery. Historical PVC/rollback state remains offline.

The existing controller identifier was deliberately retained during migration; the
GatewayClass identity and application/Service ownership were not recreated.

The local gateway watches `omni-development` and `funnel-verification`, with class
`tailscale-private-funnel-tiny`. Other application resources were not changed.
Bootstrap Secrets were removed; runtime identity Secrets contain no persisted auth keys.

## Public acceptance

A disposable HTTPRoute on this PC exposed an echo workload through the existing xvps
public load balancer. Control created and removed its DNS; no direct provider mutation.
Verified real Let's Encrypt HTTP-01 issuance and normal TLS certificate validation.

Verified through the public HTTPS edge:

- Node-local response from the local k3s workload.
- Original Host, X-Forwarded-Host, HTTPS protocol, public port, real client IP and X-Real-IP.
- Spoofed Forwarded and X-Forwarded-* metadata removed before trusted headers were generated.
- Flushed HTTP streaming and WebSocket echo.
- HTTP redirect to HTTPS.
- Voice HTTPS health and public WSS TTS with 48 audio packets.
- Existing Metube HTTPS.
- Flue HTTPS `/api/ping`, including its local model status.

The disposable HTTPRoute/workload was removed. Its DNS returned NXDOMAIN, its Tailscale
Service was absent, and its internal application became disabled. The local gateway
stays installed; the test does not leave a public endpoint behind.

## Restart acceptance

- Rebooted 5090 and xvps; compared actual kernel boot IDs.
- All eight enrolled node IDs, Tailscale node IDs and report signing keys were unchanged.
  The control node's Tailscale ID also remained unchanged.
- Services and Kubernetes workloads recovered automatically, without reenrollment.
- Public Voice WSS, echo streaming/WebSockets and header checks passed after both reboots.
- The existing Voice certificate fingerprint and validity dates were unchanged after xvps reboot.
- Replacing this PC's forwarder Pod and upgrading its Helm release preserved its identity.
  This PC itself was not rebooted.

xvps has an existing systemd network-online wait that times out after about two minutes.
Its enabled Funnel services then start automatically. Single-host outages, temporary
route withdrawal and DNS negative-cache delays occurred; this is recovery, not zero downtime.

A final Docker routing check found that 5090 lacked the matching relay image in Docker's
cache: k3s/containerd's copy is independent. Pulled the same published digest into Docker
and verified Flue. The relay runs as UID/GID 65532, unprivileged, with no mounts and all
capabilities dropped. Native Docker gateway updates must preload the matching relay image.

Second 5090 reboot acceptance: passed. Voice HTTPS/WSS and Flue `/api/ping`
recovered automatically with the cached relay image. Controller/forwarder and native
gateway identities remained unchanged; the old controller Deployment and DaemonSet
did not return. No manual service restarts or reenrollment were performed.

## Boundaries

Public acceptance used the native xvps load balancer. It does not establish WAN acceptance
for a Kubernetes LoadBalancer allocator or allocator-specific client-IP preservation.
No ACL changes, OAuth scope expansion, or unrelated workload migrations were performed.
Backups contain identity/credential material and remain protected outside the repository.
