# Report-starvation correction — 19 October 2026 (ICT)

## Fault and correction

Native Docker/load-balancer report acceptance shared the external cleanup/DNS
mutex. Health handling also performed endpoint discovery and DNS work. A slow
Cloudflare or Tailscale REST API could therefore exhaust client deadlines and
eventually withdraw otherwise healthy exposures.

The correction makes authenticated report acceptance and health storage local
operations. Separate single-flight periodic workers perform DNS/policy, endpoint,
expiry, revocation, and Service-approval work with independent pass contexts.
Expiry rechecks freshness transactionally. WhoIs binding, signatures, nonce
uniqueness, policy, application authorization, and exact DNS ownership remain
mandatory.

An initial live fault attempt exposed another dependency: Docker label resolution
waited for the Service-provisioning mutex held by Kubernetes snapshots. The test
stopped automatically at a 68.46-second report age, before report expiry; all 70
public checks in that attempt passed. The final correction lets unchanged Docker
bindings resolve without that mutex and reuses control-owned Kubernetes Service
bindings on routine snapshots. Incoming Service targets cannot replace them.

## Deployment and preservation

Built locally; no VPS or 5090 build. Control is updated through its GitOps
FunnelControl image reference with its existing database and identity Secret.
No reenrollment, transfer, database import, ACL changes, Cloudflare Access, or
manual DNS mutations are used. Native gateway/load-balancer binaries retain
v3.3.0 because the correction is entirely control-side.

Tiny's Docker gateway was restarted once during recovery to refresh its Service
listeners. Its machine identity, signing identity, and encryption key were
verified unchanged. Separately, a concurrent admissions application deployment
stopped its Docker API backends; the acceptance baseline waited for that
deployment to finish rather than changing its containers or configuration.

## Acceptance method

A temporary CONNECT proxy stalls only `api.tailscale.com:443` and
`api.cloudflare.com:443`, including already-open API tunnels. TLS is not decrypted
and credentials are not logged. Tailscale transport/DERP and application backend
networks remain available. The fault is removed before deploying the normal
image; temporary proxy resources and their narrowly scoped NetworkPolicies are
then deleted.

Each round resolves public A records over DNS-over-HTTPS, then sends HTTPS with
normal certificate/SNI validation to the resolved public address. The local LAN
resolver has inconsistent cached answers, so it is not used as DNS evidence.
Google DoH is used for the final run after Cloudflare's recursive resolver held
negative answers from the earlier withdrawal. No records are manually recreated.

Checks cover:

| Service | Host/path | Expected |
|---|---|---|
| Admissions | `tuyensinhhcmulaw.troly.ai.vn/` | 200 |
| E-office | `eoffice.tuyensinhhcmulaw.troly.ai.vn/` | 302 |
| SSO | `sso.tuyensinhhcmulaw.troly.ai.vn/login` | 200 |
| API | `api.tuyensinhhcmulaw.troly.ai.vn/health/ready` | 200 |
| Preview | `hcmulaw.dlkn.dev/` | 200 |
| Preview API | `api-hcmulaw.dlkn.dev/health/ready` | 200 |
| Preview CRM | `crm-hcmulaw.dlkn.dev/` | 302 |
| Preview SSO | `sso-hcmulaw.dlkn.dev/` | 302 |
| Harbor | `harbor.dlkn.dev/api/v2.0/health` | 200 |
| Metube | `metube.troly.ai.vn/` | 200 |

The acceptance gate requires a fault longer than five minutes, every public check
passing, both API targets actually stalled, and native Docker/load-balancer report
ages below 60 seconds. This tests external REST API failure, not database failure
or loss of the Tailscale data plane.

## Local gates

- `go test ./...`
- `go test -tags caddy ./...`
- `go vet ./...`
- Shared expiry transaction contract against SQLite and disposable PostgreSQL 18.
- Reproducible protocol generation and embedded UI assets.
- Image startup/version check and `git diff --check`.

## Final fault acceptance

- Fault duration: **380.73 seconds** (01:08:36–01:14:56 ICT).
- Public checks: **340/340 passed** across all ten endpoints; certificate
  verification succeeded on every request.
