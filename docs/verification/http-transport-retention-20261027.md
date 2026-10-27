# Funnel HTTP transport retention: xvps and Kubernetes Control

Investigation uses the live v4.1.1 deployment and isolated local regressions.
The task is dated 27 October 2026; live samples at approximately 18:35–18:45 UTC
are **28 October, 01:35–01:45 Asia/Saigon**. No service was restarted, no live
configuration was changed, and no identity was copied into a running process.

## Finding

There is a reproducible HTTP connection-retention defect. The load balancer's
30-second polling loop repeatedly calls `tsnetHTTPClient(s)` for configuration,
acknowledgment, health and backend reports. `Embedded.HTTPClient()` in v4.1.1 calls
`tsnet.Server.HTTPClient()` each time. That SDK method constructs a new
`http.Transport`, with no IdleConnTimeout, rather than sharing a connection pool.
A completed request leaves its transport, idle connection and HTTP read/write
workers reachable indefinitely. Losing the client wrapper does not close them.

The server side also accumulates these connections. Funnel Control's HTTP server
has no IdleTimeout. The SDK's `ListenService` creates a loopback TCP listener and
forwards incoming Tailscale Service connections to it. Each abandoned client
keep-alive therefore retains a loopback connection pair and associated proxy and
network-stack resources in Control as well as resources in the edge client.

Host-mode clients already had a 90-second idle timeout but created a separate
pool on every call; reuse also removes that unnecessary transient accumulation.

## Live evidence

- Kubernetes Control RSS grew from **159 MiB at 07:00 to 1,963 MiB at 01:35 the
  following day**, approximately 18 hours 35 minutes, in one unchanged Pod. Its
  memory limit is 2 GiB. This is a growth timeline, not just one high reading.
- A process memory rollup had **2,009,852 KiB anonymous memory**, with no swap.
  Cgroup file cache was only about 14 MiB; this was not predominantly page cache.
- Its file-descriptor inventory had **16,464 socket descriptors**. TCP inventory
  showed 16,458 established connections, almost all 127.0.0.1 pairs to the
  ephemeral local listener used by its Tailscale Service. Only a handful were
  external API connections. This excludes Cloudflare API connections as the
  dominant socket population in that sample.
- Prometheus history shows socket counts rising with Control RSS. The preceding
  Control Pod had repeated sawtooth growth to roughly 720–740 MiB, recorded
  OOMKilled state, and restart count increasing from 7 to 12 in the retained
  three-day window. The current Pod had no OOM kill at collection time, but its
  cgroup had reached its memory maximum three times.
- Freshly rebooted xvps had **249 goroutines**, including 69 persistent HTTP
  reader/writer pairs. A later read-only sample had **301 goroutines**, including
  93 such pairs. The growing goroutines are waiting on the gVisor Tailscale
  connections, matching the polling transport lifecycle in source.
- Fresh xvps heap samples were about 48–50 MiB allocated and about 60 MiB in-use.
  The first sampled in-use heap profile attributed about 30 MiB to WireGuard
  message buffers. Process RSS was around 137 MiB, including about 82 MiB of
  file-backed executable mappings. Those healthy-start samples do not establish
  a long-term plateau and cannot reconstruct the pre-reboot 1.5 GiB heap.
- Other Kubernetes components in the initial sweep were much smaller: agents
  about 80–117 MiB, forwarders about 31–115 MiB, operators about 32–36 MiB.
  The large Kubernetes process was Control, not every Funnel Pod.

The resource-lifecycle bug, live worker growth and accumulated Control sockets
are established. Exact attribution of every byte of historical xvps memory and
all Control heap objects remains unmeasured: the old edge process is gone and
Control v4.1.1 does not expose a live heap endpoint. No forced live GC, debugger
attachment or process-killing stack dump was used.

## Regression and fix

A local test constructs 200 successive RPC HTTP-client wrappers and sends one
complete request through each to a disposable backend. The unpatched code opens
**200 connections** and fails. The fix opens **one connection** for the same 200
polls. Closing idle connections through a component-bound wrapper reaches the
shared transport and the next request opens exactly one replacement connection.

The patch:

1. Makes each Embedded/Host connection owner retain one HTTP transport pool.
   Callers receive independent `http.Client` copies so changing one timeout or
   redirect policy does not race other callers.
2. Reuses the existing bounded HTTP transport configuration: 90-second idle
   timeout and 100 total idle connections. The pool is per connection owner,
   never global across identities. Closing the owner closes its idle pool.
3. Propagates `CloseIdleConnections` through the component identity-header
   transport wrapper.
4. Gives Control a 90-second HTTP IdleTimeout, including protection from old
   clients, and a five-second ReadHeaderTimeout. IdleTimeout runs between requests;
   it does not impose a response/upload/stream duration limit. A regression holds
   an active response longer than the test idle budget, then verifies its full
   response and subsequent idle closure.

Validation passes: full ordinary Go suite, vet, Caddy app/transport/proxy suites,
and ten race-detector repetitions of the new pool/idle regressions. Concurrent
client creation shares one transport while retaining independent mutable client
options. The Caddy suites use an isolated network namespace because the host
has IPv6 loopback disabled; host network settings were not changed.

The patch is prepared on `wip/http-transport-retention`; **not deployed**. This
work is separate from the pending hostname Cloudflare-proxy feature. Both the
original dirty checkout and that feature branch remain preserved.

## Rollout and acceptance

Upgrade Control and the public edge with the reviewed fix while preserving their
existing state and identity. Control's new idle timeout also bounds abandoned
connections from old clients; edge pooling prevents their creation. Because
service replacement has previously interrupted traffic, deployment requires the
normal coordinated rollout, continuous public health checks and rollback to the
prior binary/image. Do not change DNS, routing policy, application deployments or
identity state as a memory workaround.

After deployment, compare socket/goroutine counts and heap/RSS at comparable load
for at least a full prior growth cycle. Expect connection counts to reach a
bounded plateau and stale sockets to disappear. A reboot or lower fresh RSS is
not sufficient acceptance. A Go memory budget can provide additional headroom
protection once the corrected baseline is measured; it does not repair leaked
live connections and is not a substitute for this fix.

## Current health and retained evidence

The actual babiCloud app readiness, SSO readiness and configured discovery path
all returned trusted TLS/HTTP 200 during the investigation. Workloads were ready
in all three clusters. The old generic sweep's Harbor URL returned 404, and the
`harbor` namespace no longer contained an HTTPRoute; this is not counted as a
passing Harbor check or attributed to the memory bug. No route was recreated.

Private raw heap/goroutine profiles, process memory and file-descriptor snapshots,
Prometheus history, and test logs are under `/tmp/funnel-memory-20261027/` with
restricted directory access. Do not publish raw profiles or operational snapshots.
The app team's original evidence is in
`/home/ubuntu/m090-monorepo/reports/babicloud-public-tls-20261028/FUNNEL_MEMORY_HANDOFF.md`.
No GitHub issue comment or message to another team was posted by this task.
