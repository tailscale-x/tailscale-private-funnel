# Kubernetes installations

The OCI chart is `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`.
Select `component: control`, `loadbalancer`, or `k8s-gateway`; the default is
`k8s-gateway`. Pin the chart version and matching image tag or digest.

All components belonging to one Funnel installation share:

```text
tailscale-private-funnel-<installation>-system
```

`installation` defaults to `main` and is a DNS label of at most 31 characters.
Different controls belong to different installations and namespaces. Release names
are independent: `control`, `edge`, `edge2`, and `gateway` can coexist in one namespace.
Never reuse a release name for a different component.

## Control bootstrap

Create a dedicated namespace and an existing bootstrap Secret from protected files:

```sh
kubectl create namespace tailscale-private-funnel-main-system
kubectl label namespace tailscale-private-funnel-main-system tailscale-private-funnel.io/installation=main
kubectl -n tailscale-private-funnel-main-system create secret generic control-identity
kubectl -n tailscale-private-funnel-main-system create secret generic control-bootstrap \
  --from-file=client-id="$HOME/.config/tailscale-private-funnel/ts-client-id" \
  --from-file=client-secret="$HOME/.config/tailscale-private-funnel/ts-client-secret"
helm upgrade --install control oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel \
  --version '<chart-version>' --namespace tailscale-private-funnel-main-system \
  --set component=control --set installation=main \
  --set-string image.reference='ghcr.io/tailscale-x/tailscale-private-funnel:<image-version>' \
  --set bootstrap.existingSecret=control-bootstrap \
  --set control.hostname=funnel-control-main --set-string control.tailnet='<tailnet>' \
  --wait --timeout 5m
kubectl -n tailscale-private-funnel-main-system delete secret control-bootstrap
kubectl -n tailscale-private-funnel-main-system logs deployment/control-control -c control
```

Open the tailnet address on port 8080 printed in the logs. Management is tailnet-only;
there is no public control Service or Ingress. Default administration uses verified
`autogroup:owner`; the OAuth client needs `users:read` to resolve account roles, plus
`auth_keys`, `devices:core`, and `services` for the shared Funnel tag. Use the existing
static grant; Funnel does not change ACLs. Tagged diagnostic machines need an explicit
configured administrator role or verified capability.

Control uses one Recreate Deployment. Its init container bootstraps or validates the
precreated identity Secret. The PVC contains SQLite and its journals; the identity Secret
contains tsnet state, the database encryption key, and the configuration signing key.
Only ephemeral runtime files are restored into the Pod. Missing or mismatched durable
storage fails startup.
Restart after deleting the bootstrap Secret reuses that state without interactive login.

The current control UI also generates these commands under Tailscale settings →
**Deploy a control installation on Kubernetes**. This creates a new installation;
it does not copy the current database or identity.

## Additional public load balancers

In **Add load balancer**, select **Kubernetes · Helm**, installation, and optional
release name. Execute the generated script with the intended kubeconfig. It creates
a short-lived enrollment Secret, installs the pinned chart, waits for bootstrap,
and removes the Secret. The script waits for the Deployment separately from public
address allocation; a pending LoadBalancer address does not leave consumed bootstrap
credentials behind. Each release owns a separate Recreate Deployment, enrollment and identity Secret. Its PVC contains complete
Caddy storage, including certificates, private keys, ACME state and renewal metadata.

The chart creates a TCP Service of type `LoadBalancer` for ports 80/443. It uses the
cluster's existing cloud provider, MetalLB, or equivalent; it does not provision an
address allocator or create a public route for a private cluster address.

Supported values:

```yaml
component: loadbalancer
installation: main
loadbalancer:
  service:
    annotations: {}
    loadBalancerClass: ""
    loadBalancerSourceRanges: []
    externalTrafficPolicy: Local
```

The provider must preserve client source addresses. L7 frontends, TLS offload,
PROXY-protocol injection, or trusting arbitrary incoming forwarding headers are not
configured by this chart. Caddy terminates public TLS and generates trusted forwarding
headers. Public HTTP/HTTPS are TCP only; this Service does not publish HTTP/3 UDP.

Control uses signed, WhoIs-bound Service observations, not the Pod's outbound Tailscale
endpoint. The node can read only its own public Service. Observations pin Service
namespace/name/UID; a recreated Service UID requires an explicit administrator rebind
with the old process stopped (see the migration operation below).
Public IPv4 addresses are deduplicated, including DNS resolution when Service status
contains a hostname. Pending, stale, private-only, or unresolved addresses retain
previous owned records and produce diagnostics. Revocation removes only that node's
owned contribution. There is no manual address override or Pod/node/ClusterIP fallback.

k3d and many bare-metal k3s clusters report private addresses; these intentionally do
not become public DNS. Allocator-specific capacity limits still apply: k3s ServiceLB
may need node pools to host multiple Services using the same public ports.

Verify the actual client address after Pod replacement as well as initial installation.
On our dedicated xvps cluster, a directly routable Service VIP is handled by kube-proxy.
We disabled the CNI `portmap` plugin there because its host-port NAT path could take
precedence and replace the public client address with a ServiceLB Pod address. This is
a host-specific configuration, not a chart default; hostPort-dependent workloads would
need a different networking setup. Do not apply it to a NAT-only cluster expecting
ServiceLB host-port forwarding. The [live migration report](qa-secret-deployments.md)
records the verified setup.

