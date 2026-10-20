# v4.0.7 cleanup release and rollout — 21 October 2026

## Source and artifacts

- Runtime release: [v4.0.7](https://github.com/tailscale-x/tailscale-private-funnel/releases/tag/v4.0.7), source `5d72677754e5b8d96a57c5503e386f9ab2a24eb2`.
- [Release CI](https://github.com/tailscale-x/tailscale-private-funnel/actions/runs/39464282158) passed all jobs, including generated assets/contracts, operator API-server checks, both Go suites, vet, PostgreSQL 16/18, native binaries, container validation, image, chart and release packaging.
- Image: `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:b6aa5cb0ab472d4119c2e61f0d813f0af210fc3c9e6e6df1cc9b105b67e0fac8`.
- Linux amd64 SHA256: `9f971d09e99dd2fcfe31a76da72ad1d5dfb56c6e75bfdeba8a9627d3c0f97734`.
- Published checksums passed. Anonymous OCI chart retrieval matched the release package. Version-only image checks passed on all nine Kubernetes nodes, without joining extra Tailscale clients.

The runtime cleanup shares registration retry classification/flow and gateway
drain handling. The independently evidenced fixes remain intact. See the
[cleanup review](service-route-cleanup-20261021.md) and
[current diagnostic runbook](../service-route-diagnostics.md).

A subsequent **test/documentation-only** commit, `3918b4940b8f9ad87d72a9214d5b966358ac404d`,
consolidates three overlapping connection fakes into one fixture, groups backend
pool tests, adds deterministic listener cleanup, and clarifies actual log events.
All 37 named gateway tests remain. Both complete Go suites, vet, gateway race tests,
and the opt-in real-Docker streaming/replacement test passed after this follow-up.
Its test changes do not alter the released runtime binary and require no second
binary release or deployment. The original checkout received these hunks while
preserving its unrelated foreground-tunnel work; its focused tests passed too.

## Deployment and preservation

Deployed only prebuilt released artifacts to:

- xvps native load balancer; Tiny and 5090 native Docker gateways.
- DLKN control with its existing database/material Secret.
- Gateway agents, nine forwarders, and operators across DLKN, m090dev and m090prd.

Every workload reached Ready at the pinned release digest. GitOps stores the
image/chart selections. No VPS/cluster source builds, identity resets, ACL edits,
Service approval toggles, or DNS adoption were performed.

Comparison with the fresh pre-rollout baseline verified:

- All **11 DNS-ledger rows** and **44 exposure bindings** unchanged.
- All three native Tailscale device bindings, addresses and runtime configuration
  unchanged; their running executable checksums match the release.
- Control material Secret UID/data, provider and zone assignments unchanged.
  Authenticated decryption confirmed unchanged stored credentials.
- All eight monitored public TLS certificate fingerprints unchanged and valid.
- All 15 active reporting instances had fresh health and unexpired leases.
- DLKN Gateway `Accepted=True` and `Programmed=True` at its current generation.

The existing edge discovered the replacement control host before its planned
binary update. Kubernetes gateway replacements then recovered with the released
edge left running. Ordinary Kubernetes Pod replacement used disposable network
instances while preserving logical application/component identity and DNS ownership.

## Public acceptance

Normal-DNS and pinned-WAN HTTPS checks passed for admissions frontend/API/SSO,
eOffice's expected login redirect, Voice, Harbor, Metube, and LuongGia/SSO. HTTP
redirected to HTTPS. The preview root retained its pre-existing HTTP 404 with valid
TLS. One initial normal-DNS preview check hit a local resolver timeout; its WAN
check passed, and the full normal-DNS/WAN check set passed on retry.

Facebook verification returned the exact challenge with HTTP 200; invalid
signatures remained rejected, and a signed empty event was rejected without
creating application work. After readiness, 48 consecutive WAN/TLS samples passed
without failures in the recorded observation window.

This was not a zero-downtime rollout. Planned native and single-host forwarder
replacement caused temporary failures; affected routes recovered automatically.
The cleanup does not promise zero downtime or repair unrelated cluster/database
instability. No temporary image-check Pods or direct-Docker fixture containers or
networks remained after verification.
