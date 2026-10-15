# 5090 k3s Voice migration — 2026-10-15

This records the initial combined gateway deployment. The current split controller /
node-local forwarder architecture and subsequent reboot tests are documented in
[Node-local Kubernetes verification](qa-node-local-kubernetes.md).

## Artifacts and placement

- k3s: v1.35.9+k3s1, official prebuilt binary.
- Controller: development chart `0.0.0-dev-k3s-security-20261015-r2`.
- Controller image: `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:6795f877de2e67baaaf4385e21bd9693b98daaf7f1e90da6566418fc9bee255a`.
- Chart OCI digest: `sha256:ffa88b66bf4a2f6c2f03905e6e9475e727d59fb49bc11bc71f400b8b3740d878`.
- Voice image: existing image `sha256:c61b8c365c70745cc66e7504cb8cb9dc2ed1a655005a25265c749adf548356cc`; imported, not rebuilt.
- Voice namespace: `voice`; controller/Gateway namespace: `tailscale-private-funnel-system`.

## Verified

- Both Go suites, vet, Helm lint and diff checks.
- Reject forged external EndpointSlice addresses, mismatched owner/Pod UID,
  selector mismatch, unready/host-network Pods and incorrect target ports.
- Restricted Pod Security admits both nonroot deployments.
- Scoped cache and RBAC operate successfully with only two watched namespaces.
- Controller service account cannot read Secrets or list Pods in `default`.
- Unrelated namespace TCP access to Voice ClusterIP: blocked.
- Route from unauthorized namespace: Accepted=False, namespace not allowed.
- Cross-namespace backend without ReferenceGrant: RefNotPermitted.
- Canary HTTPS `/healthz`: 200; route Programmed=True after LB acknowledgment.
- WSS TTS canary: completed, 48 packets, 24kHz output.
- Chart upgrade preserves node identity `nYaTtRJbh811CNTRL`.
- Bootstrap Secret removed after successful enrollment/upgrade.
- k3s Secrets encryption status: Enabled; encryption hashes match.

No stable tag or GitHub release. Security scope and operational limitations are in
`deploy/voice/README.md`.

## Final cutover and cleanup

- Deleted the old tailnet-backed Voice application through control using its
  current revision; control performed ownership-scoped DNS/Service cleanup.
- `voice.troly.ai.vn` now belongs to the Kubernetes-backed application.
- Public HTTPS health: 200. Public WSS TTS: completed with 48 audio packets.
- Restarted gateway after deleting the bootstrap Secret: healthy, same identity.
- Voice restarted on its immutable local image tag and reused the model PVC.
- Canary hostname removed; public resolver returns NXDOMAIN.
- Old Voice Docker container stopped, restart policy `no`; host port 8765 closed.
  Original image, source and model volume retained.
- Existing Flue public `/api/ping`: 200.
- Llama smoke test unavailable: neither alias resolves and no Llama container
  runs on 5090. No Llama container was changed by this task.
- Host SSH agent stalled; bypassing it with the existing local identity restored
  access. Kubernetes and Voice remained reachable.

Initial checks covered controller/Voice restarts; full host reboot results follow below.

## Namespace naming correction

Moved the release and Gateway to `tailscale-private-funnel-system`, updated
Voice network policy and HTTPRoute references, and transferred persistent state
with both controller deployments stopped. The Tailscale identity remained
`nYaTtRJbh811CNTRL`. Removed the former namespace after verified recovery.
Generated Helm enrollment now defaults to the package name plus `-system`.

## Full 5090 reboot verification — 2026-10-15 01:32–01:36 UTC

Result: passed with no manual service restart, reenrollment, Helm operation or DNS
mutation after requesting the reboot.

- Confirmed actual reboot: boot ID changed from
  `5896f6f2-9f57-497b-b9c0-ffb1fa7606d3` to
  `a533cd60-5942-465b-85e4-09eb8eb610e7`.
- Host SSH and active k3s/firewall observed by 139 seconds after reboot request.
- Node Ready; gateway and Voice each Running/Ready after automatic restart.
- Gateway node ID `nYaTtRJbh811CNTRL`, enrollment ID and report signing public key
  unchanged. Fresh signed reports accepted after boot.
- Gateway and Voice PVC UIDs and backing volumes unchanged; model cache reused.
- HTTPRoute Accepted=True, ResolvedRefs=True and Programmed=True; load balancer
  acknowledgment recovered at 01:36:03 UTC.
- Public HTTPS health 200 observed by 220 seconds after reboot request.
- HTTP redirects to HTTPS with 308; WSS Vietnamese TTS completed with 48 packets
  at 24kHz through the public hostname.
- Secrets encryption remains enabled with matching hashes; host firewall table
  restored automatically. Old Docker Voice remains stopped with restart=no.

Recovery included temporary public unavailability and DNS NXDOMAIN while readiness
and control reconciliation converged. These are observed upper bounds from polling,
not exact outage measurements. This is a single-node deployment, so rebooting the
host interrupts Voice until the node, backend and public routing recover.
