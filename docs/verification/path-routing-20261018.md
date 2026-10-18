# Shared path routing acceptance — 2026-10-18

## Development rollout

Built locally; no builds ran on xvps or 5090. Development image version:
`dev-path-prefix-20261018-2`.

Updated existing components in place:

- PostgreSQL-backed control in DLKN Kubernetes.
- Native public load balancer on xvps.
- Native Docker gateways on Tiny and 5090.
- Kubernetes gateway agents and forwarders in DLKN, 5090, m090dev, and m090prd.

Preserved native identity/signing/encryption hashes and 19 selected Kubernetes
identity/TLS Secrets. No reset, transfer, ACL mutation, or DNS overwrite occurred.

## Real public data paths

Disposable Docker containers on a dedicated network and ready node-local
Kubernetes echo Pods exposed two rules on each test hostname:

| Public prefix | Internal prefix | Behavior |
|---|---|---|
| `/api` | `/` | Strip prefix |
| `/api/admin` | `/internal` | Longer prefix selects admin route |

Both paths passed 18 HTTPS checks: root/trailing slash, queries, escaped suffixes,
correct backend and original Host, POST body/method, spoofed forwarding-header
removal, path-segment boundary rejection, streaming, and WebSocket echo. Public
certificates were issued and verified normally. Gateway and HTTPRoute reported
`Programmed=True` after backend approval and load-balancer acknowledgment.

Removed the admin route from the Docker labels and HTTPRoute independently.
After configuration convergence, requests selected the surviving `/api` route;
its DNS record and TLS remained usable. Restored the admin fixtures for release
artifact verification.

Five rounds of 11 existing-service probes passed (55/55): Harbor UI/health,
registry authentication and HTTP redirect, Voice, Metube, admissions, SSO, and
admissions API. Expected application 404 responses were checked as such.

## Issues caught and corrected

- Control requests stalled while the PostgreSQL primary was changing. Database
  connectivity subsequently recovered and existing public routing was reverified.
- Native installation under umask 0077 made the new binary directory inaccessible
  to its runtime account. Fixed explicit directory mode and tested installation.
- Existing Caddy configuration had unlimited shutdown grace. Added a five-second
  bound and adaptation regression coverage.
- Storage errors could appear as node revocation. They now return unavailable;
  identity rejection remains fail-closed and has regression coverage.

Acceptance does not promise uninterrupted active connections during component
replacement. It verifies recovery and retained identities, not connection migration.

## Stable release and final deployment

- Release: [v3.3.0](https://github.com/tailscale-x/tailscale-private-funnel/releases/tag/v3.3.0).
- Source commit: `7ef465e`.
- [Release CI](https://github.com/tailscale-x/tailscale-private-funnel/actions/runs/39128487225): all jobs passed, including PostgreSQL 16/18, both Go suites, vet, generation, operator checks, native binaries, image checks, publication, and chart verification.
- Published image index: `sha256:752374865b2d83e4ab9cd7590d63ae7d078599951d9c48ec2c47888bb4db8288` (`linux/amd64`, `linux/arm64`).
- Downloaded the Linux amd64 binary from the GitHub release and checked it against release `SHA256SUMS`; native services were updated with that binary.

Updated control, xvps load balancer, Tiny/5090 Docker gateways, and DLKN/5090/
m090dev/m090prd Kubernetes gateway agents and forwarders to v3.3.0. All three
native services were running. Four gateway agents and ten forwarders reached
their expected ready counts. Control and the public load balancer kept their
identities; 19 selected Kubernetes identity/TLS Secrets and the three native
identity/signing/encryption fingerprints matched their pre-rollout values.
All 48 existing Caddy certificate/key files were unchanged.

The released artifacts passed the same 18 public prefix tests. The disposable
Gateway and HTTPRoute reached `Programmed=True`. Final ordinary public DNS/TLS
probes executed from 5090 passed 55/55 after convergence. PostgreSQL comparisons
confirmed exact preservation of the original nodes, encrypted credentials,
providers, managed zones, policies, and DNS ledger entries/owners.

During rolling replacement, single-host Services briefly lost publication;
Voice/Harbor and admissions had transient failed probes before convergence.
This verifies recovery and persistent routing configuration, not uninterrupted
service during replacement of the only eligible backend host.

A separate local DNS issue produced intermittent curl resolution failures:
the PC's DNS path returned NXDOMAIN for Voice AAAA while retaining its A answer.
DNS-over-HTTPS returned NOERROR/no AAAA and the correct A address; independent
5090 probes succeeded. The host's DNS configuration was not changed. Local
IPv4-only probes were also run to isolate that resolver behavior.

Removed the two Docker echo containers, their network/relays, and the disposable
Kubernetes namespace. Control removed both QA DNS records and disapproved all
five associated Service hosts. The final DNS ledger equaled the original
ledger exactly. Application audit/history entries may remain in inventory;
no QA backend, published record, or approved Service advertisement remains.

Updated installer defaults to v3.3.0 through the management API. Removed only
`node:n2hHrEVCJj11CNTRL` from the administrator role through confirmed self-removal;
`autogroup:owner` remains, and this PC's subsequent management request was denied.
No control transfer, identity reset, ACL change, or direct provider mutation was
performed.
