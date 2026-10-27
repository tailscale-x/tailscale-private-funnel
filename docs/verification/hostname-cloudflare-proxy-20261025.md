# Funnel Control hostname DNS mode verification

Release integration applies the scoped changes to the exact deployed v4.1.1
commit and regenerates the API contracts and UI assets. The original dirty checkout remains unchanged. No live DNS record, provider
configuration, Cloudflare SSL setting, service or identity was modified.

## Coverage

- SQLite and PostgreSQL migrations add an independent hostname-policy table.
  Defaults remain DNS-only, and route deactivation/reactivation preserves the mode.
- Eight concurrent saves using the same revision admit exactly one writer; stale
  edits fail without overwriting the winning value. PostgreSQL uses eight database
  connections for this regression.
- Typed RPC tests exercise administrator-only mutation, viewer denial, stale
  revisions, readback and rejection of Cloudflare mode on another provider.
- DNS tests confirm previews do not mutate, proxy-only differences are applied,
  identical reconciliations are no-ops, and an unowned identical RR cannot be
  adopted by enabling proxy mode.
- A failed Cloudflare proxy update after RR creation retains the exact ownership
  ledger. A later retry configures the same record without duplicating it.
- HTTP-level provider fixtures verify exact record-ID PATCH, exclusion of sibling
  values, separate zone/DNS tokens, auto TTL when enabled, restoration when
  disabled, fresh record identity validation and sanitized error handling.

## Checks

`go test ./...`, `go vet ./...`, focused race tests, protobuf lint/generation and
UI typecheck/build pass. PostgreSQL 16 and 18 contracts run against disposable
local fixtures. Docker published ports on this development host accepted TCP but
failed PostgreSQL negotiation; the successful fixtures use host networking with
PostgreSQL bound only to a randomly allocated 127.0.0.1 port. No existing database
or host networking settings were changed.

Local Playwright QA uses the synthetic browser fixture, with mocked Tailscale and
DNS providers. Desktop 1440x1000 and mobile 390x844 screenshots were inspected.
The form defaults to DNS-only, saves Cloudflare mode, persists it across reload,
and returns to DNS-only. Two routes sharing one hostname display the same mode.
Viewers have no configuration button. No page errors were observed. A clipped
long-hostname modal title found during QA was fixed before final screenshots.
Browser artifacts are `/tmp/funnel-orange-desktop.png`,
`/tmp/funnel-orange-mobile.png` and `/tmp/funnel-orange-ui.cjs`.

## Boundaries

This validates implementation and provider protocol behavior, not a live orange-
cloud deployment. Certificate renewal through Cloudflare, large Harbor uploads,
SSE/WebSocket behavior and edge/WAF/cache settings still require a deliberately
chosen live hostname and workload acceptance. Saving the setting records desired
mode and leaves application routing and Programmed semantics unchanged.
