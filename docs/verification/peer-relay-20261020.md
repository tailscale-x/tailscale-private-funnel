# xvps peer-relay grant and transport verification

> Historical investigation checkpoint. Later builds and results are indexed in
> [the Service-route review](service-route-consolidation-20261020.md). Statements
> about running versions and availability below apply to this checkpoint only.

## Authorized change

On 20 October 2026, the owner authorized adding a relay grant and verifying existing routing, explicitly without restarting components or replacing identities.

The live tailnet policy was fetched with its ETag, parsed using HuJSON, and patched with exactly one capability-only grant:

```json
{
  "src": [
    "tag:tailscale-private-funnel-main-control",
    "tag:tailscale-private-funnel-main-loadbalancer",
    "tag:tailscale-private-funnel-main-docker-gateway",
    "tag:tailscale-private-funnel-main-tailnet-gateway",
    "tag:tailscale-private-funnel-main-k8s-gateway"
  ],
  "dst": ["100.100.63.102"],
  "app": {"tailscale.com/cap/relay": []}
}
```

The destination is the existing xvps **host daemon**, not the embedded edge. The daemon already advertises its relay on public UDP port 41642. No extra overlay network permission to the relay is needed for this capability, per [Tailscale peer-relay documentation](https://tailscale.com/docs/features/peer-relay#security-and-access-control).

Tailscale validation returned 200. The policy update used If-Match and returned 200. Immediate policy readback was compared semantically against the original: only the appended grant changed. Existing tag ownership, application connectivity, Service approvals, human roles and other relay grants were preserved.

## Actual path observations

The running edge remains PID 166842, device `n9YQSZEmrA21CNTRL`, with its unchanged saved configuration. Its Tailscale UDP socket is port 33940.

A metadata-only packet inspection of that exact socket correlated public discovery keys with the host daemon's read-only peer map. While exercising API, Harbor and Metube health endpoints, the edge exchanged direct WireGuard data packets with:

| Peer | Data packets observed, both directions |
| --- | ---: |
| University forwarder on node 1 | 63 |
| Harbor forwarder on node 2 | 24 |
| Control | 38 |
| Tiny Docker gateway | 40 |

These are direct UDP observations, not a conclusion inferred from the host daemon's own connection status. No packets for that edge socket used relay UDP port 41642 in the 25-second sample. Other established transports may coexist; the sample is not an exhaustive assertion about every connection or future path.

After the grant, the relay server also exposed new Funnel sessions, mapping to control and multiple Kubernetes/native gateways. Session counters increased, confirming allocation and forwarded traffic. **These counters alone do not prove that application HTTP/RPC traffic selected the relay:** relayed discovery probes can also increment them. A separate Geneve protocol inspection distinguishes relay discovery from WireGuard data; its result is recorded in the checkpoint below.

No direct path was disabled and no connection was reset to force relay selection. Direct connectivity remains preferable when available.

## Public acceptance and preservation

- Eighteen rounds across seven existing public routes: **126/126 expected HTTP responses with successful TLS verification**.
- University API `/healthz`, admissions, SSO, Harbor health, Metube and Voice returned 200; Eoffice returned its expected 302.
- Another twelve API/Harbor/Metube requests during the edge path capture returned 200.
- University API preflight OPTIONS returned 204 with `Access-Control-Allow-Origin: https://h5.zdn.vn`.
- All four university HTTPRoutes reported Programmed=True, observed generation 2.
- All five DLKN Funnel Pod UIDs/restart counts remained unchanged between checkpoints.
- The eleven DNS ledger rows compared exactly equal to the preceding read-only snapshot.
- No binary, workload, identity, auth key, approval, Service definition, DNS record or application setting was changed. No packet payloads or raw private netmaps were saved; captures retained only metadata/counters and peer labels.

The university API had recovered **before** this grant. These checks verify current routing and additional relay availability, but do not prove that the grant repairs the earlier intermittent missing-Service-route defect. Its in-process route diagnosis and backend-specific readiness work remain separate.

Private operational artifacts: `/tmp/funnel-peer-relay-20261020/`. Policy snapshots are mode 0600 in a private directory. There is no commit, release, publication, or deployment in this task beyond the explicitly authorized ACL update.

## Relay protocol checkpoint

A 20-second metadata-only Geneve inspection of the identified Funnel relay sessions observed 577 relayed discovery packets and 457 WireGuard data packets. This confirms encrypted data, not only relay discovery, crossing xvps between control and multiple Funnel gateways. Counts are packet observations and can include both receive and transmit copies at the relay. The observed edge-to-backend data path remains direct UDP.
