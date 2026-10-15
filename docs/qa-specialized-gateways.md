# Specialized gateways verification

Date: 2026-10-14 UTC. Development build; no stable tag or GitHub release.

## Implemented

- Separate Docker, Tailnet, and Kubernetes gateway CLI modes, unified inventory, and guided enrollment.
- Shared persistent tsnet identity, signed configuration/reporting, caller-scoped authorization, Service listeners, health, and revocation.
- Automatic load-balancer assignment; removed client load-balancer selectors and the combined ingress mode.
- Docker relays remain credential-free, unprivileged, socket-free containers on locally selected application networks.
- Kubernetes Gateway API HTTP subset and networking Ingress, typed Caddy options, namespace/reference checks, EndpointSlice validation, status and Events.
- Helm with persistent storage, Recreate updates, separate bootstrap Secret, nonroot security settings, and least-privilege RBAC.

## Artifact

`ghcr.io/tailscale-x/tailscale-private-funnel:dev-specialized-gateways-20261015-r11`

Image digest: `sha256:9bc8aae2cfdf597055fc99d947fa5d2c7d19515aa5b641c792445c7ed88cbdfc`.

Linux amd64 binary SHA256: `4f5f65c30b85a6d7d02a323b820c8b374384c705a003f340ff85eb327f9f0e6c`.

Built locally. Native transfers were gzip-tested, checksum-verified, and atomically installed. No remote compilation.

## Automated gates

Passed `go test ./...`, `go test -tags caddy ./...`, `go vet ./...`, Helm lint/template, generated CSS/templ reproducibility checks, and `git diff --check`.

Coverage includes specialized mode defaults, removed API fields, reusable shared lifecycle, signed snapshot identity/replay rejection, persistent stale-node behavior, migration preservation, backend-loss recovery, current-route acknowledgment, Kubernetes translation/ReferenceGrant/unsupported features, Caddy adaptation, and forwarding-header sanitization.

A protected copy of the deployed SQLite database was migrated locally before rollout; node/report/credential/zone/ledger associations were retained. The copy was removed after verification.

## Live local Kubernetes and public edge

Disposable k3d cluster, k3s v1.31.5, Gateway API v1.5.1 Standard CRDs, Traefik disabled. Helm deployed the prebuilt image.

Verified:

- HTTPRoute `/a` and `/b` reached distinct ready ClusterIP Services.
- Networking Ingress reached its named Service port.
- Public DNS and ACME HTTPS through the existing load balancer.
- Weighted route selected both configured backends.
- Request/response header modifiers and spoofed forwarding-header removal.
- Request-body limit returned HTTP 413.
- Backend-header timeout returned HTTP 504; standard HTTPRoute backendRequest timeout overrode the annotation and returned HTTP 504.
- WebSocket echo and a 1 MiB streamed response.
- Backend removal withdrew its HTTPRoute while the independent Ingress remained available; backend restoration recovered Programmed=True.
- Observed generation advanced only after the current Caddy route configuration was acknowledged.
- Helm upgrade and rollback reused the same node identity after deleting the bootstrap Secret.
- The r9 Helm upgrade retained the node identity. Final r11 adds control cleanup idempotence and provisioning UI options; the Kubernetes forwarding runtime is unchanged from that live test.

Three problems were corrected during testing: disabled applications incorrectly blocked hostname recovery, and stale Caddy acknowledgments could acknowledge a newer route configuration. Cleanup also incorrectly left already-deleted Services pending after HTTP 404. All three have regression tests.

The local shared filesystem triggered kubelet disk-pressure eviction during builds. The eviction threshold was lowered only in this disposable test cluster; pods then recovered. This is an environment adjustment, not an application requirement.

## Native transition

Control and load balancer on xvps, and Docker/Tailnet gateway services on 5090 use the final prebuilt binary. The local development Docker gateway was updated as well. Existing Docker gateway identities were retained, and obsolete ingress units were removed. Flue and MeTube returned HTTP 200 after the transition.

