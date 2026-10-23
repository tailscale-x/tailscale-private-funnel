# HCMULAW routing incident — 19 October 2026

## Scope

Investigated the existing admissions deployment from the application team's
`/tmp/ulaw-funnel-handoff.md`. Zalo consent/token retrieval was already working;
no phone token exchange, credential change, control transfer, Cloudflare Access
change, or application deployment was performed.

## Observed recovery

All four existing HTTPRoutes in `hcmulaw` changed to `Programmed=True` at
16:26:37 UTC, observed generation 2 matching their current generations. This
recovery preceded any live mutation in this investigation. The control and
forwarder Pods were not restarted, and the native load-balancer process remained
running. Do not attribute recovery to a new software fix.

Normal host DNS resolution and public HTTPS (certificate verification enabled,
no `--resolve` override) passed for:

| Host | Request | Result |
| --- | --- | --- |
| tuyensinhhcmulaw.troly.ai.vn | GET / | 200 |
| api.tuyensinhhcmulaw.troly.ai.vn | GET /healthz | 200 |
| eoffice.tuyensinhhcmulaw.troly.ai.vn | GET / | 302 |
| sso.tuyensinhhcmulaw.troly.ai.vn | GET /login | 200 |
| metube.troly.ai.vn | GET / | 200 |
| harbor.dlkn.dev | GET /api/v2.0/health | 200 |
| voice.troly.ai.vn | GET / | 200 |

Six consecutive rounds from 16:32:37 through 16:33:35 UTC passed all 42
public DNS/HTTPS checks. All resolved to the existing public load-balancer
address. The Zalo preflight
`OPTIONS /api/zalo/phone` with origin `https://h5.zdn.vn`, requested method `POST`,
and requested header `content-type` returned 204, with:

```text
Access-Control-Allow-Origin: https://h5.zdn.vn
Access-Control-Allow-Methods: GET, POST, OPTIONS
Access-Control-Allow-Headers: Content-Type, Idempotency-Key, Authorization
Vary: Origin
```

Native-device phone acceptance remains for the application team using fresh
single-use tokens.

## Investigation and remaining uncertainty

- Load-balancer logs during the incident contained private control configuration
  request timeouts and a Tailscale upstream `network is unreachable` error.
- Control logs contained repeated Service-approval reconciliation deadlines, plus
  Cloudflare record-listing and Tailscale endpoint-lookup deadlines. The latter
  explicitly retained the previous owned load-balancer A record.
- These messages identify failed operations, not the underlying origin of every
  deadline: a shared deadline may already be exhausted before an API call starts.
- Audit events show owned university DNS reconciliation progressing at
  16:26:33–16:26:44 UTC. Runtime calls and forwarder reports are fresh after
  recovery. Reconciliation deadline warnings still occur after public recovery;
  their disappearance is not an acceptance claim.
- A current PostgreSQL connection sample was idle; it cannot establish the
  database state during the earlier outage.
- The operator host's inability to resolve the private control Service must be
  evaluated separately from public DNS: its host identity has the old shared
  Funnel tag rather than the new installation-role tags. An Owner-device request
  to the canonical control Service worked. No ACL was broadened during this
  investigation.

The precise trigger of recovery remains unproven. This incident is **recovered,
with root cause open**, rather than evidence that the Service-reachability delay
has been permanently fixed. Do not use it to pass the separate release gate.

## Preservation evidence

Read-only comparison against the pre-investigation snapshot found all eleven
DNS ledger rows identical, including record IDs, values and ownership. The
`main-material` Secret data was identical. No credentials or identity material
were printed or committed. Existing URLs and application resources were retained.

Private raw logs and repeated public checks are stored on the operator host in
`/tmp/funnel-hcmulaw-incident-20261019/`; they are not release assets.
