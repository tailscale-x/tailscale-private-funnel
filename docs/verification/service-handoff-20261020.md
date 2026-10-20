# Service lifetime and Pod-move verification — 20 October 2026

> **Historical checkpoint.** The missing ICMP discovery grants were established
> later. Use the [current diagnosis and runbook](../service-route-diagnostics.md);
> unreleased/unresolved statements below refer to this checkpoint's time.

## Scope and result

Implemented in the checkout and tested using isolated fixture Pods on real DLKN
nodes. At the time of these isolated tests, existing Funnel components were not
updated. The subsequent [development rollout](service-lifecycle-deployment-20261020.md)
is recorded separately, including its interruption, recovery and preservation
checks. No stable tag or release was created.

Two failures were tested separately:

| Test | Observation |
|---|---|
| Newly advertised Service, already-running client, no old host removed | Client lacked a matching peer throughout the 43-second observation window |
| Identical stream without withdrawal | All 20 chunks and `DONE` received |
| SDK listener closed during an established stream | Stream truncated |
| Advertisement removed before stream completion | Streams could truncate; a replacement advertisement did not guarantee the client learned that host |
| Patched embedded listener quiesce, final SDK cleanup after streams finish | All 20 chunks and `DONE` received |
| Real backend Pod moved node 1 → node 3 → node 1 | Same forwarder device IDs and Service VIP; old host served verified remote backend without Service re-election |
| Overlapping ready backend replacement, Kubernetes deletes old Pod with HTTP shutdown drain | 44 consecutive requests succeeded; terminating Pod's stream completed all 21 lines; same forwarder IDs |

The route-propagation failure occurred without any old-host shutdown, so missing
drain alone cannot explain it. No policy edits, client reconnects, production
component restarts or identity replacements were used to refresh maps.

## Lifecycle changes

- Backend address/dialer updates atomically replace the target for **new TCP
  connections**. Existing connections retain their original upstream. No listener
  or advertisement is recreated for a target-only change.
- Kubernetes hosts initially advertise only after a **local** ready backend is
  reachable. Once advertised, they prefer local endpoints but retain verified
  remote endpoints from the same authorized backend during Pod movement.
- There is no unrestricted ClusterIP, arbitrary-address or host-network fallback.
  Controller Pod/Service/EndpointSlice validation, namespace authorization and
  NetworkPolicies still apply. This changes the former strict local-only rule and
  may add a cross-node hop.
- An existing listener has a two-minute unreachable/empty-endpoint grace, evaluated
  on successful configuration polls. It does not invent a backend or make an
  unavailable application healthy. New hosts cannot advertise using grace.
- A deliberate embedded listener stop first quiesces its raw acceptor, keeping SDK
  Serve state and advertisement references while established streams finish. Final
  SDK cleanup happens afterward. Draining is bounded to 30 seconds, with a
  20-second process-shutdown budget before closing the process-owned tsnet stack.
- This deliberately defers advertisement withdrawal: early withdrawal reproduced
  loss of the client's existing route. It is **not** a claim that the pinned SDK's
  host-election drain is seamless. New connections to a quiescing host can fail
  until another host is selected.
- Revocation/configuration withdrawal and assignment-lease fencing immediately
  cancel forwarding, including streams already draining. They do not wait for
  graceful shutdown. A pending final cleanup is serialized against reopening the
  same Service/port, preventing old cleanup from deleting a replacement entry.
- Native host-daemon backend updates mutate the exact owned forwarding entry
  without withdrawing it first. Daemon-owned stream draining is not claimed by the
  embedded-listener tests.

## Real Pod moves

Disposable namespaces were created for baseline, first implementation and final
fixture tests, all labeled `app.kubernetes.io/managed-by=funnel-handoff-qa`. Test
workers had no Kubernetes API token or permissions, no TUN and no host network.
Short-lived one-use keys were scoped to the existing installation roles; OAuth
credentials stayed on the authorized test machine. A temporary Kubernetes Secret
supplied each test key. Fixtures made no Funnel runtime registration or DNS calls.

