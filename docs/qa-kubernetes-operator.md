# Operator and unified deployment interface verification

Date: 2026-10-17. Development artifacts only; no stable tag or GitHub release.

## Local gates

- Both Go test suites (`go test ./...`, `go test -tags caddy ./...`) and `go vet` pass.
- Envtest with Kubernetes 1.35.0 passes CR validation, reconciliation, foreign
  ownership, namespace filtering, conditions and deletion-guard tests.
- Pinned RPC, CRD, deepcopy, RBAC, reference and UI generation is reproducible.
- Operator Helm chart lint, packaging, OCI publication and OCI pull pass.
- AMD64/ARM64 image executables are compared byte for byte with locally built
  binaries, without emulation. CI repeats this check. This caught and corrected
  Dockerfile architecture defaults that previously packaged AMD64 twice.
- Diff whitespace checks pass. Authored/generated source metrics are recorded
  separately in `docs/metrics/operator-{baseline,current}.json`. Manual HTTP
  request sites fall from 29 to 17; overall authored code increases because the
  operator and deployment manager add functionality. No overall reduction claim.

## Real k3d and native execution

A disposable three-node k3d cluster ran control, load balancer, gateway agent and
three forwarders. A second fresh cluster verified OCI-only CLI operator create
and update, plus CLI control creation with private OAuth files.

- Moving an HTTP echo DaemonSet from worker 0 to worker 1 moved the sole approved
  Service host. Responses came only from that node's backend, without a remote
  fallback.
- A full test-cluster stop/start retained all six control/gateway/load-balancer/
  forwarder identities. Pod replacement and version updates retained identities.
- Forwarders cannot read Secrets or routing resources. Brokers cannot create
  workloads, execute in Pods, patch Pods, or read another namespace's Secrets.
  Control's exact-Secret role cannot read the load balancer identity.
- Operator deletion refused while CRs remained. Load-balancer, gateway and
  control deletion completed through finalizers, leaving no owned workloads,
  Secrets, PVCs or leases. Operator deletion then succeeded.
- Native create/get/stop/start/update/delete exercised dedicated runtime users,
  system-level units, ownership markers and persistent identity. A real deletion
  test detected stale Tailscale client state after API deletion; cleanup now
  reconnects the same identity without credentials to confirm revocation.
- A backend-generated complete POSIX installer was piped over SSH to 5090. It
  downloaded the pinned public development artifact, enrolled a temporary
  Tailnet gateway and received fresh signed reports. Existing services were
  not adopted or overwritten.
- Fresh-control bootstrap now publishes a canonical private management URL and
  version without requiring a manual settings mutation before enrollment.

Disk pressure in the disposable k3d fixture caused Pod evictions. Its local
kubelet thresholds were adjusted and terminal test Pods removed before repeating
updates. No production host kubelet settings were changed. k3d's private
LoadBalancer allocation is not WAN/ACME validation.

## Browser verification

Chrome DevTools inspected the synthetic local control UI at 1440×1024 and
390×844. All installation pages and deployment wizards were exercised. Native,
Kubernetes and operator commands come from typed backend responses. Copy/download,
node actions, read-only roles and mobile navigation were checked. No horizontal
overflow or console errors were observed. Generating instructions creates no
enrollment. Screenshots contain synthetic identities/domains only:

- `docs/screenshots/kubernetes-operator-instructions.png`
- `docs/screenshots/kubernetes-operator-mobile.png`
- `docs/screenshots/kubernetes-control-instructions.png`
- `docs/screenshots/kubernetes-control-mobile.png`
- `docs/screenshots/control-enrollment-operator.png`
- `docs/screenshots/control-dashboard-operator.png`

## Rollout evidence

Existing processes were stopped before old identities were cleaned up and new
resources provisioned. No adoption or state-import path was added. Control now
runs in `dlkn`; xvps runs only the native load balancer; Tiny runs only the native
Docker gateway. Kubernetes gateways and the operator are Ready in `dlkn`,
`m090dev`, `m090prd` and 5090. Ten node-local forwarders are enrolled (3/3/3/1).
All eighteen active node records have fresh reports. Retired release-owned broker
Secrets and leases were deleted with UID/resource-version preconditions; unrelated
routing resources and application workloads were retained.

