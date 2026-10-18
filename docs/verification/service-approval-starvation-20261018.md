# Authenticated Tailscale API timeout investigation

## Live evidence

DLKN control is Ready on v3.2.2, while Gateway programming waits for Service
approval. Basic outbound HTTPS was independently verified by the cluster task.
An additional probe in the **current control Pod's network namespace**, using
Funnel's available OAuth credentials, returned:

- Service-host list: HTTP 200, 0.716 seconds.
- Load-balancer device details: HTTP 200, 0.666 seconds.

The token was held in memory and passed to curl through stdin; it was not put
in arguments, Kubernetes resources, files, or diagnostic output. The temporary
diagnostic container has a five-minute sleep lifetime and holds no credentials.

Control logs show a load-balancer DNS timeout immediately followed by a batch
of Service-list context-deadline errors. The sequential maintenance loop used
one 45-second context for policy/DNS, report expiry, stale-node cleanup,
load-balancer DNS, and finally Service approval. Policy reconciliation also
attempted Service approval after its own DNS work. Earlier work can exhaust
that shared context before the authenticated Service calls start; the resulting
URL-bearing errors do not establish that the remote endpoint is unreachable.

## Local correction

- Give periodic Service approval its own serialized loop and fresh 45-second
  context, independently of slow DNS/provider work.
- Prevent concurrent policy and periodic Service passes from queuing behind
  each other while their deadlines expire.
- Reject already-expired contexts before loading credentials or issuing calls;
  stop iterating Service records once the context expires.
- Stop subsequent maintenance phases when their pass budget is exhausted.

Regression coverage verifies that blocked DNS cannot prevent repeated Service
approval with live contexts, canceled contexts perform no credential/API work,
and overlapping Service passes do not queue. Existing policy/ownership and
Service authorization checks are retained.

At the initial investigation this correction was not deployed or released. No stale Service/device records
were deleted, no ACL/network settings were changed, and no control restart was
performed by this investigation. Cluster rollout remains a separate operation.

## v3.2.3 deployment and additional status finding

The release passed CI and was deployed through the DLKN GitOps `FunnelControl`
image reference. Control became Ready with the same Secret UID, encryption key,
configuration signing key, tsnet machine key, addresses, database credentials,
provider configurations, zones, and enrolled node identities. All eight existing
owned DNS records were retained; control published the additional Harbor record.
Public Harbor health returned HTTP 200 with verified TLS, and its HTTPRoute became
Programmed=True. Service-approval deadline error batches stopped.

The parent Gateway remained Programmed=False because the status calculation
looked for the Gateway's resource ID among host route IDs. A v3.2.4 follow-up
aggregates the published route through its actual authorized parent/listener
attachment instead. Its regression test verifies publication and withdrawal and
keeps a second parent with a nonmatching hostname pending. This is a status
aggregation correction, not an approval or policy bypass.

## Final DLKN acceptance: v3.2.4

Both patch releases passed their full CI workflows. The final release is
[v3.2.4](https://github.com/tailscale-x/tailscale-private-funnel/releases/tag/v3.2.4),
commit `30551ab`; Service-approval isolation was committed as `22eb6bf`.

GitOps changes were committed to `m090.vn/m090-monorepo`, rather than overriding
Argo CD self-healing:

- Control image: `4db6ed9f831e90d7d6b6fc78776a9ff9c3288a6d`.
- Gateway image: `7e2cc19030b42e1df1398c1cdf271130aafd70ce`.
- Harbor front-door routing: `c5229d5fb43d9d629b16c5116e7d8c4c7aad06c0`.

Control, the gateway agent, and all three forwarders are Ready on the published
v3.2.4 image. Their actual image IDs match the release's published image index.
Control used Recreate; forwarders respected the per-node identity leases during
rolling replacement. The original identity Secret UIDs, control encryption and
signing keys, tsnet machine keys, broker TLS keys, agent identity, and all three
forwarder identities were retained. Control still uses `100.116.115.48` and
`fd7a:115c:a1e0::3236:7332`, with PostgreSQL ready.

Before/after database checks retain all 20 enrolled nodes, credential hashes,
provider configuration, managed zones, and every baseline owned DNS record.
Control added its owned Harbor record; the final ledger has nine records. The
private PostgreSQL archive and identity Secret snapshots are protected under
`/tmp/funnel-release-v3.2.3/` and `/tmp/funnel-release-v3.2.4/`; no credentials were
committed or uploaded. The external PostgreSQL database was not recreated.

`Gateway/dlkn-public` and `HTTPRoute/harbor` both report `Programmed=True` for their
current generations. Both Argo applications are Synced and Healthy. Harbor's
initial HTTPRoute targeted `harbor-core`, whose UI root legitimately returned
404; its healthy nginx front-door Service was verified internally and selected
through GitOps. Harbor now serves UI and API together.

Final five-round verification passed all 55 public checks with normal TLS
verification: Harbor UI/health 200, registry authentication 401, HTTP redirect
308, Voice/Metube/admissions/SSO/API readiness 200, and excluded API paths 404.
Admissions uses its currently served root page; its former `/login` path returns
404 from the local application too, independently of Funnel. No Service-approval
failure batches or duplicate-record errors appeared in the final observation.
No ACL, Cloudflare Access, or management-role changes were made.

Final observation: 2026-10-18 17:56:59–17:58:19 Asia/Saigon. Detailed receipts: `/tmp/funnel-release-v3.2.4/`.
