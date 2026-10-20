# University backend and Service reconciliation after endpoint replacement

> Historical investigation checkpoint. Later builds and results are indexed in
> [the Service-route review](service-route-consolidation-20261020.md). Statements
> about running versions and availability below apply to this checkpoint only.

## Follow-up — 20 October 2026, 07:47 ICT

The existing API recovered during subsequent read-only checks: three `/healthz` requests returned 200 with valid TLS; preflight OPTIONS `/api/zalo/phone` returned 204 with `Access-Control-Allow-Origin: https://h5.zdn.vn`. All four university HTTPRoutes report Programmed=True at generation 2. Harbor and Metube also return 200 with valid TLS. This supersedes the unavailable checkpoint below, but does not establish long-term stability or explain the recovery. Node-2 and node-3 forwarder device IDs changed between snapshots through activity outside this investigation; no replacement was performed here. Native-device phone acceptance remains separate.

Scoped live device inventory: 19 Funnel-tagged devices visible, 15 explicitly `isEphemeral=true`. The four not marked ephemeral are the two native Docker gateways, native public edge, and this PC's pre-existing host daemon. Current Kubernetes issuance and runtimes request ephemeral identities; native embedded defaults retain identity. No devices were deleted during this audit.

## Earlier scope and acceptance

Read-only platform investigation, 2026-10-20 00:34–00:39 UTC. **Public university acceptance remains unpassed.** No runtime was restarted, reset, reauthenticated, redeployed, or assigned another identity in this investigation. No approval, ACL, role, DNS, application, or database mutation was performed. The previously authorized edge restart is a separate earlier operation.

## Observed chain

| Stage | Result |
| --- | --- |
| Kubernetes nodes and Funnel Pods | Three nodes Ready; control, agent and three forwarders Ready |
| University API EndpointSlices | Two ready API Pods on nodes 1/3; each webhook Service has one ready Pod on node 1 |
| Stored application | Existing `k8s-855d0201282e64c59b2a54a5`, enabled, authorized gateway `LUEWsXpTU-AHQZxI` |
| Backend translation | Four Service UID/port assignments, matching current API and Facebook/Instagram/Zalo webhook endpoints |
| Actual Service approval | All four backend Services have current node-1 device `n2K37YD5ZK11CNTRL` reported `approved:auto` and `configured: ready` by Tailscale's read API |
| Gateway reports | Current node-1 and node-3 reports include API application reachable=true; node 2 has no local university backend and reports false |
| Edge probe | Application's selected Service `100.102.33.157:8080` times out |
| Edge socket | PID 166842 observed SYN-SENT from `160.191.243.219%eth0` to that private TailVIP |
| Edge desired/active routes | API hostname absent from stored sent routes and live Caddy configuration; other university hosts remain present |
| HTTPRoute | `ulaw-api` generation 2 Accepted/ResolvedRefs=True, Programmed=False since 00:27:14 UTC; other three Programmed=True |
| Public DNS/TLS | API resolves normally to existing edge and TLS verifies, but `/healthz` returns 404 |

The API application currently contains these four backend Services:

- Zalo: `svc:tpf-k8s-239bb7355a2b28bdcc6d955ee8f3` → `100.102.33.157:8080`.
- Facebook: `svc:tpf-k8s-07a73ae1340c3794d8c61254ee3a` → `100.74.95.89:8080`.
- Instagram: `svc:tpf-k8s-86501d717df24bd10e16964ff29e` → `100.74.116.37:8080`.
- Main API: `svc:tpf-k8s-4d25294e92433c331a8abf1e9e4f` → `100.117.165.184:8080`.

The application's top-level Service currently selects Zalo. Edge health probes use that top-level target; HTTP path rules contain the individual backend targets. A failure of that probe gates the entire hostname, including `/healthz` and `/api/zalo/phone` which use the main API backend. The 404 is consistent with the absent edge route, rather than an application authentication response.

## Interpretation and remaining gaps

1. **Immediate blocker:** the edge's selected-transport probe cannot reach the selected application Service even though a current, approved, ready host exists. The kernel socket is consistent with the pinned tsnet SDK falling through to its system dialer when it has no peer route. The exact reason the in-memory route is missing is still unproven; this investigation does not equate API approval with client reachability.
2. **Reconciliation consequence:** the edge reports the application unreachable; control omits its public route; Kubernetes consequently cannot acknowledge Programmed. Reapproving an already approved host or altering public DNS does not address the observed missing transport path.
3. **Readiness granularity:** forwarders report application reachability using `any` backend. `Session.Publish` then includes the application's primary Service in acknowledged names even on node 3, where webhook backends are not local. Its actual advertised-Service health list correctly excludes those webhook Services. Thus application/ack summaries alone are insufficient evidence that every HTTP backend is served. Per-backend readiness and acknowledgments should be authoritative.
4. **Endpoint continuity:** the separately reproduced Pod-endpoint hashing defect remains locally fixed but undeployed. That patch prevents endpoint-only route withdrawal; it does not repair a missing Service route. A changed HTTP rule/backend definition must still pass readiness and authorization.
5. **Latency:** contemporaneous control logs show 13–27-second load-balancer configuration operations and Service reconciliation deadlines; forwarders also logged configuration/report deadlines. These warrant bounded-operation diagnostics, but do not establish that database latency caused the missing peer route.

The appropriate next platform work is a protected in-process per-Service dial/peer-route diagnostic, a fail-closed tsnet-only path for TailVIPs, and backend-specific readiness/acknowledgment tests. Do not force Programmed, fabricate healthy reports, restore a route to a different backend without readiness, or use a shared-component restart as evidence of a permanent fix.

## Preservation and public checks

The nine pre-existing DNS ledger rows compare exactly equal to the prior snapshot, including IDs, owner values and remote provider IDs. Two additional Luong Gia rows already exist; this investigation did not create them. Edge device identity remains `n9YQSZEmrA21CNTRL`, PID 166842 throughout.

Public checks use normal DNS and certificate validation; no `--resolve`, TLS bypass, or private route bypass:

| Existing public route | Observed response |
| --- | --- |
| University API `/healthz` | 404 — failed acceptance |
| Admissions `/` | 200 |
| SSO `/login` | 200 |
| Eoffice `/` | 302 |
| Harbor health | 200 |
| Metube `/` | 200 |
| Voice `/` | 200 |

Phone exchange was not attempted. No invented or retained single-use tokens were used. Private diagnostic artifacts are under `/tmp/funnel-university-20261020/`; credentials and raw state are not included in this report. Short successful checks on unrelated routes do not establish long-term stability.
