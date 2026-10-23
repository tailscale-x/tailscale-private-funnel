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