## Kubernetes gateways and trust

The `k8s-gateway` component runs one controller/broker Deployment Pod with Recreate updates and a
node-local forwarder DaemonSet. Install pinned Gateway API CRDs before this component.
Use separate classes/releases for independent routing controllers.

Control and brokers are trusted infrastructure inside the namespace. Brokers manage
dynamic identity Secrets and forwarder leases. They have no Pod-patching permissions
and cannot create workloads, exec into Pods, modify control Pods,
mutate PVCs, or read another installation's Secrets. Control and load balancers have projected API tokens and get/update/patch access to
their exact identity Secret only;
forwarders have only audience-bound broker tokens and no Kubernetes API permissions.
No broker or forwarder mounts control storage. Delete bootstrap Secrets promptly.

All Pods run nonroot, with read-only roots and dropped capabilities. Only load-balancer
main containers add `NET_BIND_SERVICE`. There is no TUN, host network, host mount, or
Docker socket. NetworkPolicies select installation/release/component; application
namespaces must separately allow traffic from the relevant forwarders.

## Update, rollback, migration, and removal

The Kubernetes controller is a singleton `Deployment` with `strategy: Recreate`.
Its Tailscale identity is independent of the Pod name and reloads from the same
controller-state Secret. There is no controller leader election or Pod-label patching.
Forwarder leases still fence each node's identity; they are not controller election leases.
A controller restart temporarily interrupts broker/configuration service; there is no standby.

When replacing the former controller StatefulSet, explicitly scale it to zero and wait
for its Pods to terminate **before** upgrading the chart. Set `controller.replicas=1`
when reusing existing Helm values. Keep the controller-state and per-node identity Secrets.
Do not run the old and new controller processes concurrently against the same identity.
Remove the obsolete controller election Lease after cutover; retain forwarder leases.
Subsequent Deployment updates use Recreate automatically. A rollback to the previous
StatefulSet chart similarly requires stopping the Deployment first.

- Use `helm upgrade` with a pinned chart/image. For load balancers, wait on the
  Deployment with `kubectl rollout status`; Helm `--wait` additionally waits for an
  external Service address, which may remain pending. Keep component, installation, release,
  Service identity, and PVCs unchanged. Upgrade load balancers independently.
- Use `helm rollback <release> <revision>` for runtime rollback. Database schema rollback
  additionally requires a compatible backup; Helm rollback does not restore data.
- Back up SQLite PVC contents with control stopped and its matching identity Secret
  (including the database encryption key), and include
  broker identity/TLS Secrets in cluster backups. Do not copy SQLite without a consistent
  snapshot. Kubernetes Secrets encryption at rest remains a cluster responsibility.
- For migration, stop the original process, transfer its state/data and keys to the
  destination storage, then start the replacement. Never run the same tsnet identity
  concurrently. Existing installations are not automatically moved or upgraded.
- To remove a node, revoke it in control and wait for owned cleanup, then uninstall its
  release. PVCs remain retained; delete them only as an explicit irreversible operation.
- Replacing an entire control requires its database and encryption key. Missing/corrupt
  state fails startup rather than silently reenrolling; storage loss requires an explicit
  restore or new installation.

Single-replica control and load-balancer Recreate Deployments preserve identity; they are not
active/active HA. The controller is single-replica; forwarders retain identity lease
fencing. Validate WAN routing and ACME only on a cluster with a real routable address.

## Identity and storage provisioning

Control/load-balancer charts reference a precreated Secret (`<release>-identity` by default;
override `identity.existingSecret`). Identity contents never appear in Helm values or release
manifests. The generated installers create an empty Secret only when absent. Do not replace
it with an empty Secret during an update. `persistence.existingClaim` may reference an
operator-managed PVC; otherwise the chart creates `<release>-data` with Helm keep policy.

A fresh `control kubernetes bootstrap` or `loadbalancer kubernetes bootstrap` initializes
storage through the existing OAuth/enrollment flow. Subsequent boots validate and reuse
state without auth-key files. Runtime uses a Secret-backed tsnet StateStore. These commands
are chart internals; native join/serve paths retain their existing Linux filesystem behavior.

For a stopped native/StatefulSet migration, copy the source state/data into a protected
snapshot, precreate an empty identity Secret, and mount the destination PVC in a migration
Pod. Run the matching prebuilt binary with:

```text
<mode> kubernetes import-state --namespace <namespace> --identity-secret <release>-identity \
  --persistent-dir /persistent --source-state-dir /source/state --source-data-dir /source/data
```

The source must be stopped. Import preserves identity, signing keys and encrypted database
contents; the destination must be empty. Import never enrolls or mutates DNS. Keep the
original offline snapshot until verified recovery. Then start the Recreate Deployment.

When moving an existing load balancer from native discovery to a Kubernetes public Service,
an administrator explicitly binds it after stopping the old process:

```text
POST /api/nodes/<id>/public-service
{"namespace":"tailscale-private-funnel-main-system","service":"edge-public","confirm_stopped":true}
```

This audited change preserves node/DNS ownership and requires signed WhoIs-bound Service
observations before publication. Pending observations retain previous owned records. It
does not grant an ordinary node permission to choose its own Service or overwrite DNS.
