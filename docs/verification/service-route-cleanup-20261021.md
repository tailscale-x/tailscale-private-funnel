# Service-route cleanup review — 21 October 2026

## Scope

Review the released investigation changes against the verified ICMP-only
Service-host discovery prerequisites. Keep product guarantees supported by
independent reproductions; remove duplicated implementation paths. Preserve
unrelated foreground-tunnel development in the original checkout.

## Consolidated

- Registration now has **one retry loop and error classification** for fresh
  startup and retained-edge recovery. Fresh startup keeps its two-minute budget;
  an existing component-bound cache permits background recovery until shutdown.
  Explicit denial remains fatal. Transient retries use the same two-second
  interval, replacing nested two-minute batches and their extra five-second gap.
  Retained-registration notices are bounded to once every two minutes.
- Kubernetes forwarders use the same **20-second drain helper** as Docker and
  Tailnet gateways. Remove their redundant second listener-close defer. Preserve
  shutdown ordering: drain, final report, close transport, release assignment.
- The diagnostic paths already share the same filtered map reader and transport.
  Keep that implementation rather than introducing another diagnostic abstraction.
- The operational runbook states the verified discovery prerequisites first.
  Older reports link to it as historical checkpoints instead of presenting
  unresolved hypotheses and source-only artifact status as current guidance.

## Retained, with independent evidence

| Guarantee | Evidence and regression coverage |
| --- | --- |
| Nonblocking report acceptance and atomic snapshots | Measured 26–48-second report handling; cancellation, rollback, nonce, binding, and SQLite/PostgreSQL contracts |
| Connectivity failure cannot authorize DNS/hostname deletion | Exact ownership and explicit-removal/expiry continuity tests; unchanged live ledger in released rollouts |
| Independent assignment renewal, fencing, same-identity recovery | Blocked configuration-fetch tests, lease-expiry/reacquisition tests, observed node outage recovery |
| Unchanged Service listeners survive config/backend changes | Listener and handoff tests; actual replacement-host acceptance |
| Drain existing streams before SDK advertisement cleanup | Real streaming tests and serialized close/reopen coverage |
| No host-network fallback for private Service VIPs | Reproduced kernel fallback and route-removal race; direct netstack guard tests |
| Upstream SDK expiry correction | Old SDK fails the upstream expiry/delta regression; pinned SDK passes |
| Bounded passive failure/recovery diagnostics | Redaction, cardinality/rate-limit and cancellation tests; distinguished missing map, backend failure, and node outage |

Remote-Pod continuity and the two-minute endpoint-gap grace remain explicit
availability behavior, **not fixes for peer discovery**. Existing hosts may dial
only verified ready endpoints for the authorized backend; first advertisement
requires a reachable local endpoint. Explicit removal, authorization loss, and
lease fencing take precedence. This cleanup changes none of those boundaries.

## Excluded experiments

No SDK fork, automatic map refresh, control-session reset, approval toggling,
synthetic peer route, diagnostic-client join, or identity replacement loop is
present or added. These experiments were already excluded from released code.
No new ACL edit or OAuth permission is needed for this cleanup.

## Verification

Added regression coverage for transient registration recovery, permanent denial,
raw transport deadlines, caller deadlines, and cancellation without another RPC.
Retained-edge startup/denial tests continue to exercise the actual RPC path.
Run both Go suites, vet, focused race coverage, and CI generation, provider/store,
operator, container and artifact gates before rollout.

The released rollout receipt records live acceptance separately from these source
and automated-test claims. A successful route check does not establish that
independent Kubernetes or application-database instability has been repaired.

## Further test-only consolidation

After the v4.0.7 runtime patch, consolidate the listener test layer without another
runtime release:

- Three overlapping Service-connection fakes now use one socket-allocation and
  counting implementation. Plain listeners and SDK-style drainable listeners
  remain separate behavior modes.
- Shared manager cleanup drains/cancels owned listeners on test completion,
  including tests that previously left asynchronous cleanup running.
- The host-withdrawal fake tolerates its normal final cleanup as well as the
  explicitly blocked withdrawal under test.
- Pool selection tests live together. The default remote-node rejection test
  injects a dialer that would succeed, so it cannot pass merely because a remote
  address is unreachable.
- All **37 named gateway tests** are retained, including their streaming, fencing,
  authorization, cleanup serialization, and backend-replacement scenarios.

Both Go suites, vet, gateway race tests, and the opt-in real-Docker SSE/replacement
fixture pass after consolidation. The real-Docker fixture owns and removes only
its uniquely named container/network and verifies complete streams through both
container generations without published backend ports.

These follow-up changes affect tests and documentation only; the deployed
v4.0.7 runtime artifact remains unchanged. Unrelated foreground-tunnel work is
preserved in the original checkout.
