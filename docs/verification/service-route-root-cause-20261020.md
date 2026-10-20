# Service-route investigation and forwarder identity continuity — 20 October 2026

> **Historical checkpoint.** The missing ICMP discovery grants were established
> later. Use the [current diagnosis and runbook](../service-route-diagnostics.md);
> unreleased/unresolved statements below refer to this checkpoint's time.

For the subsequent local code cleanup and artifact inventory, see
[the consolidation review](service-route-consolidation-20261020.md).

## Findings and boundaries

This work separates three observed failures. It does not claim that recovering
an existing public route proves first-advertisement convergence is fixed.

1. **Unsafe dial fallback:** when a Service peer route was absent, SDK 1.102.2
   could send a private TailVIP through the operating-system dialer. The edge
   emitted WAN-interface SYNs instead of using its embedded identity. The new
   dial path resolves the plan once, then calls that identity's netstack
   callback directly. A route disappearing after planning cannot select the
   host network. Missing routes produce an explicit error and never authorize
   DNS/hostname retirement. Control/report HTTP uses the guarded dialer too.
2. **SDK stale expiry snapshots:** SDK 1.102.2's netmap expiry timer could replay
   an older peer list over newer peer mutations. Replaying the upstream
   `TestNetmapExpiryTimerPreservesPeerDeltas` against 1.102.2 failed (peer key
   reverted and the renewed peer became expired). The upstream test passes
   against the pinned 1.102.4. Go is pinned to its required minimum 1.26.6.
   This establishes a real dependency bug; it does not prove that every live
   missing-route incident has that sole cause.
3. **Avoidable forwarder identity turnover:** lease renewal already ran apart
   from control reconciliation, but a 40-second failure canceled the runtime.
   Its normal cleanup logged out the disposable device, then Kubernetes
   restarted it with a new device. Broker stalls therefore amplified routing
   convergence problems. The revised worker fences Service listeners at the
   same deadline, retains the process-owned identity, and retries assignment
   acquisition. It resumes only the exact original controller/node/Node UID/
   node-name/control-URL binding. Actual Pod termination remains disposable.

Unchanged Service/port/backend listeners remain open across polls. Endpoint
pools change in place. Listener fencing is synchronized with creation and
withdrawal so an in-flight reconciliation cannot reopen an unleased Service.
No lease deadline is relaxed.

## Live observations

The edge ran the new SDK and guarded dial path with the same saved device,
configuration checksum, application state and certificates. Existing university,
Harbor, Metube and Voice routing recovered without another identity or
reauthentication. Recovery coincided with an isolated test host joining; that
correlation is recorded rather than treated as a repair mechanism.

The approved `disable-delta-updates` experiment applied only to the main
load-balancer role, added no connectivity grants, and did not recover the
missing routes by itself. The exact diagnostic attribute was removed with
ETag checks; the pre-existing relay grant and unrelated policy were preserved.

An independent, temporary Service exposed a remaining failure on SDK 1.102.4:
the Services API reported its host auto-approved and ready, and the host's
own map contained both VIPs in AllowedIPs and the Service-host capability,
but PrimaryRoutes remained empty. The existing edge had no candidate for
that VIP. Replacing only fixture identities, an unchanged Service definition
update, and fixture-only withdrawal/readvertisement did not resolve it.
Existing public routes stayed healthy during those experiments. This matches
an [open upstream report](https://github.com/tailscale/tailscale/issues/19666),
but no upstream root cause or permanent server-side remedy is established.
Do not manufacture peer routes, bypass approval, or automate identity churn.

All isolated test devices logged out (device reads returned 404), their keys
were revoked, and the fixture Service was removed. No fixture was registered
as a Funnel component or acquired DNS ownership. The 11 DNS ledger rows
compared exactly equal to the pre-investigation checkpoint.

## Verification

- Both Go suites, vet, and image startup are run locally.
- Guard tests cover missing routes, IPv4/IPv6 targets, resolution errors, and
  route removal after planning. The original stream connection is returned
  directly, preserving its TCP/streaming methods.
- Lease tests cover fencing, recovery past server lease expiry, unchanged
  binding, changed-binding rejection and listeners attached during an outage.
- A blocked broker configuration fetch is tested concurrently with successful
  lease renewal. No control API dependency is added to renewal.
- Listener tests prove unchanged polls do not re-advertise, changed backends
  replace their listener, and a fenced assignment cannot reopen before resume.
- Targeted race checks passed, including blocked configuration, registration readiness, listener fencing and unchanged assignment recovery. Full normal/Caddy suites, vet, image startup and diff checks passed.

Private evidence and local logs: `/tmp/funnel-service-route-fix-20261020/`,
`/tmp/funnel-sdk-expiry-{old-,}regression.log`, and
`/tmp/funnel-forwarder-*-tests.log`. Credentials/raw netmaps are not committed.
No stable release or tag is part of this work.

## Final deployed checkpoint — 02:20 UTC (09:20 ICT)

- Native edge: `dev-service-route-20261020-r6`, SHA-256
  `b7d27e82d29876d826b89b71a5cecbccdbaf9dd34c31763f0f4d7d69ee569d3b`.
- DLKN Kubernetes gateway agent and three forwarders:
  `dev-service-route-20261020-r8`, image
  `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:d991f1f0016712537ece5de1feb3e6d541a8f35f1f521a5ddf6b288cd9b86e34`.
  GitOps commit: `80eef00470d23ff8e84245111dc4f8ae4e370815`.
- All four new gateway Pods are ready with zero restarts. Pod replacements during
  image rollout correctly used disposable identities. No permanent identity
  restoration or delegated child approval was introduced.
- Control was not redeployed: its Service host remains `n1BHzztjtz11CNTRL`.
  Edge device remains `n9YQSZEmrA21CNTRL`; its configuration checksum is unchanged.
  Database/encryption material, certificate storage and all 11 ledger rows remain
  intact, with exact owner and remote-record IDs unchanged.
- Public Gateway and five relevant HTTPRoutes are Accepted/Programmed at their
  current generation 2. Final normal-DNS, verified-TLS acceptance passed 70/70
  requests across university API/public/SSO/Eoffice, Harbor, Metube and Voice.
  Harbor's complete component health is healthy; anonymous `/v2/` returns 401.
  Zalo phone preflight returns 204 and the expected `https://h5.zdn.vn` origin.
- Edge diagnostics select Harbor's actual new node-2 forwarder
  `nKAe64kztC21CNTRL`, with allowed/primary routes, a successful TCP connection,
  and direct UDP. No kernel fallback or borrowed identity was used.
- Test Service reads return 404, fixture keys are absent, all temporary test
  devices logged out, and the exact diagnostic node attribute was removed.

**Not closed:** Harbor briefly failed again after its real forwarder replacement,
with an absent VIP peer despite current-generation Programmed=True. It recovered
again during the next isolated test-host registration, without changing its DNS,
backend, edge identity, or production approval. Reasserting approval only for the
isolated fixture did not make that fixture routable. The failure checkpoint is
preserved separately from final successful acceptance. These observations do not
establish a permanent Service-selection/map-delivery fix; do not automate test
node creation as recovery, and do not promote this result as full stability.

The changes close the measured avoidable lease-driven churn, broker readiness
coupling, cold-start budget, dependency expiry bug and unsafe dial fallback.
Unchanged listeners are regression-tested, while Service selection/delivery
under unavoidable host replacement remains an upstream investigation boundary.
