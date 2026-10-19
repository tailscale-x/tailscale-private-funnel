# Fresh Cloudflare DNS names and first-visit HTTPS

## Diagnosis

The NXDOMAIN failure recorded during the v4.0.1 rollout was reproducible with a
control-owned disposable record. Cloudflare's API and BIND export displayed the
expected fully qualified name and public IPv4 address, but five authoritative
nameserver addresses returned `NXDOMAIN`. Wire-format DNS-over-HTTPS POST, GET,
and JSON requests agreed. This ruled out a browser cache or HTTP GET cache as
the explanation for the authoritative failure.

Funnel converted complete owner names to zone-relative names before calling
`libdns/cloudflare v0.2.2`'s `AppendRecords`. That pinned implementation serializes
the supplied name unchanged. Cloudflare's
[create-record contract](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/create/)
requires a complete name including the zone. A successful API response which
qualifies its displayed name did not establish that the record was served.

The adapter now qualifies owner names before appending. Read normalization stays
in place: provider-relative names still need qualification when comparing the
provider inventory with the ownership ledger. Record data, targets, TTLs, exact-ID
deletion, compensation, and ownership checks are unchanged. Other DNS vendors
are unaffected.

Negative caching can prolong failures after a bad or premature lookup, but was
not the root cause here: a recursive resolver's cache cannot explain an
authoritative NXDOMAIN response. Cloudflare's
[negative-cache guidance](https://developers.cloudflare.com/dns/troubleshooting/dns-issues/#newly-created-record-still-does-not-resolve)
explains why reducing the new A record's TTL or flushing only a browser does not
clear an upstream negative entry. The observed zone SOA negative TTL was 1,800
seconds. Do not query a new name repeatedly before its DNS record is provisioned;
check authoritative DNS first when diagnosing this distinction.

## Regression checks

- Seven request-level cases exercise the real pinned library's HTTP serialization:
  single and nested relative names, complete and trailing-dot names, zone apex,
  `@`, and an empty apex name. All failed before the fix and passed afterward.
- Separate cases preserve A, CNAME, SRV, and TXT data and TTLs.
- Both complete Go suites and `go vet ./...` passed. Suites used `-p 2` and
  `GOMAXPROCS=4` after unbounded concurrent linking caused local memory pressure.
- The control image's extracted binary matched the locally built artifact.

## Live verification

Only control was updated, to the locally built development image:

```text
ghcr.io/tailscale-x/tailscale-private-funnel@sha256:fd3222f749c1ef719ac9a44a3b88b9969eb4a18e845c8498d53210197854e3d5
version: dev-dns-fqdn-20261019
```

The load balancer, gateways, forwarders, and operators remained on v4.0.1.
Control retained its PostgreSQL data and encryption material. Its disposable
network identity was replaced by the Pod update. The new control Service instance
took several minutes to accept runtime connections; existing cached public routes
continued serving. A temporary role-tagged tsnet diagnostic checked the Service
name, IPv4, and IPv6, then logged out; its one-use key was revoked. No human access
grant, ACL change, or gateway restart was used.

Three new Docker-discovered hostnames were created through normal control
reconciliation: a single-label name and nested name in `dlkn.dev`, and a name in
`troly.ai.vn`. Names were not looked up before their ledger entries existed.
All three passed:

1. Authoritative TCP DNS with `AA`, `NOERROR`, and the expected public IPv4.
2. Cloudflare and Google public DNS-over-HTTPS resolution.
3. First-visit public HTTPS with certificate verification, HTTP 200, and the
   expected echo backend. Curl used normal DNS, without `--resolve` or `-k`.
4. Preservation of the original hostname and query string at the backend.

Caddy logged successful Let's Encrypt certificate issuance for each of the three
names. These were new certificates, not reuse of an existing wildcard or fixture
certificate.

The earlier relative-name fixture still returned authoritative NXDOMAIN in the
same comparison while the complete-name fixtures resolved. This was a live
before/after request-format comparison, rather than an assumption about caching.

The seven established admissions/eOffice/SSO/API/Voice/Harbor/Metube checks passed
all 210 checks across 30 monitoring rounds around the update, plus three final
rounds. Flue's `/api/ping` and both Luonggia root URLs also passed their expected
200/302 checks with valid TLS.

Both temporary containers and their network were removed. All four fixture
exposures became inactive, and Cloudflare API reads confirmed that control had
removed their DNS records. The final ledger exactly matched the eleven baseline
records and owners; all previous exposure IDs were retained. No unrelated DNS
record was adopted, overwritten, or deleted.

GitOps commit `81fee14cce0d7c9867485480caa7fb858ce59efe` pins only control to the
fixed image; the Argo application returned to Synced/Healthy with reconciliation
enabled. Operational receipts and credentials are kept outside the repository.

## v4.0.2 release and deployment

The follow-up release is [v4.0.2](https://github.com/tailscale-x/tailscale-private-funnel/releases/tag/v4.0.2),
from commit `bffea99`. The immutable v4.0.1 artifacts retain the old append behavior.
The tag workflow passed tests, vet, generated assets/contracts, PostgreSQL 16/18,
image validation, binary packaging, multi-architecture publication, and chart
publication. Downloaded release checksums passed for Linux amd64/arm64 and macOS
arm64; both image binaries matched their corresponding release binaries.

Released control image:

```text
ghcr.io/tailscale-x/tailscale-private-funnel@sha256:ce41c37ee2582a4e6857df87b1cf31120ac4c06da38be8a30277472a3a98deab
```

GitOps commit `2041356bddfce23c2144dcf4f5dd457e4d47b830` changed only control's
image. The running executable reports `v4.0.2`; gateways, operators and the native
load balancer remain compatible v4.0.1 components. No build ran on a VPS.

### Rollout observations

Concurrent CloudNativePG maintenance replaced the database primary during this
rollout. Control reconnected to the new primary without a reset. Runtime Service
connectivity recovered after the disposable control Pod was replaced; Pod readiness
preceded runtime reconnection. Harbor was already NXDOMAIN and its HTTPRoute was
not Programmed before the release image was applied. Normal reconciliation restored
its record under the same exposure ownership, then the load balancer acknowledged
its route and `Programmed=True` returned. This interruption is recorded rather than
claimed as uninterrupted routing. No database maintenance, ACL change, human access
grant, manual DNS mutation, gateway reset, or identity adoption was performed.

A temporary role-tagged tsnet diagnostic reached the control Service using its
FQDN, IPv4 and IPv6. Its requests were correctly denied human-management access
with HTTP 403; it logged out and its single-use bootstrap key was revoked.

### Post-release acceptance

The first three-name fixture timed out before DNS publication while reconciliation
recorded external API deadline errors. Its container and network were removed;
authoritative inventory retired its registrations. A fresh retry published names
in both zones. Two names immediately passed first-visit HTTPS, while the nested
record briefly lagged the successful API/ledger update before authoritative DNS
converged. Tests therefore wait for authoritative publication before recursive
resolution and certificate checks. API acceptance alone remains insufficient
evidence of served DNS. Slow reconciliation is an observed operational limitation
of this rollout, separate from the complete-name serialization fix.

The released build subsequently passed all three names: a single-label and nested
name in `dlkn.dev`, and a name in `troly.ai.vn`. Receipts include authoritative
`AA`/`NOERROR`, Cloudflare and Google public resolution, normal-DNS HTTPS 200 with
certificate verification, hostname/query preservation, and HTTP-to-HTTPS 308.
Caddy logged new Let's Encrypt certificates for all three names, including the
nested name on its first HTTPS visit. HTTP/2 SSE completed 1,050,018 bytes with
128 payload frames and the terminal `[DONE]`. No `--resolve` or insecure TLS
option was used for those HTTPS acceptance checks.

After recovery, the long monitor finished with 26 consecutive complete passing
rounds for the seven established endpoints. Five additional rounds also passed,
as did Flue's `/api/ping` and both Luonggia URLs. Harbor's brief pre-rollout
interruption and the provisioning delays above remain part of the evidence.

Control removed the fixture DNS records after authoritative inventory confirmed
container deletion. Read-only Cloudflare API queries confirmed all six fixture
names absent. Final comparison exactly matched all eleven baseline ledger
records and owners. Prior exposure IDs and `main-material` encryption/signing
material were unchanged. All six release-test exposures are inactive, and both
fixture containers and their networks are absent. Argo reports Synced/Healthy
with control pinned to the released digest.
