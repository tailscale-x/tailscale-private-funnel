# Automatic Docker retirement verification — 19 October 2026

## Development deployment

- Version: `dev-docker-retirement-20261019-1`.
- Image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-docker-retirement-20261019-1`.
- Image index digest: `sha256:fc0a6796eed255e86e89f6617de65468890daa36353af70af880cd53ec20edf7`.
- DLKN control GitOps update: `a998dd45f19268b55123f39097907bb54a31b1c3`.
- Updated control first, then Tiny's native Docker gateway through `native gateways update`.
- Built locally. No stable tag, GitHub release, issue comment, manual DNS mutation, or exact-hostname transfer operation.

## Automatic reclamation

The first complete signed inventory retired 13 absent Docker registrations,
including the four university applications and older removed test registrations.
Stopped containers remained visible to inventory; active Metube registration and
the shared Docker gateway remained intact. Asynchronous Service cleanup completed
for all retirement entries (zero pending at final inspection).

The existing Kubernetes gateway claimed these names through its ordinary signed
configuration and reports. All four HTTPRoutes reached `Programmed=True`:

| Hostname | HTTPRoute | HTTPS result |
| --- | --- | --- |
| tuyensinhhcmulaw.troly.ai.vn | ulaw-public | 200 |
| eoffice.tuyensinhhcmulaw.troly.ai.vn | ulaw-crm | 302 |
| sso.tuyensinhhcmulaw.troly.ai.vn | ulaw-sso | 200 at /login |
| api.tuyensinhhcmulaw.troly.ai.vn | ulaw-api | 200 at /health/ready |

Three consecutive rounds checked public DNS and certificate-verified HTTPS for
these four hosts, Harbor, and Metube: **18/18 passed**. Harbor's health endpoint
and Metube returned 200. Probes used public DNS resolution and explicit connection
to the returned address while retaining hostname/SNI and certificate verification.

Application owners had independently replaced the previous preview hostnames in
their HTTPRoutes. Those four obsolete preview records were withdrawn by normal
reconciliation; they were not restored as additional routes. No application
workload, replica count, consumer, or job was modified during this deployment.

## Preservation checks

- All 20 stored node identities and revocation states unchanged.
- Control identity Secret UID and identity/encryption/signing material unchanged.
- Tiny's persistent identity, report key, state/data paths unchanged.
- Encrypted settings, provider configuration, zone assignments, and policy unchanged.
- All seven independent pre-existing DNS ledger entries unchanged.
- Four university exposure IDs retained; their DNS owners remain `exposure:<original-id>`.
- Old Docker application IDs retained as retired history; new Kubernetes applications use normal Kubernetes IDs.

No pending reservation or zero-downtime guarantee was introduced. Confirmed
removal permits ordinary owned-DNS cleanup; destination readiness and DNS caches
can create a temporary unavailable interval. See [Docker registration lifecycle](../docker-registration-lifecycle.md).

## Local gates

Passed both Go test suites, `go vet ./...`, shared PostgreSQL 18 storage/transfer
contracts, generated protocol/API/UI reproducibility checks, real read-only Docker
inventory preflight, development image startup/version validation, and
`git diff --check`. Added coverage includes inventory replay, invalid/incomplete
scans, stopped/replaced containers, aliases and shared prefixes, competing claims,
mixed old reports, shared Service preservation, and external cleanup retries.

Private diagnostic receipts remain outside the repository; they include no
published credentials. The disposable local PostgreSQL test container was removed.

## Released v3.3.2 deployment

Deployed the published v3.3.2 artifacts on 19 October 2026 (Asia/Saigon):

- DLKN control through GitOps commit `b5b3e0a772c588b4ea08400e6b562b029fbacbc0`.
- Tiny and 5090 native Docker gateways through `native gateways update`.
- xvps native `public-edge` load balancer through `native loadbalancers update`.
- DLKN, m090dev, and m090prd Kubernetes gateway agents/forwarders through GitOps commits `36b75030e0b9a172ca8b4f57f7438b6d80d185d2`, `3139c096a06d3ee1c56248de737badbe20942b4f`, and `7b08e635f6b29a67a098a67959aec1c65394ddd0`.
- 5090 Kubernetes gateway through `k8s gateways update` with its explicit context.

Runtime image index:
`sha256:606324301aea078c28e204dd3f24e27c3bd4059b413d8ebe824d3e5ba1fcea9b`.
The released Linux binary was checksum-verified; no host builds, resets,
reenrollments, database edits, or direct DNS mutations were used.
Cluster operator/chart versions were not changed by this runtime rollout.

All targeted runtime Deployments/DaemonSets and native services reached readiness
on v3.3.2. Native identities and state/data paths matched before/after. Identity
Secret UIDs and selected encryption/signing/Tailscale identity fields matched in
all four clusters. All 20 stored node bindings, encrypted settings, provider
configuration, zones, policy, and seven independent DNS ledger entries were
unchanged. A protected consistent PostgreSQL dump was saved before replacement.

The rollout included service interruption while single-replica workloads and
identity-fenced forwarders restarted. Harbor, Metube, and Voice subsequently
passed three consecutive public DNS and certificate-verified HTTPS rounds (9/9).
Additional observation returned 302 from both Luonggia login/front-end roots and
404 from the harness root, with successful TLS; these were not asserted as
application health endpoints.

### University application acceptance is blocked

This later deployment is not another four-host application acceptance result.
Before replacing control, the saved ledger already lacked three university
records. Live Kubernetes inspection found `hcmulaw/ulaw-api` explicitly scaled to
zero replicas, with no ready API backend; a separate migration Pod was in
`ImagePullBackOff`. The university HTTPRoutes remained unprogrammed while waiting
for authorized backend readiness/Service approval/load-balancer acknowledgment.
Funnel withheld their unavailable routes and reconciled their owned DNS normally.
No university replica counts, migration jobs, consumers, or manifests were changed.
The application team must restore backend readiness before those four hosts can
be accepted again. The earlier successful development acceptance above remains
historical evidence, not a claim about their current availability.
