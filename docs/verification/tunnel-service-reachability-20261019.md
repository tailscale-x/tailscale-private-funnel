# Fresh Service reachability — 2026-10-19

## Reproduction and diagnosis

A load-balancer identity was started **before** creating the Mac tunnel's
application Service. The Mac's existing host identity remained untagged; it was
used only for phone-home bootstrap. The tunnel joined with its own ephemeral
Tailnet-gateway identity.

With Tailscale SDK 1.102.2:

- The Service's identity-scoped DNS lookup returned both TailVIP addresses.
- The Services API reported the host `approved:auto` and `ready`.
- The running client's `PeerForIP` lookup had no route for either TailVIP, and its
  peer map lacked the new Service host.
- A client started after Service creation connected immediately. DNS retries and
  reapplying the existing local map did not fix the running client.

The product now pins SDK **1.102.4**, which includes upstream fixes for incremental
peer-map handling, expiry refreshes that rolled back peer deltas, and peer-index
reuse. See [Tailscale's patch](https://github.com/tailscale/tailscale/pull/21121).
No debug-map refresh, identity restart, forced address family, or additional
Tailscale identity is used as a runtime workaround.

## Acceptance fixture

`TestLiveMacPhoneHome` is opt-in and requires `-tags caddy`, protected issuer and
Service credentials, and an external Mac runner. It starts Caddy and the
load-balancer tsnet identity before exposing the control URL to the runner. It
then reloads the route without restarting that identity and tests:

- Initial Service reachability within 45 seconds of receiving the application.
- Complete 1,040,014-byte SSE response with `[DONE]`.
- WebSocket exchange and a 35-second stream across report/configuration polls.
- Explicit withdrawal and ownership-scoped fixture DNS cleanup.

The fixture uses real Tailscale networking and Caddy but synthetic DNS. Public
DNS, TLS issuance, and deployed-version checks are recorded separately after
rollout. Cross-compilation alone is not Mac runtime acceptance.

## Results

Pending the fresh-client regression run and release rollout.
