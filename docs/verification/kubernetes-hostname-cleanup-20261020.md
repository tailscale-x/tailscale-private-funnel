# Kubernetes hostname isolation and Voice cleanup — 20 October 2026

## Implemented

- Control accepts the nonconflicting hosts in an authenticated Kubernetes snapshot. Foreign hostname claims are excluded from forwarding; their applications and ledger are untouched. Per-resource/hostname errors travel in `ConfigurationPayload.kubernetes_errors` and become route-specific `Programmed=False` conditions.
- Explicit Kubernetes gateway revocation withdraws its exclusive applications and registered children. Cleanup releases hostname claims transactionally only after owned DNS and unshared, control-created Service cleanup succeeds. Retain application rows, exposure IDs, retirement history, and audit evidence. Offline gateways and disabled applications alone do not release claims.
- Gateway cleanup visits only its assigned exposure partitions. An unavailable unrelated zone cannot block retirement. Completed cleanup returns recorded success without repeating external deletions.
- No database/schema migration, compatibility aliases, OAuth changes, permanent ACL changes, or host identity resets were introduced. Upgrade Kubernetes agents along with control to render the new per-resource diagnostics.

## Verification

Both Go suites, vet, targeted race tests, SQLite/PostgreSQL 16 and 18 storage contracts, reproducible RPC generation, unchanged embedded UI asset hash, image startup, and diff checks passed in an isolated v4.0.3 worktree. Authored changes were applied to the main checkout while preserving its separate foreground-tunnel work; regenerated merged contracts and focused main-checkout/race tests passed.

The live test added one temporary HTTPRoute claiming Docker-owned `metube.troly.ai.vn`. Only that route became `Programmed=False`, with a specific ownership error. The seven legitimate DLKN routes remained `Programmed=True`, and Metube's owner was unchanged. The fixture was removed afterward.

Regression tests cover conflict isolation/recovery, foreign ownership protection, route-specific conditions, administrator authorization, cleanup failure/retry, foreign Service hosts, shared applications/Services, transactional rollback, owned-ledger guards, preserved exposure IDs, completed-result idempotence, unmanaged DNS preservation, and unrelated-zone failure isolation.

## Development rollout

No stable tag or GitHub release was created. Builds ran locally; remote hosts did not compile anything.

- DLKN gateway/forwarders: `dev-v4.0.3-k8s-cleanup-20261020`, image `sha256:78b7928c5743a319465eec1b6d791f8cf886ec3a23b58973a8d90c9b59d6dd22`.
- Final control-only refinement: `dev-v4.0.3-k8s-cleanup-20261020-r2`, image `sha256:7f34e0c8c8bd1a14caa7eec1e7f689998f45c2714b8d890c9debb0f06a0c92bf`.
- Infrastructure GitOps commits: `c23b2d2eb60d42072a7f35fc62e9cf7798a58834` (initial fix), `c5e6bf3760e7769aac6eea6ad0eff73a317cf544` (Voice route reattachment), `d48a55483d4b8f4b5105c74d8f200363801761df` (control-only cleanup refinement).
- Native edge, native Docker gateways, other Kubernetes installations, and operators were not reset or upgraded by this fix. DLKN forwarders retained their instances during the final control-only rollout.

The initial disposable control/agent replacement encountered the known Tailscale Service-host map delivery race. The gateway agent was replaced once after the control Service was confirmed auto-approved/ready. The native edge was not restarted or reauthenticated. A later DLKN API/etcd disruption produced lease fencing; the gateway logged `etcdserver: request timed out`, node 3 reported DiskPressure, and the node-2 API server restarted. Forwarders reacquired their existing bindings after API recovery without Pod restarts. No cluster storage, ACL fencing, or authentication checks were bypassed. These are separate infrastructure limitations, not claims that the hostname fix resolves all Service-map or cluster-availability failures.

## Voice acceptance

Administrator API cleanup revoked only removed gateway `j_WYf5wjhh_SzFI6` and its registered forwarder, retired application `k8s-db33467abf4e5a828f2a0f95`, and released `voice.troly.ai.vn`. Cleanup status is `pending=false`, `operational_complete=true`.

The existing Voice HTTPRoute was reattached through GitOps. Normal DLKN reporting created application `k8s-be76a8fa059082ea808066de`. Existing exposure ID `WJnZZhOpeWNbqgaVIAJVjg` and its `exposure:WJnZZhOpeWNbqgaVIAJVjg` ledger owner were retained. Voice's Deployment, PVC, credentials, and backend configuration were not changed by this task. Its actual readiness endpoint is `/healthz`; `/health/ready` is not a Voice endpoint.

Final readback preserved all 11 ledger rows exactly, including Voice's original owner and public IPv4. The control `main-material` Secret UID and all data remained identical. The former gateway and forwarder are revoked; unrelated applications and owners remain intact.

## Admissions Facebook webhook

The public Facebook callback returned 502 while API `/healthz` returned 200. The dedicated Facebook Pod and direct Kubernetes Service health were good, but the existing edge had no route-map candidate for its separate Service VIP. Later live diagnostics showed the approved host, a userspace Tailscale connection, and a direct UDP path. No Meta credentials or subscription settings were changed.

Public callback tests used the mounted verification token and app secret only inside the application Pod:

- Correct callback challenge: HTTP 200, exact challenge echoed.
- Invalid signature: HTTP 403 `INVALID_SIGNATURE`.
- Correctly signed empty event: HTTP 403 `ACCOUNT_MISMATCH`, proving signature validation was reached without enqueueing a synthetic delivery.

No real Facebook user message, webhook subscription mutation, token rotation, or business-data backfill was performed. The separate upstream Service-map notification limitation remains; this task verifies current callback recovery rather than permanent immunity to that issue.

## Final public and security checks

At approximately 16:46–16:47 ICT, TLS-verified public requests returned HTTP 200 for Voice `/healthz`, all four admissions hosts, Harbor, Metube, and both LuongGia hosts. All eight attached DLKN HTTPRoutes reported `Programmed=True` at current generations. The continuous eight-host monitor recorded 2,456 requests, with over 15 clean minutes following the final observed infrastructure disruption.

The temporary administrator selector `node:n2hHrEVCJj11CNTRL` was removed through `SaveAccess` with confirmed self-removal. A subsequent administrator API call returned HTTP 403. `autogroup:owner` remains configured. The temporary source-IP grant to control TCP 8080 was removed with ETag protection, restoring the exact pre-QA policy bytes. No temporary diagnostic authority remains.

Private evidence, pre-change database backup, and deployment receipts are under `/tmp/funnel-k8s-cleanup-20261020/`; they must not be committed. Disposable PostgreSQL fixtures and the temporary conflict HTTPRoute were removed.
