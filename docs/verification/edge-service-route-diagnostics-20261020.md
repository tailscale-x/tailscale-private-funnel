# Edge Service-route diagnostics and DNS continuity

> Historical investigation checkpoint. Later builds and results are indexed in
> [the Service-route review](service-route-consolidation-20261020.md). Statements
> about running versions and availability below apply to this checkpoint only.

## Result — 20 October 2026

A live recurrence was captured before the edge executable changed. University URLs and Harbor timed out while Metube and Voice worked. The new diagnostic then established **missing Service peer routes in the actual edge identity**, before DERP/peer-relay selection.

The native edge now runs the locally built diagnostic version `dev-service-diag-20261020-r3`, SHA-256 `69f5cfcb33520db2ea51aa540645274b95cc9e11314dfbae3f84cd4b9f2bc124`. It retains Tailscale SDK **1.102.2**, its saved device `n9YQSZEmrA21CNTRL`, logical component `bGC5KTzhD8srlsgL`, configuration, certificate storage and state/data paths. The shared control and gateways were not redeployed by this investigation. No stable tag or release was created.

## Evidence timeline

Times are UTC (ICT is UTC+7).

1. Around 01:05–01:07, **before deployment**, the old PID 166842 had kernel eth0 SYNs toward private Service VIPs and public requests timed out. The upload's initial staging into the VPS's small `/tmp` tmpfs failed checksum validation; nothing was installed from that partial file. It was removed and the verified artifact was staged on disk instead.
2. One ordinary executable update started PID 167437 from the same native state/configuration. This did not fix Service reachability. The saved device ID and configuration digest compared equal.
3. At 01:08:00, the new root-private diagnostic in that process returned:

| Existing Service | Matching route candidates | SDK dial result | Discovery ping |
| --- | --- | --- | --- |
| API webhook VIP `100.102.33.157:8080` | None | host-fallback-refused | no matching peer |
| Main API VIP `100.117.165.184:8080` | None | host-fallback-refused | no matching peer |
| Harbor VIP `100.112.216.176:80` | None | host-fallback-refused | no matching peer |
| Metube VIP `100.91.202.236:8081` | Allowed and primary | Tailscale, connected | direct UDP |
| Voice VIP `100.107.94.228:8765` | Allowed and primary | Tailscale, connected | direct UDP |

4. Concurrent Tailscale API reads showed current university/Harbor Service hosts `approved:auto`, `configured: ready`. Ready Kubernetes API endpoints existed. Some forwarder processes had independently restarted and obtained disposable identities during this period; this task did not initiate those replacements.
5. At 01:10:58, a separate ephemeral diagnostic client on the **same VPS, same SDK and exact load-balancer role** received primary routes and connected to all five Services. It did not register a Funnel component or create an application/DNS owner. Its one-use auth key was supplied through stdin only. It logged out, its key was revoked (200), and its own device was confirmed absent (API 404). Its remote executable was removed.
6. At 01:11:16, the existing edge, still PID 167437 and device `n9YQSZEmrA21CNTRL`, had acquired the missing primary routes. API/Harbor TCP dials succeeded and discovery pings selected direct UDP via the current node-1/node-2 forwarders. No second edge restart, reauthentication, approval mutation, or shared identity replacement occurred.

This is client-specific Service-route convergence evidence. A new-client join and the existing edge's recovery occurred close together, as in an earlier incident; this is not sufficient proof that joining clients repairs coordination. Do not automate identity churn as a workaround. Whether the incomplete map originates in coordination-server delivery or SDK map processing remains unresolved. [Tailscale issue 19666](https://github.com/tailscale/tailscale/issues/19666) records similar symptoms, but this investigation does not establish identical upstream causation.

## Diagnostic implementation and limits

`internal/tsconnection/diagnostics.go` reads the selected runtime's own LocalAPI. It filters the current map down to matching node IDs, names and AllowedIPs/PrimaryRoutes membership. It never serializes raw netmaps, private keys, credentials or unrelated peers.

The LocalAPI dial upgrade invokes the same SDK dial classification. A `Dial-Self` response is reported as `host-fallback-refused` and **is not followed with an OS connection**. This refusal applies to the diagnostic probe; it does not change the production Caddy dialer. SDK dial failures, connection success, and a discovery ping are timestamped separately; the result is not claimed to be an atomic map snapshot.

The load-balancer runtime exposes only a Unix socket under its private data directory:

