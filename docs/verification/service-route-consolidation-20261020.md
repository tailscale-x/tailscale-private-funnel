# Service-route code and artifact review — 20 October 2026

> **Historical checkpoint.** The missing ICMP discovery grants were established
> later. Use the [current diagnosis and runbook](../service-route-diagnostics.md);
> unreleased/unresolved statements below refer to this checkpoint's time.

## Scope and status

Review covers the routing-outage investigation: report starvation, DNS/route
continuity, the selected-identity dialer, SDK expiry handling, broker readiness,
forwarder leases, Service listener lifetime, diagnostics, and associated release
history. Foreground-tunnel, protocol/UI and unrelated ongoing work is preserved.

This is a **local consolidation**, not a deployment or release. The deployment
checkpoints below are historical evidence from the linked investigation; this
review does not claim a new live availability check.

## Release and development artifact inventory

| Artifact | Purpose and disposition |
| --- | --- |
| v3.3.1 | Published report-starvation correction. External provider work moved out of report acceptance. Its historical signature/enrollment descriptions are not the current v4 authorization contract. |
| v3.3.2 | Published authoritative Docker inventory retirement. Confirmed removal is deletion intent; offline discovery is not. |
| v4.0.1 | Published role-based registration, disposable Kubernetes identities and direct Docker forwarding. |
| v4.0.2 | Latest published stable release, checked through GitHub Releases. Fixes complete Cloudflare owner names; does not claim to solve missing Tailscale Service routes. |
| dev-continuity-20261020-r1/r2 | Earlier control/gateway continuity changes; r2 starts the edge from saved routes before waiting for control. See [continuity checkpoint](routing-continuity-20261020.md). |
| dev-service-diag-20261020-r3 | Earlier edge diagnostic build on SDK 1.102.2. Superseded, useful historical evidence. |
| dev-service-route-20261020-r4/r5 | Intermediate SDK/dial integration builds. r4 briefly deployed; r5 not deployed. |
| dev-service-route-20261020-r6 | Last recorded native edge build; also briefly deployed to DLKN gateways. SDK 1.102.4 plus selected-identity guard and lease fencing. |
| dev-service-route-20261020-r7 | Published development image, not deployed. Superseded by the separate configuration/lease request budgets in r8. |
| dev-service-route-20261020-r8 | Last recorded DLKN agent/forwarder build. Broker readiness separation, cold configuration budget and isolated request budgets. |
| docs/releases/v4.1.0.md | Unpublished draft containing additional foreground-tunnel work. Not evidence of a published or accepted stable release. |

Exact deployed digests, identities and successful **and failed** acceptance
checkpoints remain in [the investigation record](service-route-root-cause-20261020.md).
No published tag, release, package or rollback artifact was removed or rewritten.
Future builds should use the canonical checkout; temporary investigation worktrees
are not independent product branches or another implementation to maintain.

## Consolidation decisions

| Concern | One authoritative implementation | Why it remains separate |
| --- | --- | --- |
| Tailnet route selection | `tsconnection/dial.go`: resolve the SDK plan once, then invoke its netstack callback | Prevents kernel fallback even when the map changes after planning. Removed the redundant Status/AllowedIPs scan in `dns.go`; it could lag the actual routing table. DNS resolution and dual-stack racing still use the selected identity. |
| Service lifetime | `gateway/listener.go`: all removal paths call `removeLocked` | Replacement, explicit removal, reconciliation, fencing and shutdown must serialize withdrawal with creation. Removed repeated close/cancel code and fixed reconciliation's late-withdrawal race. Unchanged listeners still survive polls. |
| Assignment ownership | `app/forwarder_lease.go` | Independent renewal and exact-binding reacquisition fence listeners without replacing the tsnet identity. This is not a health cache or authorization shortcut. |
| Broker configuration | `kubebroker/configuration.go` | One authenticated five-minute cache with explicit-denial invalidation. Recheck expiry after fetch, including cached fallback; a request crossing expiry must not return stale authorization. |
| Broker availability | `kubebroker/rpc.go` | Existing Pod-bound leases can renew while new sessions wait for controller registration. Keep full authentication on each request. |
| Broker request budgets | `kubebroker/client.go` | Two clients intentionally share one TLS transport: short assignment requests and longer cold configuration requests. Do not merge their timeouts and accidentally stretch the lease safety margin. |
| Durable DNS ownership | `web/dns_retention.go`, store ledger | Health failure is not deletion intent. Retain exact ownership filtering and explicit removal/policy/revocation checks. This is distinct from whether a backend can accept traffic now. |
| Route continuity | `web/routing_continuity.go`, `app/loadbalancer_cache.go` | Control rechecks current authorization; the edge saves only successfully applied configuration for disconnected restart. The caches have different trust/lifetime boundaries and cannot safely become one generic stale-data fallback. |
| Diagnostics | Private edge diagnostic socket and `tsconnection/diagnostics.go` | Inspect the actual running identity; never repair routes by replacing devices or probing through an unrelated host identity. |

