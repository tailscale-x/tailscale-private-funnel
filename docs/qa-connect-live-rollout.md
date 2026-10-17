# Connect development fleet rollout

Date: 2026-10-17. Development artifacts only; no stable tag or GitHub release.
All binaries and images were built on the development machine.

## Final topology

| Location | Funnel workload |
| --- | --- |
| `dlkn` Kubernetes cluster | Control Deployment, Secret-backed Tailscale identity, SQLite on OpenEBS LVM PVC |
| `dlkn`, `m090dev`, `m090prd` clusters | Kubernetes controller/broker Deployments and three node-local forwarders each |
| 5090 | Kubernetes controller/forwarder plus native Docker and Tailnet gateways |
| xvps | Native public load-balancer systemd service only; k3s removed |
| Tiny development host | Native Docker gateway only; k3s removed, Kubernetes gateway revoked |

Control retained its private Tailscale setup URL. The source control was stopped
before identity and SQLite transfer. Database integrity checks passed. Control
OAuth/provider credentials, policies, routing and ownership ledger moved through
private transfer; no credentials are committed to the repository.

The public load balancer was stopped in Kubernetes before its Secret-backed
identity and certificate storage were restored to native Linux state/data paths.
It runs as a dedicated unprivileged system user with only NET_BIND_SERVICE,
root-owned versioned executable, protected filesystem and explicit writable state.
Its endpoint source now uses native Tailscale discovery. Ports 80/443 are owned
by the native process. The obsolete Kubernetes firewall unit/table was removed.

## Artifacts

Image/native version: `dev-connect-20261017-final2`.
Image digest: `sha256:444f51f846b1df32ee72c982d3041d652a5332b2ecd4198428d3c77c75e2c840`.

Talos controller/bootstrap scheduling needed explicit placement settings because
all cluster nodes carry the control-plane NoSchedule taint. These are configurable
for controllers/bootstrap and control/loadbalancer workloads; no global taint or
Pod Security relaxation was applied. Regression chart tests cover placement.

Development OCI chart: `0.0.0-dev-connect-20261017-final2-chart3`.
Chart digest: `sha256:337bfc178b8ca5485b554973189597cc151619f9034c2289640bfc4f8fe704ab`.
The chart uses the same prebuilt image; no remote compilation was performed.
Two never-bound forwarder reservations with lost signing state were invalidated
while control was stopped; their controllers subsequently enrolled fresh children.
Bound existing identities were retained.

## Acceptance

- All three provided kubeconfigs authenticate successfully after the user's ACL update.
- Helm reports deployed revisions; controller and all three forwarder Pods are ready
  in each Talos cluster. 5090 controller/forwarder and native gateways report normally.
- Caller-authenticated Connect management, signed reports and Service-host inspection work. Cloudflare read testing succeeds after encrypted database migration.
- Public Voice `/healthz`, harness `/api/ping`, and MeTube return success with normal TLS validation; HTTP redirects with status 308.
- Public DNS resolves to the native xvps load balancer. No direct provider mutation.
- Tiny and xvps k3s executables/services are removed; required native services remain enabled.

Control Pod replacement and native load-balancer service restart recovered at the
same private/public addresses. All 18 remaining active nodes had fresh heartbeats;
the native load balancer reported its public IPv4 ready without diagnostics.

No host reboot was performed during this rollout. A single control Pod and a
single public load balancer are not high-availability deployments; migrations and
restarts can interrupt requests while discovery and routing recover.

## Placement settings

`controller.nodeSelector`, `controller.affinity`, and `controller.tolerations`
apply to both the Kubernetes controller and its bootstrap Job. Forwarder
placement remains separate under `forwarder`. Control and load-balancer releases
use `control` and `loadbalancer` placement settings respectively. For an
all-control-plane Talos cluster, explicitly allow its infrastructure workload:

```yaml
controller:
  tolerations:
    - key: node-role.kubernetes.io/control-plane
      operator: Exists
      effect: NoSchedule
```

Use the same toleration under `control` when deploying control on those nodes.
This does not remove cluster taints or grant access to Kubernetes resources.
