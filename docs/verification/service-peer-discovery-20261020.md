# Service peer-discovery recovery — 20 October 2026

## Finding

Service-only grants were insufficient to keep newly approved, disposable gateway
hosts present in the existing load balancer's peer map. The edge reported no
matching peer and refused ordinary host-network fallback to private Service VIPs.
The missing peer route precedes DERP/direct/peer-relay selection; host `netcheck`
does not inspect the embedded edge identity.

[Tailscale documents minimal ICMP access as sufficient for peer discovery](https://tailscale.com/docs/features/app-connectors).
That documentation concerns app connectors; Service-specific applicability here is
established by live before/after replacement tests with the pinned v1.102.4 SDK.
Earlier Tailscale multi-cluster guidance described host access as a temporary
Service limitation, but the current reorganized Service guide no longer states it.
The approved remedy is installation-scoped **ICMP-only** device access from the
load-balancer role to the three gateway roles, alongside existing application
Service TCP access and Service auto-approval. No device TCP/UDP application ports,
TCP 8766 health server, new administrator authority, issuer scopes, tag ownership,
or auto-approval changes were added. See [the generated policy](../TAILSCALE-ROLES.md).
The live policy update used validation and an ETag; semantic comparison verified
exactly one appended grant and no unrelated policy changes.

## Experiments excluded from the implementation

Before the policy fix, full map-stream refreshes, normal map restarts with metadata
upload, and authenticated control-session reconnection still missed approved hosts.
Idempotent Service and host-approval reassertion did not recover the missing route.
These experimental recovery hooks and the local Tailscale SDK fork are **not** in
the product. Production keeps the pinned upstream SDK. No edge restart, logout,
reauthentication, replacement identity, approval toggle, or synthetic route injection
was used for the final acceptance runs.

## Isolated acceptance

One existing client retained its Pod, process and device identity while the actual
Service-host Pod was replaced three times. No additional client joined between
replacements. Raw LocalAPI map inspection and the netstack connection test ran
before HTTP, so the rejected automatic recovery prototype could not mask failure.
Logs confirm no control-session refresh during the accepted test window.

All three replacement hosts became routable at the first successful sample after
3.22, 3.23 and 3.55 seconds respectively. Each streamed all 20 chunks and `DONE`.
The initial host's stream also completed. These are sampled post-advertisement
propagation times, not total Pod rollout or guaranteed interruption bounds.


## Real deployment acceptance

Replaced the three DLKN DaemonSet forwarder Pods sequentially, with no agent,
control, application Pod, or edge restart. No extra diagnostic client joined
between replacements. The edge kept PID `176412`, its original device ID and
addresses, binary and configuration throughout. Disposable forwarder device IDs
changed as intended; stable logical forwarder/application IDs did not.

| Node | Replacement Pod | Time from old Pod deletion completing to Ready |
|---|---|---|
| `dlkntalos2` | `dlkn-gateway-forwarder-j5tq2` | 63.5 s |
| `dlkntalos1` | `dlkn-gateway-forwarder-8tqq9` | 61.9 s |
| `dlkntalos3` | `dlkn-gateway-forwarder-mzwh6` | 66.4 s |

These are complete startup/registration/readiness intervals, not isolated map
propagation measurements. The existing edge's maps learned replacement hosts
and connected through the `tailscale` plan. Recorded live transport probes used
direct endpoints, with no DERP/peer relay selected for those probes. Neither a
fresh edge identity nor an extra client was required to restore routing.

**This replacement test was not zero-downtime.** Harbor and Voice each had one
eligible node-local host, so stopping that forwarder interrupted that application.
Harbor's sampled failures ended at 22:37:58 Asia/Saigon and its next successful
sample was 22:38:00. Voice had 504/502 samples during node 3 replacement and then
recovered automatically. Both recovered without an edge action. Two other Voice
probe failures were local DNS-resolution timeouts; final normal-DNS HTTPS passed.
The remaining monitored admissions, Metube and LuongGia checks stayed HTTP 200.

Final verification:

- Voice, admissions API/public/SSO, Harbor, Metube and LuongGia Explorer/SSO all
  returned HTTP 200 with valid public TLS. eOffice returned its normal 302 to
  `/login?returnTo=%2F`.
- DLKN public Gateway: `Accepted=True`, `Programmed=True`.
- All three cluster agents and nine forwarders Ready; control Pod unchanged.
- All **11 DNS ledger rows** and **44 exposure bindings** exactly match the
  pre-test snapshots, including ownership values and resource IDs.
- Control material Secret UID and all encryption/signing data unchanged.
- Only the three explicitly replaced disposable forwarder device bindings changed;
  native gateway and load-balancer bindings remained unchanged.
- Exact disposable QA namespace, Service, bootstrap Secrets and devices removed.
- Both Go suites, vet and diff checks passed in the isolated fix tree and the
  canonical checkout, retaining unrelated foreground-tunnel work.

Final live acceptance completed on 20 October 2026.
The policy remedy is live; generator/guidance changes are source changes for the
next control artifact. No stable tag, release or production binary update was made.

## Scope and remaining limits

The remedy satisfies the documented peer-discovery prerequisite and the measured
replacement scenarios. It does not claim to fix Tailscale's coordination service
implementation or guarantee zero outage when the only eligible forwarder is stopped.
Keep the earlier listener/draining and independent lease-renewal fixes; the policy
addresses missing peer maps, not application draining or conflicting writers.

The generated policy, API warning and control setup guidance now include this
prerequisite. The live fix requires only the approved tailnet policy change; no
production binary roll was needed for it. The updated generator will appear in
the next control artifact. No release/tag was created.

## Evidence

Private, redacted-on-publication raw evidence is under
`/tmp/funnel-map-refresh-20261020/` and
`/tmp/funnel-map-refresh-r3-20261020/`. It includes policy before/after, semantic
diff verification, maps and transport selections, Pod replacement records,
stream results, public HTTPS monitoring, and database/key snapshots. Do not commit
OAuth credentials, bootstrap Secrets, or raw identity material.