The new 5090 Tailnet gateway is enrolled and reporting peers. Voice migration was pending an externally assigned backend-access tag/grant at initial rollout; it is resolved in the follow-up below. No ACL policy or OAuth scope was changed. Llama was not running on 5090 before this task and was not recreated.

## Browser QA

Chrome MCP tested Docker, Tailnet, and Kubernetes provisioning at 1440×1024 and 390×1024, including generated instructions, pending enrollment, prerequisites, and role-hidden mutation controls. No horizontal overflow or JavaScript console errors. Browser informational issues about CSS @property and autocomplete are not runtime errors. Screenshots use synthetic identities, domains, and bootstrap credentials.

See [screenshots](qa/specialized-gateways/). The Tailnet flow has no Docker prerequisite; Kubernetes credentials are separated from Helm values and runtime mounts.

## Scope

This implementation supports the documented HTTP subset; it does not claim Gateway API conformance. The live test used one load balancer and one Kubernetes gateway. Multiple-edge selection, authorization failures, unsupported features, and namespace constraints also have automated coverage, but are not a full multi-cluster conformance run.

## Disposable resource cleanup

Deleting the HTTPRoute disabled only its application and removed only its owned hostname record; the independent Ingress and shared backend remained available. Deleting the remaining Ingress withdrew its listeners, removed its owned DNS, and deleted unused application Services. Both disposable hostnames returned no A record from 1.1.1.1. Helm, its PVC/namespace, and the dedicated k3d cluster were removed. Control revocation deleted the Kubernetes device; the final idempotent cleanup retry is recorded below.

UI provisioning was additionally checked with selected Kubernetes namespace/classes. The generated Helm values and Gateway/Ingress examples matched the selections. The Tailnet details link preselected the gateway and populated the machine dropdown.

Final r11 cleanup retry returned HTTP 200 with `pending=false`. The final native binary version was verified on xvps and 5090, and the managed Docker relay image on 5090 is r11. Temporary enrollment scripts, bootstrap JSON, serialized control database copy, and remote binary transfer files were removed. Persistent runtime state and original credential files were retained.

Final post-deployment checks: Gateways UI HTTP 200, Flue HTTP 200, MeTube HTTP 200, MeTube DNS resolves to the existing public edge, and both disposable Kubernetes DNS names remain absent. A restart temporarily withdraws unreachable backend contributions until fresh gateway/LB reports and Service readiness converge; this recovery was observed during rollout.

## Tailnet Voice follow-up — 2026-10-15 (Asia/Saigon)

After the administrator assigned `tag:dlkn-server`, the 5090 Tailnet gateway reported both that tag and the application base tag, and discovered the selected backend device. The control-requested signed probe to its existing HTTP port 8765 succeeded.

The existing Voice application was updated through the control API, retaining its immutable application ID and public hostname. Control provisioned the tagged application Service, approved the exact gateway device, and reconciled owned DNS. No ACL changes, broader OAuth permissions, or direct DNS mutations were used.

Live verification uncovered and fixed a load-balancer reporting defect: tailnet-backed reports omitted their Service name, preventing publication readiness. A regression test now checks that report identity. Both Go suites, vet, and diff checks pass. Control/load balancer on xvps use prebuilt `dev-specialized-gateways-20261015-r12`; gateway binaries remain r11 because their runtime is unchanged.

Development image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-specialized-gateways-20261015-r12`.
Digest: `sha256:e28eaded80c8d4423ac0c62b84eda54c0badf727a79fc9114b0ec5cb95390acc`.
Native binary SHA256: `a0a9846ce23f8c4b7b3517bd9dd30b2ed90dc2e27a8988abd9277d7ef265d2c5`.

Verified public DNS, HTTPS 200, HTTP 308 to HTTPS, and identical public/backend page content (SHA256 `8400f2d1a8fd78ff6b6bfd9d106add17c4741b7bf080fafafc38b7ab3fef3826`). Restarting the Tailnet gateway retained the same Tailscale device identity, both tags, fresh peer discovery, and public HTTPS 200. Flue and MeTube also returned HTTP 200 afterward. This verifies routing and page delivery; no real voice session was started. No stable tag or GitHub release was created.
