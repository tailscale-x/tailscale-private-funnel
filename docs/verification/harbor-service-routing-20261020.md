# Harbor Service route loss after node maintenance

> Historical investigation checkpoint. Later builds and results are indexed in
> [the Service-route review](service-route-consolidation-20261020.md). Statements
> about running versions and availability below apply to this checkpoint only.

## Current checkpoint: authorized edge restart

At 00:15 UTC on 2026-10-20, the owner authorized exactly one ordinary native edge restart with unchanged binary, configuration and saved identity. Harbor had already recovered beforehand; this restart is not evidence of the cause of that recovery.

- Saved device ID remained `n9YQSZEmrA21CNTRL`; logical deployment and configuration were unchanged. A private root-only state/data snapshot was saved before restart. No new key, reset or reauthentication was used.
- The old PID 165155 exceeded systemd’s 60-second stop timeout and was killed by systemd. New PID 166842 started at 00:16:31 UTC; Caddy was listening by 00:16:34 UTC. Public requests failed during the stop interval. This exposes an unresolved graceful-shutdown delay.
- Post-start checks returned Harbor health 200, registry `/v2/` 401 (expected unauthenticated challenge), admissions API/site/SSO 200, Eoffice 302, Metube 200 and Voice 200, with successful TLS verification. Harbor HTTPRoute generation 2 reported Accepted, ResolvedRefs and Programmed=True.
- Neither this short observation window nor the restart establishes a permanent repair for the earlier client-specific Service-route loss. The endpoint-equivalence fix below remains local and undeployed.

## Earlier status and coordination (superseded)

Harbor is currently unavailable publicly after node-2 maintenance. Internal backends recovered; approval is not the remaining blocker. The owner chose to continue the remaining VM resizes with Harbor unavailable. Further live recovery/deployment is paused during node shutdowns.

Coordination: [issue #1](https://github.com/tailscale-x/tailscale-private-funnel/issues/1).

## Confirmed transport evidence

- Harbor's current host was `approved:auto`, `configured: ready`. Its application, gateway membership, policy revision and exposure ID remained intact.
- Owner-client HTTP health requests to both IPv4 and IPv6 Service VIPs returned 200.
- A temporary same-VPS, same-SDK, same-role tsnet client reached both families. Its independent ephemeral diagnostic device was removed (verified API 404), key revoked, and remote executable removed.
- The existing edge PID 165155 was observed in kernel SYN-SENT from its public eth0 address to Harbor's private IPv4 TailVIP. SDK UserDial can fall through to SystemDial when PeerForIP cannot resolve a route. The actual selected peer/netmap was not exposed by the deployed binary, so the exact upstream cause is not established.
- Both families timed out through a temporary loopback-only Caddy diagnostic server using the existing process-owned dialer. This required graceful Caddy config reloads, not a process restart. The diagnostic server was removed and public server configuration compared equal before/after.
- An unchanged-definition Service refresh retained both existing VIPs, tags, port and name and did not restore reachability. Initial requests omitting API-required existing addresses were rejected without mutation.
- One exact existing coordination TCP socket was disconnected after PID/five-tuple verification. The same process established a new coordination connection; no restart, authentication or identity reset occurred. Harbor remained unavailable. Twenty-four rounds kept university API/public site, Metube and Voice at 200 with valid TLS, while Harbor was 404. This does not prove every long-lived stream was uninterrupted.

Do not treat approval success as end-to-end readiness or add host-network routes/grants to bypass the selected identity. The TailVIP fallback needs a fail-closed selected-tsnet path; it must not silently borrow host networking. No such transport patch was deployed here.

## Reproduced continuity defect and local fix

`sameRoute` compared every HTTPRule backend field, including Pod addresses, UIDs and node metadata. Ordinary Pod replacement therefore looked like an altered public Caddy route and could drop the previously validated route during Service convergence.

The local fix normalizes only `Backend.Endpoints` out of route-equivalence hashing. It leaves Service/backend identity, target, port, weight, matches, rewrites, filters, limits and policy checks intact. Stored gateway discovery data remains unchanged. Published-host acknowledgment uses the same equivalence function.

Regressions reproduce failure before the fix and pass afterward for new Pods, a temporarily empty ready-endpoint list, and node replacement. Backend UID/Service/port/weight/path/rewrite/header changes remain rejected by retention. Existing revocation/policy/removal tests pass.

Passed in the isolated focused worktree:

- `go test ./internal/web ./internal/proxy`
- `go test -tags caddy ./internal/web ./internal/proxy`
- `go vet ./internal/web ./internal/proxy`
- `git diff --check`

Focused continuity tests also pass in the main checkout alongside its unfinished tunnel work. No local continuity patch, new binary or image was deployed. The patch prevents future endpoint-only withdrawal; it does not repair client Service transport or reconstruct a route already dropped from sent configuration.

## Next recovery boundary

A protected in-process route diagnostic and a fail-closed TailVIP dial path are needed. Resetting only the coordination socket did not repair this incident. A normal native service restart with unchanged config/state could establish a new in-memory map session without a new identity, but briefly interrupts edge connections and requires separate coordination under the current no-edge-restart constraint. Do not run another server against its saved identity or reauthenticate to gather diagnostics.

Private evidence: `/tmp/funnel-harbor-20261020/`. Raw goroutine profile is mode 0600 locally and its remote copy was removed. Do not attach raw profiles/netmaps/credentials to public issues.
