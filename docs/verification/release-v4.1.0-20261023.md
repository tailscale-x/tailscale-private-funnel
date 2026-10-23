# v4.1.0 release validation

The original dirty development checkout remains unchanged. An isolated release
branch combines the foreground-tunnel snapshot, released main `e14ca76`, and the
temporary HTTPRoute lifecycle fix. No private `.handoff`, database, credentials,
identity state, HANDOFF.md or RESUME_PROMPT.md is included.

## Passing local checks

- Ordinary and Caddy Go suites; `go vet ./...`.
- Targeted lifecycle, foreground-tunnel and bootstrap-budget race tests.
- Eight-worker backend probing with cancellation coverage and a total 25-second
  pass budget so pending backends cannot accumulate overlapping probe passes.
- PostgreSQL 16 and 18 storage, control, registration and management RPC contracts.
- Real API-server operator tests with pinned envtest tooling and Kubernetes 1.35.
- Real Docker streaming and backend replacement regression with a disposable fixture.
- Pinned RPC/operator generation, schema lint, UI typecheck/build and generated drift.
- Native static Linux amd64/arm64 binaries and Darwin arm64 cross-build.
- Helm lint and versioned chart packaging.

The existing IPv6 proxy contracts run in a temporary network namespace because
this development host disables IPv6 loopback. Only that namespace's settings are
changed. Full Caddy tests pass there.

The new concurrent PostgreSQL bootstrap regression initially admitted 48 requests
against a 30-request global limit. Serializing the global counter before reading
both budgets fixes the lost updates; the same regression passes afterward.
The fixture intentionally permits multiple PostgreSQL connections to exercise
concurrent transactions, independent of the default serialized connection pool.

## Browser smoke test

Browser plugin not available; used installed Playwright with a synthetic local
control at `http://127.0.0.1:8097`, without contacting production or issuing keys.
Flow: Gateways → Generate a local tunnel command → embedded/host command generation.
Desktop viewport 1440×1000, mobile viewport 390×844.

Page identity, meaningful content, absence of framework overlays, console health,
command output, switching connection modes, mobile overflow, and screenshots pass.
Commands contain no bootstrap keys. Screenshots and the temporary test script
remain outside the repository under `/tmp/funnel-release-*`.

## Limits and deployment boundary

These are local regressions and release checks, not a new live-tailnet Mac test or
cert-manager staging issuance/renewal. The dated real Mac result remains documented
in `docs/foreground-tunnel.md`. The Caddy HTTPRoute lifecycle fixture uses local
backends and does not claim to reproduce Tailscale propagation timing.

Release publication is authorized; production deployment is separate. No live
route, hostname policy, DNS record, database, identity or service was changed.
Coordinate continuous Harbor health and registry probes for the post-deployment
route/Pod/namespace lifecycle test before cert-manager staging issuance/renewal.

The CI availability fixture caught a public-probe EOF while it forcibly reloaded
unchanged configurations, discarded the production HTTP drain settings and reset
the shared client's idle connections at solver replacement. The fixture now keeps
the generated HTTP app settings, uses the production `caddy.Load(..., false)` path,
and closes only the replaced solver's backend connections. Its no-error Harbor
assertion remains strict, without request retries. Validation: 500 lifecycle
repetitions, 25 repetitions with the race detector, and the full proxy suite in a
fresh process pass. Repeating the entire proxy suite in one process exposes an
existing SSE fixture certificate-cache isolation limitation; normal fresh-process
suite execution remains the release gate.

## Follow-up release gate: shared TCP listener (v4.1.1)

The v4.1.0 tag job caught an EOF on the synthetic challenge request immediately
following route deletion, despite the original PR and main-branch checks passing.
The tag rerun was canceled before publication after a longer 10,000-repetition
stress run reproduced three EOF/reset failures. No v4.1.0 release assets were
published and production was not changed.

Caddy 2.11.4 on Unix creates independent SO_REUSEPORT TCP sockets for overlapping
configurations. Retiring a socket can reset queued connections. The embedded
load balancer now registers a shared TCP network that retains one kernel listener
and directs pending connections to the newest configuration. Closing an old
configuration releases its virtual listener; only the last release closes the
kernel listener. A failed replacement falls back to the previous listener.
HTTP/3 keeps the standard UDP mapping.

The lifecycle fixture reserves its listener until Caddy owns it, avoiding the
free-port race during concurrent tests. Challenge assertions use fresh connections
to inspect the new configuration; Harbor probes retain keep-alive connections
throughout. No request retry or failure suppression is used. Deterministic tests
cover queued payload delivery, rollback, blocked Accept cancellation and final
listener removal.

Follow-up validation passes: 10,000 lifecycle repetitions; 100 combined lifecycle
and listener repetitions under the race detector; 100 final queued-listener race
repetitions; full Caddy suite in the isolated IPv6 network namespace; and a fresh
proxy suite after routing the TLS/SSE fixture through the same production loader.
The TLS fixture changes configuration between loads and verifies HTTP/1 and
HTTP/2 responses while exercising HTTP/3 listener setup.
