# Hostname DNS mode

Funnel Control owns DNS mode once per public hostname, across all routes,
applications, gateways and clusters contributing to that hostname. New and existing
hostnames default to **DNS-only**. Gateway API HTTPRoutes, Ingress resources,
Docker labels and tunnel reports cannot set or reset this choice.

Administrators can open **Exposures → Configure DNS** beside a hostname and choose
DNS-only or Cloudflare proxy (orange cloud). The Cloudflare option requires the
hostname's managed zone to use an enabled Cloudflare provider. Viewers can inspect
the configured mode. Saving schedules normal DNS reconciliation; it does not
claim the provider has already applied the setting. Review zone DNS status in
Operations if reconciliation is pending or failed.

The same setting is available through the typed management API:

- `ListExposures` includes `dns.cloudflareProxied`, `dns.revision` and
  `cloudflareProxyAvailable` on each hostname.
- Admin-only `SaveHostnameDNS` accepts the exposure `id`, `cloudflareProxied` and
  the last read `revision`. A stale revision returns `aborted`; read again before
  making another change.
- The generated CLI exposes `exposures dns save`. DNS previews report
  `proxyUpdates` independently from record additions and removals.

The configuration is stored by hostname, independently of route membership.
Removing a temporary challenge route, changing its backend, or rediscovering a
hostname does not overwrite the saved mode. Separate hostnames and aliases keep
independent settings. If a hostname moves to a non-Cloudflare provider while its
proxy mode remains enabled, reconciliation reports an error rather than silently
changing its requested exposure mode; switch it to DNS-only explicitly.

Cloudflare reconciliation first validates the existing exact-record ownership
ledger. Proxy changes patch only matching owned A/AAAA/CNAME record IDs, with a
fresh identity check before mutation. They do not replace the DNS record or adopt
an unowned record. Existing overwrite preview/approval rules still apply. A failed
proxy PATCH leaves the record owned and retryable. Cloudflare's automatic TTL is
used while proxied; DNS-only restores the generated record TTL (at least 60 seconds).
No zone SSL/TLS, cache, WAF, Access or firewall setting is changed automatically.

Use [Full (strict) TLS](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/)
with a valid origin certificate. Cloudflare's upload limits, caching, challenge
rules and request timeouts still apply. Verify large Harbor pushes and streaming
workloads before enabling proxy mode on those hostnames. Certificate validation
paths must remain reachable without cached or browser-challenge responses.

The API updates the DNS record's proxy setting through Cloudflare's
[DNS record PATCH endpoint](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/edit/).
No live hostname is enabled merely by upgrading Funnel.
