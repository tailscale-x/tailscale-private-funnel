# Opt-in live handoff fixture

This is an isolated QA executable, not a Funnel runtime mode or installer.
Build locally from the repository root:

```sh
CGO_ENABLED=0 go build -o handoff ./internal/gateway/testdata/handoff
```

Package it in a nonroot static image with CA roots. Mount a writable `/state`
emptyDir and a short-lived single-use role-tagged key at `/bootstrap/key`. Use
separate ephemeral hosts and a load-balancer-role client; never mount OAuth
credentials or an existing Funnel state directory. `POD_NAME` identifies the
fixture. `QA_BACKEND=1` instead runs an HTTP echo/stream backend on port 8080,
without Tailscale credentials; `/ready` is suitable for its readiness probe.
That backend gracefully shuts down in-flight HTTP requests on SIGTERM.

The diagnostic fixture API binds only Pod loopback `127.0.0.1:9092`. Access it with
authorized `kubectl port-forward`, not a public or cluster Service:

| Endpoint | Operation |
|---|---|
| GET `/status` | Fixture device ID and tailnet addresses |
| POST `/configure` | Set a disposable Service and verified actual Pod endpoints |
| GET `/map?ip=TAILVIP` | Candidate hosts and selected dial plan |
| GET `/probe?target=TAILVIP:8080&path=/id` | HTTP through this running client's selected transport |
| GET `/probe?target=TAILVIP:8080&path=/stream` | Streaming through the raw forwarding path |
| GET `/drain` | Quiesce the host listener, then clean up after streams finish |
| GET `/close` | Remove this fixture's selected listener |

Example configure request (replace all fixture values from actual Kubernetes
Pod/Node resources; use only ready, nonterminating endpoints):

```json
{
  "Service": "svc:disposable-handoff-test",
  "Port": 8080,
  "NodeUID": "actual-local-node-uid",
  "NodeName": "test-node-1",
  "Endpoints": [
    {
      "address": "10.244.1.10:8080",
      "pod_uid": "actual-backend-pod-uid",
      "node_name": "test-node-1",
      "node_uid": "actual-local-node-uid"
    }
  ]
}
```

Measure client propagation before removing any host. Test streaming withdrawal
separately. For moves, pre-create a ready replacement on another node, begin an
old-backend stream, delete only the disposable old Pod, update endpoint snapshots,
and check concurrent requests and the final `DONE` marker. Compare device IDs and
Service VIPs before/after. Remove exact fixture Services and the isolated namespace
afterward. See the dated verification document for observations and boundaries.
