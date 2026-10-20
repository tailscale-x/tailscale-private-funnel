# Service lifecycle development rollout — 20 October 2026

> **Historical checkpoint.** The missing ICMP discovery grants were established
> later. Use the [current diagnosis and runbook](../service-route-diagnostics.md);
> unreleased/unresolved statements below refer to this checkpoint's time.

## Artifact and deployment

- Runtime version: `dev-v404-service-lifecycle-20261020`.
- Image: `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:362e337383641a51ff116804122d46763bcd97404eb5e3e44c12565750bce0a1` (Linux amd64).
- Native binary SHA-256: `04ebb09aaf97e037fc246560157696c9b760fb0c4977bd898e3a891cfd9b791d`.
- Built locally from the isolated lifecycle implementation tree, excluding unrelated foreground-tunnel work. No stable tag or GitHub release.
- Updated the gateway agents and forwarders in DLKN, m090dev and m090prd, plus native Docker gateways on Tiny and 5090.
- Control and public load-balancer binaries remain on their previously verified report-recovery development builds. Operators remain v4.0.4.

GitOps commits in the infrastructure repository:

| Cluster | Commit |
|---|---|
| m090dev | `5dda1c426ce1e4b8e335ca5ffdd0bd53335d392f` |
| m090prd | `2c49505f1feff76dceb45e17a2f18fcb1d1a8141` |
| DLKN | `466d8724f9c2ed6c0ec95bf375549590359c1b0c` |

The change keeps unchanged listeners open, updates backend pools without disconnecting existing streams, provides bounded existing-host continuity during backend moves, and drains accepted streams before final Service cleanup. See [the independent handoff and propagation tests](service-handoff-20261020.md). It does not repair Tailscale's separate missing Service-map update issue.

## Interruption and recovery

This was **not** a zero-downtime rollout. A slow image pull on DLKN node 2 delayed the singleton Recreate broker. Existing forwarders correctly fenced when their broker leases could no longer renew. All replacement Pods were ready by approximately **21:48 Asia/Saigon**.

Public 502/504 samples occurred between **21:45:38 and 21:53:00 Asia/Saigon**. DNS and retained Caddy configuration remained intact. After the new Service hosts were approved and configured ready, the existing edge still had no matching Service peer.

One ordinary systemd restart retained the edge binary, configuration, identity and IP. Its old process exceeded the 60-second stop timeout and systemd killed it; this is not evidence of successful graceful edge shutdown. The restarted process initially still lacked the new Service routes.

A separate ephemeral, load-balancer-role diagnostic client could reach the existing LuongGia Service through the xvps peer relay. After that unrelated peer joined, the existing edge also learned the new host and reached it directly. Recovery correlated with that peer/map update; it cannot be credited to the restart or lifecycle patch alone. The diagnostic container, exact Tailscale device and local credential/state files were removed. No policy edits, device reset, reauthentication, DNS edits or application Pod moves were performed.

For future Recreate broker updates, pre-pull the exact image on target nodes before replacing the broker. Keep lease fencing. The upstream Service-map propagation problem and the existing edge stop-timeout remain separate limitations.

## Acceptance after recovery

- All three agents and nine forwarders are ready on the pinned image. DLKN Gateway is `Accepted=True`, `Programmed=True`; its HTTPRoute conditions show no failures.
- Both native Docker gateways are ready on the new binary, with byte-identical saved configuration and unchanged addresses/device bindings. The edge retains its prior device binding and addresses.
- All **11 DNS ledger rows** and exposure ID/application/hostname bindings exactly match the pre-rollout snapshot. Control material Secret UID and encrypted/signing data are unchanged. No control restart or database migration was performed.
- The three Kubernetes controller and nine forwarder **disposable network instances changed**, as expected from Pod replacement. Logical component/application IDs did not. Native and control device bindings did not change.
- TLS-verified public checks pass for Voice, admissions API/public/SSO/eOffice, Harbor, Metube and LuongGia Explorer/SSO. No further 502/503/504 samples occurred during the post-recovery observation.
- Facebook webhook: valid verification challenge returns 200 with the exact challenge; invalid signature returns 403 `INVALID_SIGNATURE`; signed empty event returns 403 `ACCOUNT_MISMATCH`, without queuing a fabricated event.
- Final runtime samples show zero expired active leases and zero stale component health. Tiny report receipt latency is 0–1 seconds in recorded samples.
- Earlier probes included local resolver timeouts and eOffice application rate-limit responses. The eOffice probe frequency was reduced; its final TLS check returned 200. These are distinct from the recorded DLKN 502/504 interruption.

Both Go suites, focused race tests, vet and diff checks passed before rollout. The deployment used prebuilt artifacts only. State/data backups were taken before each native gateway update. No stable release was published.

Private snapshots, deployment scripts, logs and detailed probes: `/tmp/funnel-lifecycle-deploy-20261020/`. They contain operational state and must not be committed.

Follow-up: [ICMP-only Service peer-discovery recovery](service-peer-discovery-20261020.md)
records the separately approved policy remedy and replacement tests without an edge restart.
