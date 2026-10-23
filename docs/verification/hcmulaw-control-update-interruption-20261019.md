# HCMULAW interruption during control verification — 2026-10-19

## Accountability and current result

Live control-update verification caused further public-routing interruption. It was stopped when the user reported the outage. No API-stall fault was enabled. Replacing the disposable control Tailscale Service host nevertheless reproduced a connectivity gap for existing runtime clients. This was an unsafe live acceptance method; further fault and restart acceptance belongs in a disposable installation.

At 17:44 UTC, all four existing university HTTPRoutes were Accepted, ResolvedRefs, and Programmed at generation 2. Public HTTPS using Cloudflare recursive DNS returned API /healthz 200, public site 200, SSO /login 200, CRM 302; Metube, Harbor health, and Voice returned 200 with valid certificates. Zalo phone preflight returned 204 with the expected https://h5.zdn.vn origin. No actual phone-token request was submitted.

The API also returned 200 using ordinary DNS from xvps. The development PC still had a negative API lookup through its LAN DNS path after public records recovered. Public Cloudflare and Google resolvers must be distinguished from that cache; the zone SOA advertises a 1,800-second negative TTL. Recovery is not a claim that every recursive cache was immediately repaired.

## Failure mechanisms

1. Existing runtime clients temporarily could not dial the replacement control Service host, including network-unreachable errors for the IPv6 TailVIP. An Owner host could reach control. The underlying Service/map propagation problem remains unresolved; an idempotent Service approval refresh and SDK experiments did not establish a reliable fix.
2. Five-minute health/report expiry was used as destructive DNS desired state. Earlier verification removed owned records and induced negative caching. Re-created records keep exposure IDs and owner strings, but Cloudflare remote record IDs change.
3. Configuration readiness repeatedly read the same PostgreSQL metadata. With injected 5 ms SQL latency in a disposable local fixture, the old request took 23.57 seconds versus 1.12 seconds with request-scoped metadata memoization. Canceled or malformed reads must fail, never publish an empty configuration.
4. The deployed retention guard preserves already-owned DNS during missing/expired reports and missing load-balancer health, without treating stale backends as ready. It does not solve every fresh-negative backend report: current fresh-negative semantics can still withdraw DNS. This remaining limitation must not be represented as a complete outage-prevention guarantee.

## Restoration and deployed artifact

Control now runs the locally built clean development image:

`ghcr.io/tailscale-x/tailscale-private-funnel@sha256:5be28e4718746f3f277db4d8f2ce8e343c7e6a201a6fa07ee5efc52600be00ff`

Version: `dev-config-20261019-r4`. GitOps pin commit: `a8382805ad9759ebc27d38b0639dc51ccdeca425`.

It includes bounded configuration reads, malformed-read error propagation, owned-DNS retention on report uncertainty, and logout on closure of application-owned ephemeral identities only. Persistent native and borrowed host identities are not logged out.

Affected Kubernetes gateway agents/forwarders were restarted to reconnect to the current control Service. Their logical component/application identities remain; disposable Pod devices change by design. The native public load balancer was restarted with its existing persistent state. No gateway binary upgrade, database reset, control transfer, URL change, ACL change, or Cloudflare Access change was performed. No temporary administrator grant was added.

The QA proxy namespace and its temporary control egress policy were deleted after the clean control image was ready. No proxy environment remains in that image. No stable tag or release was created.

## Preservation

Read-only checks confirmed:

- Control material Secret data unchanged.
- Database identity binding unchanged.
- All pre-existing exposure IDs unchanged.
- All 12 existing encrypted settings secrets decrypt to the same values.
- The nine currently published ledger records retain their original exposure/load-balancer owner strings.

The two former LuongGia Docker backends were separately observed stopped during the incident; their old reports no longer advertised the hostnames. They were not restarted or reconfigured as part of this task. Do not claim those removed rows remained unchanged or that those applications passed acceptance.

## Local validation and remaining gate

The isolated fix worktree passed `go test ./...`, `go test -tags caddy ./...`, `go vet ./...`, PostgreSQL 16/18 storage and focused configuration/DNS contracts, and image startup/version checks. These results do not cover the primary working tree's separate unfinished tunnel feature.

The clean image remains a development artifact. Full Tailscale Service reconnection reliability and fresh-negative DNS semantics require isolated reproduction and regression coverage before another live acceptance rollout or a claim that the root cause is completely fixed.

Private detailed logs and redacted-preservation comparison inputs are retained under `/tmp/funnel-rootfix-rollout-20261019/`; they are not release assets.
