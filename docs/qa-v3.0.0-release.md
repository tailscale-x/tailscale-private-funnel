# v3.0.0 release acceptance

Date: 2026-10-17. Builds ran on the development machine, not the VPS or 5090.
The candidate is `dev-operator-20261017-r35`; stable publication is gated by CI
from the committed source. Stable artifact verification is recorded separately
when publication completes.

## Recovery topology

After the previous control cluster became unavailable, control was recreated as
an owned native systemd resource on xvps at the user's request. OAuth bootstrap,
provider setup, zone discovery, policy configuration, and enrollment used the
supported CLI and authenticated control APIs. No DNS mutation bypassed control.

- xvps: native control and public load balancer.
- Tiny: native Docker gateway.
- 5090: native Docker gateway; Kubernetes operator, gateway agent/broker, and one
  node-local forwarder. The existing Voice Pod and routing resources were retained.
- Removed clusters were not provisioned again. Their DNS names were not reclaimed.

The old 5090 gateway/controller and forwarder stopped before replacement. Exact
obsolete Tailscale device identities were verified against the prior enrollment
snapshot and role tag before deletion. After the old control was lost, exact-UID
Kubernetes remnants required administrative retirement; this was disaster recovery,
not an adoption path or a successful normal-finalizer claim. Retired native units
were stopped and disabled, and their device identities deleted. Historical local
state is not treated as an active deployment.

## Ownership and authorization

The fresh database did not claim ownership of existing DNS. The user explicitly
approved reclaiming only:

- `funnel-gateway.dlkn.dev`
- `voice.troly.ai.vn`
- `metube.troly.ai.vn`
- `dichvucong-harness.troly.ai.vn`

Control previewed and applied those approvals. A preview invalidated by an earlier
partition's ledger change was rejected, then previewed again. Unrelated records,
including records for removed clusters, were left untouched.

The temporary bootstrap administrator selector was removed through authenticated
`SaveAccess` with explicit self-removal and verified remaining Owner access.
Persisted administration is only `autogroup:owner`. The tagged diagnostic node's
`manage whoami` returns `permission_denied`, exit 3. No database mutation bypass
was used to restore diagnostic privileges.

## Live acceptance

- Voice `/healthz`, Flue `/api/ping`, and MeTube `/` returned HTTPS 200.
- Fresh ACME issuance succeeded and public HTTP returned 308 to HTTPS.
- A disposable Docker-label exposure was automatically provisioned through control.
- Header echo preserved the public Host and generated trusted client/protocol/port
  headers. Spoofed `Forwarded` and incoming `X-Forwarded-*` values were absent.
- Streaming delivered `1`, `2`, `3`; a stopped backend returned 502 and recovered
  to 200 after restart.
- A 120-second observation window around the actual control upgrade yielded
  220 HTTPS 200 responses, zero failures.
- A separate control stop/restart check yielded 128 HTTPS 200 responses, zero failures.
- A single public WebSocket echoed 120 frames across the control outage/recovery.
  Its TLS connection explicitly selected the verified public edge address because
  the development host's recursive resolver retained a negative cache entry. This
  was not an application dialer change; public DNS was checked separately.
- All five newly enrolled Tailscale node identities survived development updates
  and the control restart. The Kubernetes forwarder retained its broker-backed
  identity across Pod replacement, including the expected lease-fencing wait.
- Native Docker gateways on both hosts fetched the previously absent pinned relay
  image after the update; no manual pull of that new image was used.
- Removing the disposable container withdrew its exposure and owned DNS through
  normal signed reports. Its isolated relay and QA Docker network were removed;
  Cloudflare DoH returned NXDOMAIN. Auto-provisioned application history is retained
  for inspection/reuse; it is not an active exposure or a DNS ownership claim.

No host reboot was performed. No full Gateway API conformance or indefinite control
outage availability is claimed. The five-minute signed configuration expiry and
60-second identity lease remain enforced. Live-provider acceptance used Cloudflare;
other linked adapters were contract-tested.

## Local gates

Passed:

- `go test ./...`
- `go test -tags caddy ./...`
- `go vet ./...`
- controller-runtime envtest with Kubernetes 1.35 assets
- regeneration of RPC/operator contracts and embedded UI: 57 checked files unchanged
- npm clean install, TypeScript/Vite asset build, Tailwind/DaisyUI asset build
- Helm lint and packaging
- Linux amd64/arm64 image binaries byte-equal to their prebuilt native artifacts
- focused clean-host relay image-pull and bootstrap-failure condition regressions
- credential scan (only the explicitly synthetic browser-preview auth key fixture)
- `git diff --check`

Earlier multi-node k3d, independent-installation, authorization, finalizer, and UI
checks are recorded in [operator QA](qa-kubernetes-operator.md),
[control continuity QA](qa-control-update-continuity.md), and
[design QA](../design-qa.md). Source metrics distinguish authored and generated code;
the broader feature scope increased authored code, so no total code reduction is
claimed.