- Maximum observed native Docker/load-balancer report age: **29.55 seconds**.
- Newly stalled CONNECT requests: **31 Tailscale REST** and **12 Cloudflare REST**.
- No native report/configuration timeout warnings during the fault.
- During the fault, all **20 node bindings**, **13 owned DNS records**, encrypted
  settings, provider configuration, both zone assignments, and policy matched the
  pre-deployment snapshot.
- Harbor's health response separately reported `healthy`.

Normal development image:
`ghcr.io/tailscale-x/tailscale-private-funnel:dev-report-isolation-20261019-3`

Published image index:
`sha256:27bcb2540cfda3e67466de74addc6c6480a7663ccf23b5035061b29f63e1a76a`

Final normal-image GitOps commit:
`d8837fcd2fe20f771af42189ea912c82366d9b40`.

The development verification above did not create a stable tag or GitHub release.
The subsequent stable rollout is recorded below.

## Normal-image acceptance and cleanup

After the fault, the normal image was verified Ready with the published image
index and no proxy environment. The test namespace, extra control egress policy,
and disposable PostgreSQL container were removed. No temporary administrator
grant was introduced by this verification.

A subsequent cluster disruption restarted application, broker, forwarder, and
control containers and changed the PostgreSQL primary. Preview API briefly
returned 503 and Harbor was unavailable while its workloads recovered. These
post-fault failures are recorded separately from the successful external-API
stall test; the deployment is not claimed to have had zero interruption overall.
No application configuration or database was changed to mask those failures.

After recovery, **30/30 consecutive public checks passed** on the normal image;
Harbor again reported `healthy`. Control was Ready, zone reconciliation reported
`Reconciled`, and exposure synchronization had no errors or pending cleanup.
The final storage snapshot again matched all original node bindings, 13 ledger
records with their exact owner values, encrypted settings, provider, zones, and
policy. Control's encryption/signing/Tailscale identity and Tiny's persisted
identity hashes matched their pre-deployment values.


## Stable v3.3.1 rollout

Published [v3.3.1](https://github.com/tailscale-x/tailscale-private-funnel/releases/tag/v3.3.1)
from commit `880d894`. All jobs in the
[release workflow](https://github.com/tailscale-x/tailscale-private-funnel/actions/runs/39179434221)
passed, including both Go suites, vet, generation, operator checks,
PostgreSQL 16/18 contracts, native builds, image checks, and OCI chart verification.
Linux amd64/arm64 and macOS arm64 binaries, checksums, metadata, CRDs, and chart
artifacts are available. The downloaded Linux amd64 binary and release metadata
passed checksum verification; both the binary and container report `v3.3.1`.

Updated only DLKN control in place through GitOps commit
`6da52f8239b15d596f2111d6f2b703a7eaa8d39c`. Argo reports Synced, and the sole control
Pod is Ready on the exact published image index:

```text
ghcr.io/tailscale-x/tailscale-private-funnel:v3.3.1
sha256:28cb42d929746197ccddd38220d62e8523a202713a85c9eb43caea624407f7b5
```

The load balancer and gateways remain running v3.3.0; the patch is control-side
and requires no client or deployment-contract upgrade. No reset, transfer,
reenrollment, ACL mutation, administrator grant, or manual DNS mutation was used.
A consistent protected database backup and identity snapshot were saved before
replacement.

The fresh deployment baseline contained 20 node bindings and 11 owned DNS ledger
records. All of them, including their exact owner values, matched after rollout.
Provider configuration, encrypted settings, both zones, and policy were unchanged.
The control Secret UID, encryption key, signing key, Tailscale machine/profile
identity, hostname, and storage binding were retained.

Released-image verification passed **50/50 public DNS/HTTPS checks** across the ten
admissions, preview, Harbor, and Metube endpoints listed above. Each HTTPS request
used normal certificate validation. Harbor separately reported `healthy`; all
four preview HTTPRoutes reported `Programmed=True`. Native load-balancer and Docker
gateway reports were fresh (approximately 12 and 2 seconds old at the final
storage probe), and exposure reconciliation reported no errors or pending cleanup.
The earlier 380.73-second development fault test remains the external-API-stall
acceptance evidence; this stable rollout rechecked normal public routing and
persistence without reintroducing fault infrastructure.
