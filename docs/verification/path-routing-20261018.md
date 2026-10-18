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

- A PostgreSQL primary change temporarily stalled control requests. Database
  connectivity recovered and existing public routing was reverified.
- Native installation under umask 0077 made the new binary directory inaccessible
  to its runtime account. Fixed explicit directory mode and tested installation.
- Existing Caddy configuration had unlimited shutdown grace. Added a five-second
  bound and adaptation regression coverage.
- Storage errors could appear as node revocation. They now return unavailable;
  identity rejection remains fail-closed and has regression coverage.

Acceptance does not promise uninterrupted active connections during component
replacement. It verifies recovery and retained identities, not connection migration.
