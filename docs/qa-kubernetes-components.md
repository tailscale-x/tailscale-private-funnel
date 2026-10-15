# Kubernetes component verification

Verified locally on 2026-10-15. Existing VPS and 5090 deployments were not changed.
No stable tag or GitHub release was created.

## Development artifacts

- Image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-kubernetes-components-20261015-r3`
- Image digest: `sha256:5638ac425946637e2db0e493a1e8f13ad3c69da70526b2a63f99ba5064a71cbc`
- OCI chart: `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`
- Chart version: `0.0.0-dev-kubernetes-components-20261015-r3`
- Chart digest: `sha256:792b4684b012ba2b1d99ca0d2444ab46af555abb33c6e8fd23ff5f2b9ab8f825`
- Linux amd64 binary SHA256: `2127d57f4c69d2d7f057dae3e259c44e3f9bddc6e44f2690b8935a647e4c066f`

Artifacts were built on the development machine. This verification used Linux amd64;
it does not establish multi-architecture runtime acceptance.

## Disposable cluster

Used k3d with one server and one agent, Traefik disabled, and the bundled k3s
ServiceLB. Control, three independently enrolled load balancers, two controller
StatefulSet Pods, and node-local forwarders shared one installation namespace.

Verified:

- OAuth-only control init and encrypted credential reload from its dedicated PVC.
- Bootstrap Secret removal; control and load-balancer Pod replacement reuse identity.
- Generated, pinned OCI Helm instructions enroll a fresh load balancer end to end.
- A pending external address does not prevent the installer from removing its consumed
  bootstrap Secret. The script waits for StatefulSet readiness separately.
- Independent load-balancer identities and PVCs within the same installation.
- Controller failover: the second controller acquired leadership while the first was
  held terminating. The application Service continued routing to the node-local echo Pod.
- Final-image gateway rollout recovered both forwarders after identity-lease fencing.
- Helm upgrade, rollback to the previous development chart/image, and restore preserved
  the load balancer's Tailscale node identity.
- Public HTTP listener returned a redirect to HTTPS through the local LoadBalancer.
- Private Service status addresses and pending allocation did not produce public DNS.
- Parent revocation cleaned up its children independently from load-balancer revocation.

K3s ServiceLB allocated a private address to the first load balancer; additional public
Services remained pending because its host-port slots were occupied. Funnel did not
substitute outbound, node, Pod, or ClusterIP addresses.

## Live permission checks

| Principal | Operation | Result |
| --- | --- | --- |
| Broker | Patch its controller Pod | Allowed |
| Broker | Patch control Pod | Denied |
| Broker | Create Deployment | Denied |
| Broker | Execute into Pod | Denied |
| Broker | Update PVC | Denied |
| Broker | Read another installation's Secrets | Denied |
| Forwarder | Read Secrets | Denied |
| Forwarder | Read HTTPRoutes | Denied |
| Control | Read Secrets | Denied |
| Load balancer | Read its exact public Service | Allowed |
| Load balancer | Read another public Service | Denied |

Helm rejected a different installation in the occupied namespace and rejected unsupported
replica counts. Control and load balancers do not share broker storage.

## Automated and browser checks

Passed both Go test suites, `go vet ./...`, all three component Helm lint/template
checks, reproducible UI asset checks, container startup, and `git diff --check`.
Focused tests cover endpoint signature/identity/nonce/freshness/Service binding,
public-address filtering, hostname resolution failures, previous-ledger retention,
persistent-state validation, and generated namespace/release instructions.

Chrome checks covered native/Kubernetes load-balancer selection, generated instructions,
control provisioning, and layout at 1440px and 390px. No horizontal overflow or console
errors were observed. The commands referenced the published development chart.

## Acceptance boundary

WAN routing, allocator-specific public client-IP preservation, and real ACME issuance
remain unverified for Kubernetes: the disposable cluster had no routable public
LoadBalancer address. Existing unit/integration tests for routing, headers, streaming,
and WebSockets are not a substitute for this external acceptance gate.

The test HTTPRoute and enrolled nodes were removed through their control lifecycle.
Control reported successful cleanup, including forwarder revocation; its DNS ledger
was empty and its test application Service was absent. The disposable cluster and
remaining test control identity were removed after verification.

Deployment, backups, update/rollback, and explicit removal instructions are in
[KUBERNETES.md](KUBERNETES.md).
