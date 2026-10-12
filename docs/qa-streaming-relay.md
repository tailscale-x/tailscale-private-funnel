# SSE relay diagnosis — 10 October 2026

Historical diagnosis. The relay was later removed; current forwarding and rollout results are in [qa-direct-backend.md](qa-direct-backend.md).

A bounded request to `/v1/chat/completions` used `stream: true`, `max_tokens: 48`, and a synthetic prompt. Success required HTTP 200, a clean curl exit, and the SSE `[DONE]` marker.

| Segment | Before change | Observation |
| --- | --- | --- |
| Router's published port on 5090 | 2/2 complete | Model/router kept running; no OOM or restart. |
| Router's Docker network IP | 4/4 complete | Backend network was reachable. |
| Relay's Docker network IP | 4/4 complete | Raw relay completed streaming. |
| Relay's loopback published port | 3/9 truncated | This port was served by `docker-proxy`. |
| Public HTTP/1.1 | 2/2 truncated | Caddy logged `reading: unexpected EOF`; an earlier request returned 502. |
| Public HTTP/2 | 1/2 truncated | Curl reported `INTERNAL_ERROR` for the failed stream. |

The failure boundary was the relay's Docker loopback published-port path. The ingress supervisor now selects an approved Docker network address for the relay, verifies TCP reachability, and passes that address to `tsnet.ListenService` forwarding. New relay containers publish no host port. It retries network inspection while Docker finishes attaching a new container. No Docker socket or Tailscale credential is passed to a relay.

After the direct-IP change, 8 HTTP/1.1 and 8 HTTP/2 public requests all completed with `[DONE]`. The ingress process had an established connection to the relay's Docker IP. Both relay containers were replaced from the new image without host port mappings. After the final restart and relay replacement, Llama `/health` and Flue `/api/ping` both passed. Four HTTP/1.1 and four HTTP/2 SSE requests all returned HTTP 200 with a clean exit and `[DONE]`. Both relay containers run the final image, have empty Docker port bindings, and the ingress node retained its original Tailscale device ID. Full Go suites, vet, and diff checks passed.

Artifact: `dev-stream-direct-20261010-r2`, `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:278bf280608de0df8ba089366799f422369f6d91b6883af90b81cb5084c053d1`. Built locally; the VPS and 5090 hosts only pulled or received prebuilt artifacts. No stable tag or release was created.
