# Voice on the 5090 k3s host

Voice runs in `voice`; the gateway controller and Gateway run in `tailscale-private-funnel-main-system`.
Public TLS stays on the existing external load balancer. Kubernetes has no public
LoadBalancer/NodePort Service, Docker socket mount, TUN device, or host-network pod.

## Apply and maintain

Use an explicit kubeconfig for this cluster. Apply `workload.yaml` and
`network-policy.yaml`, install the Kubernetes operator, then run `k8s gateways create site` with an
explicit context, namespace `tailscale-private-funnel-main-system`, classes
`tailscale-private-funnel`, and `--watch-namespace voice`. Apply `routes.yaml`
after the component reports ready. See [operator deployment](../../docs/KUBERNETES.md).

The Voice image is imported from the existing Docker deployment, with the fixed
local tag `localhost/voice-server:sha-c61b8c365c70` and `imagePullPolicy: Never`.
A replacement node needs that image imported before scheduling the workload.
The model cache is on the `voice-models` PVC; the original Docker volume is retained.
This is a single-node deployment, not highly available. Back up the PVC and k3s
state before deleting the host or cluster.

The control UI generates CLI instructions without minting enrollment. The CLI
authorizes enrollment and creates an owned bootstrap Secret; the operator removes
it after initialization. A bootstrap Job persists the agent bundle and a Recreate
Deployment uses its Secret-backed identity.
The forwarding DaemonSet owns one additional persistent identity per selected node.
Identity Secrets replace the former gateway PVC and must be backed up with the cluster.
Only the controller broker can read/update its namespace's identity Secrets. Forwarders
mount only the broker CA and have no Kubernetes API permissions.

## Security boundaries

- Both namespaces enforce the Kubernetes **restricted** Pod Security Standard.
- Workloads run nonroot with seccomp RuntimeDefault, all capabilities dropped,
  no privilege escalation, and read-only root filesystems.
- Voice has no service-account token. Its HTTP port accepts only this installation’s
  `site` forwarder Pods. If the FunnelGateway deployment handle changes, update
  that exact NetworkPolicy selector; do not broaden it to all namespace Pods.
- Broker Secret and lease access is restricted to its installation namespace.
  Gateway routing/backend metadata reads are cluster-scoped, with `voice` as a
  processing filter. Cluster access includes TokenReview and node/class discovery,
  with status writes only for the configured GatewayClass.
- Gateway `allowedRoutes` permits the labeled Voice namespace only.
  Cross-namespace backend references still require ReferenceGrant.
- Backend Services require ready, selector-matching Pods and controller-owned
  EndpointSlices with matching Pod UID/IP/port. ExternalName, external-address,
  selectorless, headless and host-network backends are rejected.
- Chart policies isolate broker/forwarder traffic. Workload policies permit Voice forwarding and outbound
  encrypted Tailscale connectivity. Voice may download models over public HTTPS.
- k3s disables Traefik, ServiceLB and metrics-server; enables Secrets encryption;
  keeps kubeconfig mode 0600; and restricts host API/kubelet/overlay ports using
  the dedicated `private_funnel_k3s` nftables table.

Namespaces alone are not network isolation. Cluster administrators, node root,
and principals allowed to change RBAC, namespace labels or network policies remain
trusted. NetworkPolicy does not isolate traffic from the node itself. The gateway
holds its own Tailscale identity, so compromise exposes the access granted to that
identity; keep the shared-tag Tailscale policy narrow.

This controller implements a documented HTTP subset. These checks do not claim
full Gateway API conformance, a penetration test, or a hardened multi-tenant host.