Load-balancer startup now has one cache-first selection path. The duplicate
second cache read after a failed control fetch was removed. Tests ensure both
working and intentionally empty saved configurations avoid a startup control RPC.

The configuration renderer also reads policy once per request instead of repeating
policy decoding inside its nested exposure loop. Stale comments claiming report
expiry removes DNS or that lease loss terminates the forwarder were corrected.

## Remaining boundaries

- Development images were built from investigation checkouts with uncommitted
  changes. Digests identify the artifacts, but do not substitute for a clean
  reviewed source commit. A future stable release must build from that commit,
  rather than promote an intermediate diagnostic tag as-is.
- Service-map delivery/selection after host replacement remains unresolved.
  SDK expiry regression fixes, a successful public probe, or Programmed=True
  do not prove that the edge has a usable TailVIP route. Do not add reset loops,
  approval bypasses, synthetic peer routes or diagnostic-device churn as repair.
- Multi-backend readiness needs its own follow-up: the aggregate application
  report/acknowledgment can name the primary Service when only a different
  node-local backend is advertised; edge probes likewise use the primary target.
  Do not equate these summaries with per-Service dataplane acceptance.
- Retention must continue to be tested alongside explicit policy denial,
  revocation, source-inventory removal and route-definition changes. Removing
  continuity safeguards merely because the Service transport recovers would
  reintroduce destructive cleanup during the next connectivity failure.
- Device route metadata scans used by diagnostics are observations, not a second
  authorization or routing engine. No broad transport retries are added here.

## Local verification

Regression coverage includes route absence/arrival/removal through the same SDK
plan, dual-stack racing, serialized Service withdrawal/replacement, configuration
expiry during both successful and failed slow fetches, independent lease renewal,
fencing/reacquisition, explicit denial and ownership-scoped retention/removal.

Passed on the canonical checkout after consolidation:

- `go test -p 2 ./...`
- `go test -p 2 -tags caddy ./...`
- `go vet -p 2 ./...`
- Targeted race tests for dialing, listener lifetime, configuration expiry,
  broker availability and forwarder fencing/reacquisition.
- `git diff --check`

Logs: `/tmp/funnel-consolidation-{all,caddy,vet,race}.log`.
PostgreSQL-specific contracts were skipped because `FUNNEL_TEST_POSTGRES_URL`
is not configured; SQLite coverage ran. No schema or SQL changes were made by
this consolidation. No UI/protocol generation changes were required.

No deployment, process restart, identity replacement, DNS mutation, policy change,
commit, tag or publication accompanies this review. Existing unrelated work is
preserved. These local tests do not constitute public stability acceptance.


## Subsequent release preparation

The local-only review above is the pre-release checkpoint. v4.0.3 subsequently
packages the reviewed continuity/consolidation changes and bounded diagnostics
from a separate clean release worktree. Foreground-tunnel changes remain excluded.
PostgreSQL 16 and 18 contracts, regeneration and UI assets were rerun for that
release scope. See [v4.0.3 notes](../releases/v4.0.3.md). This publication does not
redeploy the recorded live installations or establish Service-map stability.
