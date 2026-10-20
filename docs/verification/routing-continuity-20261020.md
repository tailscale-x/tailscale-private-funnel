# Latest checkpoint — 2026-10-20 00:17 UTC

> Historical investigation checkpoint. Later builds and results are indexed in
> [the Service-route review](service-route-consolidation-20261020.md). Statements
> about running versions and availability below apply to this checkpoint only.

Harbor recovered before a separately authorized single native-edge restart. That restart preserved saved device identity and configuration; systemd hit its 60-second stop timeout before starting the replacement. Public Harbor, admissions, Metube and Voice pass post-start TLS checks. Harbor HTTPRoute is Programmed=True. See [the detailed checkpoint](harbor-service-routing-20261020.md). The underlying intermittent Service-route cause is not proven; the endpoint-equivalence patch remains undeployed. Earlier outage observations below are historical.

# Routing and DNS continuity — 20 October 2026

> Latest status: Harbor is publicly unavailable again after node-2 maintenance; internal health and Service approval have recovered. The owner is continuing the remaining VM resizes. See [the Harbor transport/continuity report](harbor-service-routing-20261020.md) and [issue #1](https://github.com/tailscale-x/tailscale-private-funnel/issues/1) for current evidence. Earlier successful observations below are historical.

## Deployment

Locally built development artifacts only; no stable tag or GitHub release.

- Control and Kubernetes agents/forwarders in DLKN, m090dev, m090prd, and 5090: `dev-continuity-20261020-r1`, image `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:1806263173ba45e83dba80b24071f09f7b20fc7de5df80ecd3f8c8841a47f346`.
- GitOps image commit: `99c1e8b478ba7982ab472a8c42522e2200c3ab3e`. Only component image references changed; operator deployments and policy were not replaced.
- Native xvps load balancer: `dev-continuity-20261020-r2`, SHA-256 `2bb087e4b6f431d4f97b1793ca9256a984add38238116e90dde9b96908acf190`. The additional r2 change starts from valid saved routes before waiting on control. Unit and native deployment-version metadata updated; state/data paths and device identity were preserved during that initial artifact deployment. The later recovery identity exception is recorded below.
- Native Docker gateways were not rebuilt/restarted: their discovery behavior already retains listeners on failed control resolution. The control-side change applies to their reports too.
- Kubernetes identities remain disposable. No persistent identity migration, ACL change, additional OAuth scope, Cloudflare Access change, or application credential rotation.

## Reconciliation changes

Missing, stale, empty, and negative health reports are not deletion intent. Existing owned A/CNAME records remain while durable application membership, managed zone, current policy, and nonrevoked gateways/load balancers authorize them. Existing ledger ownership is unchanged. New hostnames still require readiness; unowned overwrites still require preview-bound approval.

Previously readiness-validated load-balancer routes survive report loss and control replacement. Each response compares the saved route to the current exact Service target, port and HTTP rules, and rechecks application membership, current policy, source inventory, and revocation. Changed/new routes cannot be introduced through this fallback. An authoritative empty configuration replaces the saved set so withdrawals do not reappear.

Docker inventory-confirmed deletion/label removal, Kubernetes desired configuration removal, application disable/delete, hostname removal, policy denial, and revocation continue to remove their scoped routes. Stopped containers and failed discovery do not release ownership.

Report expiry discards health snapshots without revoking backend assignments or reconciling gateway-owned DNS to empty. Missing load-balancer health preserves the prior owned address. Service-host authorization uses acknowledged, still-authorized ownership rather than expiring ownership merely because reports/acks became old; Tailscale auto-approval policy remains authoritative.

Kubernetes forwarders no longer close listeners because control configuration/report delivery has failed for five minutes. Independent broker leases still fence assignment ownership; losing a lease still stops the forwarder. This change deliberately does not weaken fencing.

The load balancer atomically saves successfully applied routing with mode 0600, bound to its logical node ID, installation and control URL. Startup loads a valid cache before any control RPC. Failed fetches/reloads do not overwrite it; successful authoritative withdrawals do.

## Local gates

Passed in the isolated focused-fix worktree:

- `go test ./...` and `go test -tags caddy ./...` (r1 and final r2).
- `go vet ./...` and `git diff --check`.
- PostgreSQL 16/18 storage/RPC contracts, including transactional expiry preserving assignments.
- Connectivity-loss regressions: missing reports, fresh empty reports, unreachable backends, report sweeper, node sweeper, and fresh Control objects against the same store retain previously ready routes and owned DNS.
- Explicit policy denial, gateway revocation, application disable, hostname removal, backend changes, and HTTP-rule changes withdraw old routes without resurrection.
- Route-cache persistence, exact node/control/installation binding, file permissions, corruption detection, and authoritative empty replacement.
- Development image version/startup and deployed native binary checksum/version.

Focused tests also passed in the primary working tree, preserving its separate unfinished tunnel work. No tunnel implementation or protocol/UI changes were included in the deployed focused build.

## Live results and important remaining failures

The rollout is **not an all-green public-routing acceptance**. Before deploying this fix, ULAW and Harbor were already returning 404; Metube/Voice also suffered transient failures during recovery. ULAW subsequently recovered, then lost transport again when DLKN forwarders fenced themselves after broker lease-renewal timeouts. Their replacement devices were auto-approved, but the load balancer continued seeing Service dial failures. These remaining failures must not be described as fixed by the reconciliation patch.

The control and Caddy retained DNS and the previously ready ULAW routes during the later failures: requests failed with upstream timeouts/502 rather than DNS deletion or configuration-driven route removal. Harbor had already lost its old route before the new retention logic was installed and remained pending load-balancer readiness. Voice recovered after replacing only its affected disposable forwarder; Metube remained healthy in later checks.

The stored nine DNS ledger rows remained byte-for-byte unchanged across this deployment, including remote record identifiers and exact owner values. Control material Secret, database identity binding, all existing exposure IDs, and all twelve existing encrypted settings secrets were verified unchanged (secret plaintext compared privately, never logged). Native load-balancer device remained `nQtUoPTPxB11CNTRL`.

No live fault injection was enabled. Gateways and the native load balancer were updated/restarted for deployment and recovery. Two affected forwarder Pods were additionally replaced during recovery; this caused fresh disposable devices, not a reset of application IDs or ownership. An idempotent refresh of the already auto-approved Harbor/Voice hosts left their approval levels unchanged and did not resolve the transport failure. No new host or unknown Service was approved.

Read-only Service checks show current application tags and auto-approved/ready hosts. Current ACL already grants the load-balancer role TCP access to the application Service tag. An Owner Mac can reach application Services while the embedded load balancer reports dial failures. This is evidence for a separate client/Service transport investigation, not proof of a particular upstream bug.

Broker probes through a temporary authenticated port-forward returned configuration in 2.05/4.96 seconds and lease renewal in 0.41/0.38 seconds. That sample does not explain the intermittent Pod-side lease timeouts. Temporary bound-token material and port-forwards were removed after probing; no management administrator grant or Kubernetes RBAC was added.

The current API Pod declares `/health/ready` and `/health/live`; the old `/healthz` acceptance path now returns 404 directly through its Kubernetes Service. Use current readiness plus the unchanged public application URLs and Zalo CORS preflight for follow-up acceptance. Do not confuse an outdated health path with DNS failure.

## Follow-up boundaries

Investigate Tailscale Service reconnection and the broker lease timeout independently, preserving disposable identities and fencing. Do not compensate by opening ACLs, adopting unowned DNS, fabricating healthy reports, or restoring routes for new/unready targets. Disconnected components cannot receive new policy revocations until they reconnect; Tailscale network policy remains independently enforced.

Detailed private evidence is under `/tmp/funnel-continuity-20261020/`. These files contain operational state and are not release assets.


## DLKN cluster investigation — 2026-10-19 18:38 UTC

Read-only checks confirmed cluster-wide intermittent control-plane failures independent of Funnel:

- All three kube-apiserver logs contain localhost etcd Range/Txn deadline failures. Node 3 explicitly returned `etcdserver: request timed out` at 18:37:51 UTC, so this is not only historical startup noise.
- Node 2 logged a timed-out PUT to the Funnel assignment Lease at 18:11:16 UTC. Forwarder previous logs show lease renewals timing out at approximately 18:10:37 and 18:10:53 UTC before fencing. This supports Kubernetes API instability as a contributor; it does not establish the full causal chain or explain the separate load-balancer Service transport failure.
- Scheduler/controller-manager, API servers and Argo CD have health failures in the warning event history. The API server readiness request succeeded in 0.21 seconds during the sample and all nodes currently report Ready. A successful spot check therefore does not establish sustained health.
- Current nodes have 4 CPUs and about 8 GiB RAM each. Node 1 CPU PSI `some.avg10` was 88.18%, node 3 was 24.79%; no current MemoryPressure/DiskPressure/PIDPressure. Available root filesystem space was approximately 5.7–8.0 GiB.
- A follow-up sample found the PostgreSQL primary consuming approximately 2 CPU cores on node 1; Funnel control and gateway agent consumed approximately 0.087 and 0.009 cores. This sample does not prove PostgreSQL caused etcd stalls. CNPG reported Ready.
- CoreDNS, Flannel and kube-proxy workloads are currently ready. Actual cross-node network loss and etcd disk latency have not yet been measured.

No cluster resources, workload placement, ACLs, identities, database contents, or DNS records were changed during these checks. Do not restart etcd members or disable assignment fencing based on these observations. Next evidence needed: Talos etcd health/leader/commit and disk fsync latency, host CPU/I/O pressure, and inter-node connectivity during an incident.


## Follow-up recovery — 2026-10-19 18:50 UTC

Public routing recovered after load-balancer reauthentication and restart. Fresh checks returned 200 for admissions, API `/health/ready`, SSO `/login`, Harbor `/api/v2.0/health`, Metube and Voice, and the expected 302 for Eoffice. TLS verification succeeded for every hostname. Zalo phone OPTIONS returned 204 with `Access-Control-Allow-Origin: https://h5.zdn.vn`. All four HCMULAW HTTPRoutes report Accepted, ResolvedRefs and Programmed=True for observed generation 2. This supersedes the earlier negative public acceptance, not the unresolved cluster findings.

An isolated temporary client on the VPS, using SDK 1.102.2 and the same load-balancer role, reached API Service IPv4 and IPv6, Harbor and Voice twice. Its SDK dial plans selected netstack/Tailscale. Temporary credentials were passed through stdin and revoked afterward; its owned ephemeral device was logged out. No host-device tags or policy were changed.

A stopped-state diagnostic using the existing native identity did not complete login. That diagnostic did not carry the application's AdvertiseTags, so it was not a configuration-identical reproduction and must not be used as proof that the saved identity was originally corrupt. Native service restart then requested login. Recovery used a fresh short-lived, single-use role auth key passed only through stdin, with the original hostname, state/data paths and correct role tags. The service restarted successfully from the resulting saved identity without another key. Diagnostic executables on the VPS and temporary auth keys were removed.

**Identity exception:** the native load-balancer Tailscale device changed from `nQtUoPTPxB11CNTRL` to `n9YQSZEmrA21CNTRL` during recovery. Do not claim its network identity was preserved. Funnel logical component `bGC5KTzhD8srlsgL`, data/certificate paths, exposure IDs, and all nine prior DNS ledger rows were preserved; the ledger rows were compared byte-for-byte after recovery. Control and gateway identities were not reset during this follow-up. The old device was not explicitly deleted.

The restoration does not establish a durable Service-reconnection fix. Original live-client route state was not captured before restarting. Fresh-client success and post-reauthentication recovery narrow the transport issue but do not identify the vendor/client root cause.

At 18:43–18:46 UTC, DLKN node 2 I/O PSI full averaged approximately 46–53% over 60 seconds; node 3 approximately 17–20%. The PostgreSQL replica Pods showed approximately 67% and 38% full I/O pressure in another sample. These are stall measurements, not throughput or proof that PostgreSQL caused etcd failures. All three CNPG volumes use node-local OpenEBS LVM. Node 1 CPU pressure was also high. Recent node 1 API/etcd timeouts persisted after public recovery. Host/Talos etcd and underlying disk measurements remain needed; no Talos client configuration was found locally. No database processes, application workers, storage settings, replicas or fencing were changed.

Private recovery/diagnostic evidence: `/tmp/funnel-investigation-20261019/`. Do not publish raw infrastructure or credential-related evidence as release assets.

Final observation: 20 consecutive rounds / 140 public HTTPS checks passed over 213.3 seconds. At 18:54 UTC, node 2 still showed I/O PSI full.avg60=49.03%, and node 1 API logs included new etcd Range timeouts at 18:52:49–50 UTC. Public recovery must not be described as resolving DLKN infrastructure instability. No additional forwarder restart occurred during this observation.


## Harbor recurrence — 20 October 2026, approximately 06:39–06:42 ICT

A separate cluster investigator reported internal Harbor healthy, public Harbor timed out, and other edge routes remained operational. That investigator replaced only node-2 forwarder `lqt6z` with `wstwt`; its identity was preserved by the existing broker flow. This follow-up did not restart, reload or reauthenticate the shared edge, reset a gateway, change Service approvals/advertisements, edit ACLs, or mutate DNS.

Service API showed Harbor's current host auto-approved and ready. The Owner Mac reached Harbor's IPv4 and IPv6 TailVIPs with HTTP 200 while the public edge timed out. A fresh ephemeral SDK 1.102.2 client on the same VPS, carrying the exact load-balancer role, also reached both Harbor VIPs twice and selected netstack in its dial plans. API and Voice succeeded from that client too. Its stdin-only auth key was revoked; its own ephemeral device logged out and remote diagnostic binary removed.

A subsequent normal public check returned Harbor, API, Metube and Voice HTTP 200 with successful TLS verification. The edge stayed on PID 165155. Recovery timing is consistent with asynchronous Service convergence but is not proof: joining the diagnostic device and recovery were temporally close, and the old client's in-process netmap was not observable. Do not automate extra client joins as a fix or describe this as a proved etcd-caused outage.

The cluster investigator supplied aligned etcd term/index and no alarms, alongside slow Range/apply requests. CPU PSI was explicitly distinguished from CPU utilization; both PostgreSQL replicas were caught up. Those observations do not establish the cause of the per-client Service failure. The earlier request for Talos access need not be repeated while that separate investigator provides it.

Recovery proposal: preserve the shared edge and identities; capture client-specific dial-plan/peer-route data first. If a Harbor-only advertisement refresh is necessary, coordinate it on the exact existing Service/forwarder only; the currently deployed runtime has no safe targeted operation, so do not substitute a broad Serve reset, application deletion, or edge restart. No such mutation was performed. A protected read-only diagnostic on the existing runtime is needed before claiming a vendor-specific cause.

The incident report and coordination instructions are tracked in [project issue #1](https://github.com/tailscale-x/tailscale-private-funnel/issues/1). Private live-check evidence is in `/tmp/funnel-harbor-20261020/` and `/tmp/funnel-investigation-20261019/service-probe-recurrence.log`.

Harbor follow-up observation: eight consecutive rounds returned Harbor/API/public-university/Metube/Voice 200 with TLS verification; Luong Gia returned its expected login redirect (302). No transport errors. The watcher originally expected 200 for all hosts, so its 5/6 count is not a failed Luong Gia transport test. Temporary diagnostic device deletion was independently confirmed by Tailscale API 404, and edge PID remained 165155. Recommend leaving the recovered edge/advertisement intact; recurrence root cause remains unproven.
