# Control update continuity and release acceptance

Date: 2026-10-17. Development artifacts only. No stable tag or GitHub release.

## Causes and fixes

- Broker authorization lookups made ownership-lease renewal depend on control
  availability. Previously verified membership now survives transient outages for
  no more than five minutes. Unknown identities are denied. Explicit denials and
  invalid signatures invalidate the cache.
- Forwarders closed listeners after forty seconds without a new configuration.
  They now use the original signed configuration's five-minute validity window;
  cached responses never refresh signatures or extend expiry. Local Kubernetes
  token/Pod validation, backend probes and lease fencing remain mandatory.
- Forwarder heartbeats waited on a mutex held during provider reconciliation.
  Their database-only acceptance path now runs independently. Transactional
  checks reject revoked children/parents and prevent late backend resurrection.
- Readiness now represents local forwarding under a valid configuration and
  ownership lease. A report-delivery failure alone does not stop healthy traffic.
- Access changes support explicit self-removal only when the replacement retains
  a real active/idle tailnet Owner, verified with `users:read`. The confirmation is
  one-shot, audited and not stored as an authorization setting. The UI clears it
  after saving and refreshes authenticated identity.
- Tailnet exposure deletion keeps its disabled application ownership record until
  exact owned Service cleanup succeeds. Shared Services and foreign hosts are
  protected. Provider failure leaves cleanup retryable.
- Generated update instructions use the selected release or explicit version,
  rather than accidentally downgrading to the enrollment version.

## Automated and browser gates

Both Go suites, vet, provider contracts, envtest, generated RPC/CRD/asset
reproducibility, Helm checks and packaged AMD64/ARM64 byte verification pass.
Native Linux amd64/arm64 and macOS arm64 binaries are built locally. macOS was
cross-compiled; no macOS runtime acceptance is claimed. A transient disk-space
link failure during parallel builds was resolved before repeating the full gates.

Tests cover bounded outage survival, expired configuration, explicit denial,
invalid signatures, forged Pod UID rejection, revocation races, nonblocking
heartbeats, self-removal safeguards, Service cleanup failure/retry, shared/foreign
Service preservation, and update-version selection. Chrome checks at 1440 and
390 pixels verify the confirmation, required-confirmation error, one-shot reset,
responsive layout and refreshed identity. The negative permission test produces
an expected HTTP 400; no unexplained browser errors were observed.

## Live maintenance evidence

After gateway rollout converged, two deliberate control outages held the stopped
control offline for ninety seconds each. The operator was temporarily paused to
prevent it undoing the test, then restored. Old identity owners were fully stopped
before replacement. Control became Ready again about eleven seconds after restart.

- 1,330 public HTTPS checks across Voice's two domains, Flue, MeTube and a
  disposable echo exposure: zero failures in the maintenance window.
- All eighteen enrolled node identities are unchanged. Voice's four forwarders
  across dlkn/5090 retain the same Pod UIDs and restart counts.
- One public WebSocket carried 180 echoed frames on a single connection across
  the second outage and recovery. The Voice certificate fingerprint is unchanged.
- Original Host and trusted forwarding headers pass; malicious incoming forwarding
  metadata is removed. HTTP redirects return 308. Chunked streaming passes.
- Stopping the disposable backend returns 502; restarting restores 200.
- UDP DNS sampling returned four empty recursive replies out of 265 lookups.
  These were not authoritative responses. Direct authoritative TCP queries have
  the AA flag; the subsequent control-replacement test passed 34 TCP-DNS/TLS checks.
  Cloudflare record IDs and creation/modification timestamps predate both outages
  and remain unchanged. Google and Cloudflare DoH independently confirm the live
  Voice records and NXDOMAIN for the removed disposable exposure.

Initial gateway rollout/warm-up was outside this maintenance window. An early
WebSocket opened while routes were still being added closed on configuration
change; the subsequent steady-state three-minute connection passed. This is not
an assertion that arbitrary route deletion or workload replacement preserves
existing connections.

## Final artifact and cleanup

The final control-only follow-up is `dev-operator-20261017-r34`; gateway agents,
forwarders and operators run r31. Existing native load balancer and gateways remain
on r27, exercising the unchanged node contract. All builds happened locally;
servers pulled prebuilt artifacts. Two orphaned Services from earlier QA fixtures
were removed using their exact recorded names and verified host ownership. Future
exposure deletion completes Service cleanup through control before dropping its
application record. No DNS record was mutated outside control.

Final image: `sha256:e30af82c3b185e53f3eccad688ecafd98a22de6850d85c43097de97994118268`.
Operator chart `0.0.0-dev-operator-20261017-r34`:
`sha256:0e05cc9bdde6d68487a1ecd7f172aac4aef0eb2375e692262a6e92a9dfe10691`.

