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

The earlier relative-name fixture still returned authoritative NXDOMAIN in the
same comparison while the complete-name fixtures resolved. This was a live
before/after request-format comparison, rather than an assumption about caching.

The seven established admissions/eOffice/SSO/API/Voice/Harbor/Metube checks were
monitored repeatedly during the update. Temporary fixtures and their owned DNS
records are cleaned up through Docker inventory and control reconciliation;
unrelated resources are excluded. Operational receipts and credentials are kept
outside the repository.

## Release boundary

The fix is deployed as a development control build. The immutable v4.0.1 artifacts
still contain the old append behavior; this task does not create a stable tag or
GitHub release. The original rollout report remains historical evidence, with
this follow-up providing the diagnosis and successful fresh-host acceptance.