The Go fixture under `internal/gateway/testdata/handoff` uses the production
`ListenerManager`, endpoint pool and selected tsnet transport. Its management
listener is Pod-loopback-only and reached through authorized `kubectl port-forward`.
The driver supplied endpoint snapshots from actual Pod IPs, Pod UIDs and Node
UIDs, excluding terminating Pods. This exercised the production forwarding and
lifecycle primitives; it was **not** a rollout of the entire controller/broker.

The first moves deliberately deleted the sole backend before its replacement was
ready. There were 35 failed probes during the no-backend/discovery windows, and 21
successful probes. Once the new ready backend snapshot was applied, end-to-end
HTTP recovered in approximately 1.2–1.3 seconds (including both host configuration
requests). This is not a zero-downtime claim for deleting the only application Pod.
The client still selected the original host after the move; that host forwarded to
the verified remote Pod, without requiring the replacement Service route.

The overlapping test started the replacement Pod and verified readiness first.
The old Pod's HTTP server performed graceful `Shutdown` on SIGTERM. Kubernetes
removed it from eligible discovery while an existing stream finished. During that
move all 44 HTTP probes succeeded and all 21 stream lines arrived, including
`DONE`. Killing a Pod without application draining cannot migrate its TCP streams.

## Preservation and concurrent activity

- All **11 DNS ledger rows** exactly match the pre-test snapshot.
- Exposure IDs/application bindings/hostnames exactly match.
- Control material Secret UID and encrypted/signing material match the prior
  verified snapshot.
- Test worker device IDs remained unchanged across both Pod moves and the rolling
  replacement. Existing control, native components and other recorded device
  bindings were not changed by this test.
- One live node-1 production forwarder was independently replaced during the run:
  Kubernetes records the old Pod stopping at **20:56:26 Asia/Saigon** and the new
  Pod starting at **20:56:37**. The recorded device binding changed accordingly.
  These tests issued no mutation to the production namespace, DaemonSet or node.
  The initiating actor is not established by these events. Therefore an
  installation-wide unchanged-identity claim would be false.
- All disposable namespaces, bootstrap Secrets, Pods and three explicitly named
  test Tailscale Services were removed. No production device was revoked.

After cleanup, TLS-verified HTTP health probes returned 200 for Voice, admissions
API/public/SSO/eOffice, Harbor, Metube and LuongGia Explorer/SSO. This is a point-in-
time public check, not evidence that the upstream Service-map issue is fixed.
Creating test peers can itself generate unrelated tailnet map updates, so public
recovery cannot be attributed to this patch; existing components still run their
previous binaries.

## Verification and reproduction

Unit/race contracts cover stable listener updates, retained streams, bounded
continuity, local-first verified remote selection, local reachability before first
advertisement, cleanup/reopen serialization, immediate fencing/revocation and
native owned-entry updates. Both Go suites, focused race tests, vet and diff
checks passed in the isolated implementation tree. Both complete Go suites and
vet also passed in the working checkout after merging without replacing its
unrelated changes. Run:

```sh
go test ./...
go test -tags caddy ./...
go test -race ./internal/gateway ./internal/tsconnection ./internal/app
go vet ./...
git diff --check
```

For opt-in live repetition, build `./internal/gateway/testdata/handoff` locally with
`CGO_ENABLED=0`, package it in a restricted nonroot image, and use an isolated
namespace. Provide temporary role-scoped auth keys through Secrets, never issuer
credentials. Run one test host on each selected backend node and one client on a
third node. Use the loopback fixture API to record `/status`, configure actual ready
Pod endpoints through `/configure`, inspect `/map`, and issue `/probe` requests.
Measure advertisement propagation **before** any withdrawal. Repeat streaming
with `/drain` separately. Move only disposable backend Pods; maintain overlap and
application shutdown draining for the availability test. Record device IDs before
and after, then delete only the labeled fixtures and exact test Services.

Private driver scripts, snapshots, logs and detailed probe results are under
`/tmp/funnel-handoff-20261020/`; do not commit that directory. Fixture image tags are
QA artifacts, not Funnel releases. Do not use them as a component image.