```sh
sudo curl --unix-socket "$DATA_DIR/diagnostics/service.sock" \
  'http://localhost/service?ip=100.112.216.176&port=80'
```

The directory is 0700 and socket 0600, verified live. Requests accept only a literal Tailscale IP and nonzero TCP port, permit one probe at a time, and use bounded contexts. There is no public/Tailscale HTTP listener, raw LocalAPI proxy, auth/reset endpoint or CLI compatibility path.

A future fail-closed production dialer must use actual route classification, not merely the presence of AllowedIPs: advertised routes alone do not establish that the selected runtime can route a TailVIP. That behavior has not been changed by this diagnostic deployment.

## Do binary or device replacements still delete DNS?

**Ordinary replacement is no longer deletion intent**, provided the control data, logical component/application/exposure IDs, owned address and current authorization remain unchanged.

- Reports and heartbeat expiry expire health snapshots without revoking assignments or reconciling their DNS to empty.
- Re-registering a replacement device changes the runtime instance/device binding, not the logical component or exposure owner.
- New regressions exercise gateway, forwarder and load-balancer device replacement plus a fresh Control object. The old instance loses authority while exact ledger entries, exposure IDs and validated routes remain intact.
- Live verification compared all **eleven** ledger rows and the edge's logical/device binding exactly equal before and after this update, outage and recovery. Independent disposable forwarder replacements likewise did not remove these records.
- Explicit resource deletion/revocation, hostname removal or policy withdrawal still removes the affected owned records. A blank database, changed logical deployment IDs or changed authorization is not covered by ordinary-update continuity.

Existing native routes start from the successfully applied cache before control reconnects. The separate endpoint-only route-equivalence fix remains local and undeployed in control; this diagnostic update does not claim to deploy that fix.

## Verification

Passed in the isolated build worktree:

```text
go test ./internal/tsconnection ./internal/app ./internal/web ./internal/proxy
go test -tags caddy ./internal/tsconnection ./internal/app ./internal/web ./internal/proxy
go vet ./internal/tsconnection ./internal/app ./internal/web ./internal/proxy
git diff --check
```

Targeted new diagnostic/replacement tests also pass in the primary checkout. Tests cover private socket permissions, duplicate listener refusal, malformed/duplicate/unknown parameters, non-tailnet address rejection, no following of host fallback, route candidate filtering, and replacement ownership continuity.

After recovery, all four university HTTPRoutes reported Programmed=True at generation 2. Normal-DNS TLS checks passed for university API/public/SSO/Eoffice, Harbor, Metube and Voice. Zalo phone preflight returned 204 with the expected origin. No phone exchange or application job was triggered. The deployment monitor explicitly records the preceding outage and update interval; it is not an all-green deployment history or proof of permanent transport stability.

Private evidence: `/tmp/funnel-service-diag-20261020/`. Saved state/data/unit snapshots are root-only on xvps. No DNS, ACL, Service approval, application intent, database identity, or shared credentials were changed in this task.

Final monitoring checkpoint: 34 consecutive rounds, 238 successful HTTP/TLS checks across the seven existing routes after recovery. The last sample was 2026-10-20T01:13:02.899437+00:00.

## Sentinel-maintenance handoff checkpoint — 08:17 ICT

A coordinating cluster task reported another earlier public Harbor timeout during separately authorized Sentinel work. Read-only checks at 01:16–01:17 UTC found the existing edge PID 167437/device `n9YQSZEmrA21CNTRL` already had Harbor's primary route through device `n7ibVyKDF211CNTRL` on node 2. The selected LocalAPI TCP dial succeeded and discovery ping selected direct UDP, with no peer-relay/DERP hop reported. HTTPRoute generation 2 was Programmed=True (transition 01:15:05 UTC).

Six rounds of normal-DNS, verified-TLS requests returned Harbor health HTTP 200 and expected registry anonymous HTTP 401; university API, Metube and Voice returned 200. No recovery mutation, edge restart, identity replacement, DNS change or approval change was needed/performed in this checkpoint.

**Harbor application acceptance remains failed:** both public health and an independent read through the Kubernetes Service proxy report overall `unhealthy`. Jobservice, registry and registryctl have internal connection-refused errors at their Kubernetes Services. Core, database, Redis, portal and Trivy are healthy. The public HTTP 200 health envelope must not be mistaken for all-components health. The coordinating cluster task owns those internal component/liveness checks; no application workload changes were made here.

Private checkpoint evidence: `/tmp/funnel-harbor-sentinel-20261020/`.
