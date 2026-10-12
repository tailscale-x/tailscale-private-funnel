# Tailscale-only load-balancer IPv4 discovery

Verified 2026-10-09. Supersedes the IP override behavior in qa-settings.md.

- Public IP overrides, their node API, CLI flags, saved deployment values and
  node-reported IP source are removed.
- Control requests GET /api/v2/device/{verified-enrolled-device}?fields=all and
  decodes clientConnectivity.endpoints. The original top-level endpoint parsing
  was incorrect; the contract test now uses the actual Tailscale response shape.
- Only endpoint addresses are candidates. Device addresses and health-reported
  values are ignored. Ports are stripped, IPv6/private/CGNAT addresses filtered,
  and identical IPv4 addresses deduplicated.
- Missing, ambiguous endpoints and API failures preserve the prior owned A record
  for a fresh node. Report expiry/revocation cleanup remains unchanged.
- Migration removes obsolete settings without changing DNS ledger ownership.
- Node inspection displays read-only control-fetched endpoints.

Both Go suites, vet and diff checks passed. Tests cover fields=all, nested JSON,
multiple ports for one IP, ambiguous endpoints, missing endpoints, IPv6/private
filtering, ignored reported/device addresses, API failures, retained A records,
provider-disable cleanup, migration and removed override API.

Chrome checks confirmed the mobile 390px node page has no overflow and no
mutation form. Live inspection shows the discovered address and source guidance.

Locally prebuilt control image:
`dev-endpoints-20261009-r2`
`sha256:01dff2d8aa036d150cb82cfe71709d7aaecab1751138d7729d3b5eeeca517f36`

Deployed the matching prebuilt native load-balancer binary on the VPS; removed
its obsolete saved public_ipv4 field. No builds ran on the VPS. No stable tag or
GitHub release was created. Ingress was not changed.

Control fetched 160.191.243.219 from Tailscale, matching the existing owned A
record, with no DNS sync error. The immediately preceding baseline's zone
assignments, exact ledger entries, encrypted provider credentials and secret
ciphertexts remained identical after deployment. The older zone-selection
baseline had already differed before this rollout, so it was not restored.

Post-restart probes: control health 204 and Llama health 200. Flue's hostname did
not resolve; it was already absent from the two-record baseline taken before
this deployment (only Llama CNAME and load-balancer A were present). This rollout
neither restored nor removed that exposure.
