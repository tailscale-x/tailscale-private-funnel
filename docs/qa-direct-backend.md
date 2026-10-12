# Direct Docker backend forwarding · 10 October 2026

The ingress supervisor now dials the approved application's selected Docker-network IP and port directly. It checks reachability before advertising the Service. The `ingress relay serve` command and runtime relay-container creation have been removed. Existing relay containers on 5090 were removed without touching `llama-router` or `flue`.

The previous relay-path investigation is preserved in [qa-streaming-relay.md](qa-streaming-relay.md). It established that the application's Docker-network address was reachable from the host and that the Docker loopback published-port path was the streaming failure boundary. Direct application forwarding removes both the published-port path and the extra relay hop.

Local gates: `go test ./...`, `go test -tags caddy ./...`, `go vet ./...`, and `git diff --check` passed. The control database migration renames relay metadata to backend assignments in a transaction and preserves its rows.

Live checks after the first direct-forwarding deployment: Llama `/health` and Flue `/api/ping` returned HTTP 200. Two public HTTP/1.1 and two public HTTP/2 bounded SSE requests returned HTTP 200, a clean curl exit, and `[DONE]`. No managed relay containers remained. The final artifact and post-restart checks are recorded below.

Final live artifacts:

- 5090 ingress: locally built native binary `dev-direct-backend-20261010-r3`, SHA-256 `5df2b54fe761c025ab6c26254eda2bf52f9955fd0abdc51e1a63b620147b1164`.
- xvps control: locally built development image `ghcr.io/tailscale-x/tailscale-private-funnel@sha256:dd501a623c7fd4d4a487d7dcb2db148ad83a44ed6e10cf157d067886bedbac63`. The control database contains `backend_assignments`, no `relay_assignments` table, four existing DNS ledger rows, and a fresh signed ingress report.

After the final ingress restart, Llama `/health` and Flue `/api/ping` returned HTTP 200. Two more HTTP/1.1 and two HTTP/2 bounded public SSE requests all completed HTTP 200 with `[DONE]`. The old managed relay containers remained absent. The ingress systemd unit and control container stayed active. Only prebuilt artifacts were installed on the remote hosts. No stable tag or GitHub release was created.
