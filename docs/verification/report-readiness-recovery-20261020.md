# Runtime report and edge-startup recovery — 20 October 2026

## Acceptance status

Tiny's report handling and startup readiness recovered. Subsequent live verification found all 15 current runtime components with fresh health and unexpired leases, all eight DLKN HTTPRoutes and its Gateway Programmed at their current generations, and all nine public HTTPS health checks passing. The approved temporary policy diagnostic was removed and the exact original policy verified. This is a recovery observation, not a permanent fix for upstream Service-map notification.

## Measured failure

Bounded control instrumentation captured a Tiny report taking 47.93 seconds, including 46.06 seconds in its handler, after the client had canceled. Other reports took 26–48 seconds. Simple reads and lease checks waited 1–5 seconds on the single database connection. Process-wide cumulative pool waits reached minutes; those totals are not individual request latencies.

The old path held one installation-wide report mutex, repeated metadata/authorization reads and identical settings writes, and separately committed the snapshot and each backend assignment. Expired requests could also keep doing unbounded storage work. Docker inventory acceptance queued behind the same report mutex.

A separate DLKN node-3/API disruption caused an etcd synchronization timeout, Pod replacement and a CloudNativePG primary change to `dlkn-postgres-1`. Verification was updated to follow the cluster-reported primary. This does not attribute every storage delay to Funnel alone.

## Fix

- Bounded, cancellation-aware component lock stripes replace the global report mutex. Inventory/report ordering for one component is retained.
- Request-scoped verified component bindings avoid repeating authentication/storage work in nested helpers. Every new request still performs WhoIs/role/instance authorization.
- Operation-scoped metadata caching avoids repeated routing reads. Ownership mutations remain on the live store.
- Report, nonce, roles, backend updates and stale-backend withdrawal commit atomically in one transaction. Cancellation, invalid bindings or any failure roll the entire operation back.
- Identical JSON settings and already-revoked backend assignments are not rewritten. The serialized database pool and durability settings were retained.
- Slow RPC/storage logging is bounded and excludes payloads, SQL text, arguments and credentials.

No HTTP timeout was increased and no readiness flag was manually changed. Tiny must receive a successful publication acknowledgment before startup readiness.

The edge also had a startup-order gap: it registered before consulting its saved routes. An existing edge now validates its local role and component/control/installation-bound cache, starts retained routes, and retries registration in the background. Fresh/corrupt/foreign-cache startup still requires registration. Explicit registration denial is fatal; retained routes cannot authorize new control assignments. This was exercised on the VPS while control's Service peer was missing.

## Builds and deployment

Built locally, with no stable tag or release:

- Diagnostic control: `dev-v404-report-timing-20261020`, image `sha256:daf1ad248489799bc756bf767dcd33fea2343e0c8991bed6f50defce9cacd8f2`.
- Repaired control: `dev-v404-report-recovery-20261020`, image `sha256:fc19797459c41c919e996d64c70435925b271dcdc35aa458f3efcff7ec590c11`.
- Native xvps edge: locally prebuilt `dev-v404-report-recovery-r2-20261020` binary.
- Tiny and 5090 retain their released v4.0.4 binaries; their report-processing fix is server-side.

Control changes went through GitOps commits `1c3940efc45cb737596a1ebe2e123b4d8fbe893d` and `410834f0f934664cccad44360aebe6331490137e`. Native processes were restarted normally with saved state, without logout, reset or reauthentication. No Kubernetes gateway/forwarder identity was deliberately replaced to clear the map issue.

The persistent native device IDs remain `nUP28juCMC11CNTRL`, `n14Mio641P11CNTRL`, and `n9YQSZEmrA21CNTRL`. All 11 ledger rows, existing exposure bindings and control material Secret UID/data remained unchanged. Native configuration bytes were preserved. Kubernetes control networking remains Pod-scoped/disposable; database/encryption material and logical identities were retained.

## Verification

Both Go suites, vet, targeted race tests, SQLite and PostgreSQL 16/18 contracts passed. The first broad PostgreSQL 18 run timed out during fixture creation under heavy local load; a lower-concurrency rerun passed. New contracts cover atomic rollback, replay/revocation, foreign-backend rejection, cancellation, unchanged-setting writes, component-scoped serialization, cached-edge startup and explicit denial. Focused tests also passed after merging into the checkout's unrelated foreground-tunnel work.

At 11:02–11:09 UTC, Tiny receipts were consistently 0–2 seconds after the client-created expiry timestamp's inferred send time, with report age 0–9 seconds. These are second-resolution observations, not subsecond benchmarks or a matched-load comparison against all clients. No Tiny inventory/publish/configuration timeout was logged after 11:00 UTC through the subsequent verification window. Readiness became green through actual acknowledgments.

After retained edge startup, TLS-verified public health probes passed for Voice, admissions API/public/SSO/eOffice, Harbor, Metube and LuongGia explorer/SSO. Facebook callback challenge returned 200 with the exact challenge; invalid signatures returned `INVALID_SIGNATURE`; a correctly signed empty event reached `ACCOUNT_MISMATCH` without enqueueing a fake delivery. No application credentials or Meta configuration changed.

## Control-Service route recovery and remaining upstream risk

The new control Service host is approved/ready at the same TailVIP, but some existing clients report no matching peer. The edge's own diagnostic socket confirmed `host-fallback-refused`; normal LAN fallback remains prohibited. This matches the separately acknowledged [Tailscale Service-map notification issue](https://github.com/tailscale/tailscale/issues/19666#issuecomment-6153756034), whose latest maintainer update still reported a fix in progress when checked.

Tiny recovered connectivity after a normal restart retaining its identity. With explicit user approval, a temporary `disable-delta-updates` node attribute was applied only to the main Docker/Kubernetes gateway role tags. The policy was validated first and changed using its ETag. Exact original policy bytes were restored with another ETag check and verified by readback. No connectivity grant or administrator authorization changed.

At 11:41–11:44 UTC, all 15 current runtime components had valid leases and fresh health; Tiny report receipt latency was 0–1 seconds in these samples. The edge diagnostic showed the current control host and a direct userspace Tailscale route, still present after rollback. All eight DLKN HTTPRoutes and its Gateway were Programmed=True at their current generations. Some acknowledgments occurred at 11:33, before the diagnostic, so these observations do not prove that the attribute caused recovery. No component was restarted or identity replaced during this diagnostic.

TLS-verified checks passed for all nine health URLs after the policy operation. Facebook challenge/signature checks passed again. All three native processes were ready with their original configuration and running PID receipts. The 11 DNS ledger entries, existing exposure IDs/bindings and control material Secret were checked again and remained exact matches. One background Service-approval reconciliation timed out after rollback; runtime reports/leases and public checks remained healthy. The upstream Service-map notification issue remains a recurrence risk; retained edge routing now protects startup availability but does not itself repair upstream map delivery.

Private evidence and backups are in `/tmp/funnel-report-recovery-20261020/`. They must not be committed.