Published development image `dev-operator-20261017-r27`:
`sha256:0467f78704686635dcabe6fb9638a4e5450643c3ab984e8edc84a0c3bd2625a1`.
Published operator chart `0.0.0-dev-operator-20261017-r27`:
`sha256:6228c4bc184454d70534e2b85d93048d9dd2bbc13462411147aa154d9cf29e04`.
Both Linux image architectures contain their matching native binary. macOS
arm64 was cross-compiled; no macOS runtime acceptance is claimed.

Live acceptance:

- Voice on both configured public domains, Flue and MeTube return HTTPS 200;
  HTTP redirects with 308. Existing application Services remain approved.
- A disposable tailnet-backed HTTP echo exposure received a real ACME certificate.
  Spoofed Forwarded/X-Forwarded metadata was removed. Host, actual client IP,
  HTTPS scheme and public port were preserved, including an explicit authority port.
- Chunked streaming and WebSocket frame echo pass. Stopping its backend returns
  502, and restarting restores traffic.
- Pausing its gateway beyond the five-minute report lease removed its report and
  owned alias without revoking its persistent identity. Resuming restored routing.
- The disposable application was deleted through the control API; authoritative
  Cloudflare DNS confirms the alias is absent. The echo process was stopped.
- Version updates preserved node identities and the existing Voice certificate
  fingerprint. Native temporary gateway/control resources were created and deleted,
  including an installer piped over SSH and OAuth-only native control bootstrap.
- A same-name foreign Helm release and canary survived rejected operator creation;
  envtest also verifies a replacement Secret survives a UID-fenced delete race.

No physical host reboot was performed in this rollout. The disposable k3d full
stop/start provides restart evidence; it does not prove recovery of these live
hosts after a physical reboot. The missing legacy Llama container was not
recreated or claimed as tested. No stable tag or GitHub release was created.

Storage note: dlkn's OpenEBS LVM StorageClass uses `Retain`. The retired
`control-data` PVC was deleted; its Released PersistentVolume is intentionally
left to storage-administrator disposal. The new `main-data` PVC is Bound and
unrelated application volumes were not changed.

Final control-only patch: `dev-operator-20261017-r28` accepts the currently running
development version in deployment defaults while continuing to verify stable
release assets. Unknown development tags are rejected. Both Go suites, vet and
AMD64/ARM64 packaged-byte checks pass again. Image digest:
`sha256:108e560e4d6552a74d75314b72d8dfaf27ece6a6196980f540638d3e30dbb4f5`.
Matching operator chart `0.0.0-dev-operator-20261017-r28` digest:
`sha256:d016a85ab4e6069db255b09fe54fb3377a15a52f9245f3326af3e9c2ea538631`.
The fleet remains on r27; control runs r28. This patch changes only control-side
release-default validation and its regression test, not runtime contracts.

Availability observation: the final single-replica control update temporarily
withdrew both Voice routes and their owned DNS records while Kubernetes
configuration and Service readiness reconverged. Both recovered automatically
with the same identities; authoritative DNS and direct public TLS returned 200.
This deployment is not zero-downtime control-plane HA. Fresh reports alone must
not be interpreted as public-route readiness; inspect the per-exposure state.

Administrative cleanup boundary: the attempt to remove this tagged QA machine's
explicit administrator selector was rejected by the API's self-lockout guard
(`retain your administrator access`). The existing `autogroup:owner` selector
remains, alongside that temporary node-specific selector. An authenticated Owner
must remove the QA selector from Access Roles; no direct database bypass was used.
Temporary bootstrap scripts, Cloudflare credential staging and the exported 5090
kubeconfig were removed. Original user credential files were preserved.

## Follow-up resolution

The Voice-update interruption and temporary administrator cleanup described above
are historical findings. They are resolved in the current development build by the bounded broker configuration
and authorization cache, nonblocking forwarder report acceptance, and explicit
Owner-verified self-removal. See [current acceptance and release decision](qa-control-update-continuity.md)
for the final development build and live evidence. This section does not rewrite
the earlier observed failures.
