# Single-controller Deployment verification

Verified on 2026-10-15 using locally built development artifacts. No stable tag or
GitHub release was created.

## Implementation

The Kubernetes controller/broker is one `Deployment` replica with `strategy: Recreate`.
It loads the existing controller identity from its controller-state Secret. Removed
controller leader election, leader labels, Pod patching, controller Pod-name dependence,
and the headless controller Service. The broker Service selects the singleton controller.

Readiness waits for the controller's tsnet HTTP client; authentication still applies to
broker operations. Shutdown cancels the runtime and waits for cleanup. Forwarders remain
a DaemonSet with independent Secret-backed identities and per-node lease fencing.

The chart rejects controller replica counts other than one. Control and public
load-balancer workload types remain unchanged. UI instructions and deployment docs now
describe one controller, its restart behavior, and stopped-state migration/rollback.

## Artifacts

- Version: `dev-single-controller-20261015-r1`.
- Image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-single-controller-20261015-r1`.
- Image digest: `sha256:d2e92762a7169666d41f4af3379f856c470f23db409826ed496bec9586a28807`.
- Native Linux amd64 SHA256: `10418c6f02656a0ab5dafe34aac280ba566b72bfc44108f83b33df5beb324538`.
- OCI chart version: `0.0.0-dev-single-controller-20261015-r1`.
- Chart digest: `sha256:10d4a98ef1ea96277978eac1d4f09914fb20027d2f58b706eeaa5b2013b0c96a`.

## Gates

Passed `go test ./...`, `go test -tags caddy ./...`, `go vet ./...`, Helm lint/package
checks, container startup, and `git diff --check`.

New tests check singleton Recreate rendering, rejection of multiple replicas, DaemonSet
retention, broker selectors without leader labels, absence of Pod mutation permissions,
readiness before join, and token authentication after readiness. Existing forwarder
identity/lease/authentication tests remain in place.

## Live cutover

Migrated releases `private-funnel` on 5090 and `tiny-gateway` on this PC, both in
`tailscale-private-funnel-main-system`:

1. Scaled the old controller StatefulSets to zero and waited for their Pods to disappear.
2. Captured protected identity snapshots, then upgraded to the pinned development image/chart.
3. Reused controller-state and forwarder identity Secrets. Forwarder lease fencing delayed
   replacement Pods until the former identity lease expired; no parallel identity reuse.
4. Removed the obsolete controller election Leases. Only forwarder identity Leases remain.
5. Restarted both controller Deployments. Replacement Pods loaded the same identities.

Verified unchanged enrollment IDs, report signing keys, cluster IDs and Tailscale node IDs.
No new enrollment, broader OAuth scope, or ACL mutation was needed. The public native
Funnel control was updated from the same prebuilt binary so provisioning UI/instructions
match the new chart. Public load-balancer runtime behavior was unchanged.

Live RBAC denied controller Pod patching and denied forwarder Secret reads. Both
controllers and forwarders became Ready; neither installation has a controller StatefulSet
or an election Lease. The live provisioning UI describes the singleton Deployment.

Public acceptance after controller replacement: Voice HTTPS health, public WSS TTS
with 48 audio packets, and Flue HTTPS `/api/ping`. These checks used normal certificate
validation. This turn tested Pod replacement, not another physical host reboot.

## Operational boundary

There is no controller standby. Recreate updates temporarily interrupt broker/configuration
service. Forwarders keep their identity lease safeguards; long broker outages stop
advertisements and recover when the broker returns. This is not a zero-downtime or
active/active controller design.

final result: passed