The first final-version upgrade candidate (r33) exposed intermittent Voice failures
again. Its logs showed OAuth/device API timeouts being treated as authorization
denial. This was a failed gate, not a passing acceptance result. r34 distinguishes
unavailable authorization from denial: it retains an already approved host while
fresh authenticated reports, acknowledgments and policy remain valid; it cannot
approve a pending host without verification. Explicit policy denial/revocation
still withdraws the host. Dependency failures remain visible as diagnostics.

Control now reuses concurrency-safe, expiry-aware OAuth token sources keyed by
credentials and tailnet. Rotating credentials creates a fresh source. Tokens remain
in memory. The endpoint follows [Tailscale's OAuth documentation](https://tailscale.com/docs/features/oauth-clients#tailscale-oauth-token-endpoint):
`https://api.tailscale.com/api/v2/oauth/token`. Token lifetime/renewal is separate
from the five-minute forwarding-configuration window.

Final r34 upgrade acceptance:

- 128 direct authoritative TCP DNS and public HTTPS checks: **zero failures**
  throughout the actual image-version update. The control Tailscale identity is
  unchanged. External API timeout diagnostics occurred without withdrawing traffic.
- Both Voice domains, Flue and MeTube return HTTPS 200 after QA access removal.
- The disposable exposure was deleted through control. Its owned Service returns
  GET 404 from Tailscale; both Google and Cloudflare DoH return NXDOMAIN for its
  hostname. The two older orphaned test Services also return 404 after exact-name,
  host-verified fixture cleanup. The echo backend was stopped.
- Explicit self-removal succeeded after a live Owner verification. The returned
  administrator mapping is only `autogroup:owner`. A subsequent `manage whoami`
  from the tagged QA node is rejected with `permission_denied` (exit 3).
  No database bypass or permanent diagnostic selector was used. A further stopped-state
  control restart retained this denial and passed 40 more authoritative TCP DNS/HTTPS
  checks with zero failures; the bootstrap administrator was not restored.
- Temporary bootstrap scripts were already removed. The task-exported kubeconfig,
  tunnel and browser fixture are removed at the end of this verification.

## Release decision

The requested issues and the additional cleanup/update-command blockers are closed
and pass live acceptance. The implementation is ready for release-candidate review.
A stable release still requires review
and commit of the complete working tree, green CI from that commit, and artifacts
built from that immutable source. The current development image is not a stable
release. No full Gateway API conformance, control-plane HA, physical-host reboot,
or untested DNS-provider live acceptance is implied. Maintenance beyond the
five-minute authorization/configuration window fails closed.

## Historical post-acceptance infrastructure incident

After the passing acceptance runs and persistent QA-grant removal, a final check
found all three m090 API endpoints (10.3.60.103/.107/.110:6443) unreachable from
both this PC and 5090. dlkn control and the cluster gateway/forwarder peers showed
offline; `voice.dlkn.dev` then timed out. The selected subnet router MFLNKTOB2
(100.97.135.21, route 10.3.60.0/24) remains online and answers a Tailscale ping.
This is an infrastructure/routing investigation, not evidence that the earlier
acceptance failed, and no ACL mutation was made by Funnel.

**At that check, stable promotion was held** until cluster connectivity and current public
routing recover and are verified again. The requested code fixes and IAM cleanup
are implemented and passed their gates; current live availability is not claimed.
A working jump/Talos endpoint or restoration of the subnet path is required for
further infrastructure diagnosis. No stable release was published.

### Release recheck after cluster removal

The operator confirmed that m090 was removed. A subsequent release preflight on
2026-10-17 still found the dlkn control identity offline and its private HTTP
endpoint unreachable. Both `voice.troly.ai.vn/healthz` and
`voice.dlkn.dev/healthz` timed out. The 5090 Voice workload remains ready, but its
Funnel forwarder is not ready and reports `control authorization unavailable`.
The dlkn API at 10.3.60.107:6443 also timed out; 10.3.60.110:6443 is now reachable.

Cluster removal does not establish a passing routing gate. The control destination
must be confirmed and restored or reprovisioned before repeating public routing
acceptance and publishing a stable release. The temporary QA administrator grant
remains removed; no grant was reintroduced during this check.

### Resolved through fresh native control

At the user's request, control was recreated natively on xvps. The remaining
nodes were re-enrolled and Voice, Flue, and MeTube recovered. Exact DNS reclaims
required explicit user approval; unrelated and removed-cluster records stayed
untouched. The temporary bootstrap grant was removed again. Final acceptance,
release checks, and the bounded continuity evidence are recorded in
[the v3 report](qa-v3.0.0-release.md). The earlier hold is historical.
