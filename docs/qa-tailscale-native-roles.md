# Tailscale-native roles: local verification

Date: 2026-10-19. Initial local checks are recorded below; subsequent authorized
live deployment checks are recorded in the final section.

## Implementation boundary

The runtime contract now uses verified WhoIs identity and installation/role tags.
Enrollment, component signatures, manual component approval, and Kubernetes identity
restoration are removed. Role authority is deliberately shared among devices with
the same role tag; component IDs and assignment leases are coordination, not secrets.
Human management authorization remains independent.

Docker discovery selects and probes container addresses directly. It never creates
relay containers or publishes host ports. The historical streaming failure involved
Docker's published-port forwarding path, not binding an application to `0.0.0.0`.

Native embedded identities persist. Host mode borrows the existing daemon without
joining or changing its identity. Kubernetes uses temporary per-Pod identities;
control encryption/database and load-balancer certificates remain durable.

## Automated gates

- Both Go suites, with Caddy enabled separately; `go vet`; `git diff --check`.
- Real envtest API-server validation and installer boundaries, including issuer
  references, absence of join credentials in Kubernetes installers, and unchanged
  externally managed issuer Secrets.
- Shared SQLite and PostgreSQL 16/18 contracts: credentials, logical component
  replacement, leases, replay/freshness, policies, reports, ownership, and transactions.
- PostgreSQL encryption-material persistence across simulated workload replacement.
- Role authorization tests: wrong role, cross-installation, shared-role authority,
  disabled/unknown instances, lease conflicts, and runtime-to-human separation.
- Host LocalAPI fixtures: preserved unrelated Serve entries, optimistic ETags,
  ownership conflicts, persistent borrowed-device binding, multi-port withdrawal,
  fail-closed rejection of unsupported host-reused control, and mandatory
  PROXY v2 source metadata for embedded control.
- Docker fixture rejects every mutation. An additional real-Docker fixture creates
  only its own uniquely named backend/network, verifies direct container-IP SSE
  completion and `[DONE]`, replaces the container, repeats the stream, and confirms
  no published ports. Fixtures are removed afterwards.
- Caddy HTTPS fixtures verify HTTP/1.1 and HTTP/2 streaming completion across reload;
  existing prefix rewrite, trusted headers, WebSocket and raw TCP tests remain.
- Protocol, operator and UI generation reproduced byte-for-byte; Buf lint and Helm
  checks. The local nonroot image starts and reports the development version.

Reproduce the opt-in direct-container test with an existing Docker daemon and the
fixture image `node:24.18.0-alpine`:

```sh
FUNNEL_DOCKER_INTEGRATION=1 go test ./internal/gateway \
  -run '^TestDirectDockerStreamingAndReplacement$' -count=1 -v
```

## Disposable Kubernetes checks

A separate two-node k3d cluster runs the development operator and rendered gateway,
forwarder and load-balancer workloads. No real OAuth credentials are provided.
Verified Pod-bound bootstrap reaches the issuer and fails closed with
`failed_precondition: key issuer unavailable` when its referenced Secret is absent.
An uninitialized component remains unready and does not join Tailscale.

Authorization checks confirm:

- Forwarders cannot read Kubernetes Secrets or Pods.
- The gateway/broker cannot read Secrets or patch Pods.
- A load balancer can read its exact public Service, but not an unrelated Service.
- Control has no default Kubernetes API token and cannot read Secrets. Its durable
  material Secret contains encryption/storage material, not a Tailscale identity.
- The gateway has broker TLS material, not forwarder/network identity Secrets.
  SQLite and Caddy use separate application PVCs. All workload environment names
  are unique; a duplicate discovered during k3d review was fixed and regression-tested.

This exercises deployment, TLS bootstrap, token validation, rendering, and RBAC;
it does **not** prove real tailnet forwarding or disposable device recovery.
The temporary cluster and database containers are removed after verification.

## Browser review

Synthetic local control data only. Chrome DevTools MCP was used because a Browser
plugin was unavailable. Settings and native-host/Kubernetes gateway setup were
checked at 1440px and 390px. Policy JSON is collapsed and copyable; issuer selection
offers one installation issuer, without a per-role credential selector; host
instructions contain no join key and show the exact role;
Kubernetes instructions reference an issuer Secret. No horizontal overflow or
console errors were observed. Viewer setup shows no mutation form. Commands are
backend-rendered and generating UI instructions creates no credentials.

## Live acceptance outstanding after the initial local phase

Local fixtures cannot establish these live claims:

- Service-tag policy acceptance and automatic host approval on a real tailnet.
- Host-reused control source preservation through an actual tailscaled daemon,
  rather than the protected-socket/PROXY fixture.
- Disposable Pod replacement joining with newly minted real role-scoped keys,
  stable control Service discovery, and routing/DNS retention during replacement.
- Real IPv4/IPv6 Service routing, tag/device removal, and public ACME issuance.

Do not label the new architecture production-accepted from unit or fail-closed
k3d checks. At that initial phase, no existing deployment, DNS record, ACL, device, credential,
or identity had been modified and no tag or artifact had been published.

## Code surface

See [before](metrics/tailscale-native-roles-before.json) and
[after](metrics/tailscale-native-roles-after.json). Measured from baseline
`e4989555feb22017860d6089c05f38d2148e7bd9` with `scripts/code-surface.py`.
Authored runtime/interface/tooling lines decrease; tests, generated output, and
documentation are reported separately. This measures source size, not correctness.

Authored source: 28,545 → 26,797 nonblank lines (1,748 fewer; 6.1%).

## Single installation issuer — 2026-10-19

The local implementation now uses one encrypted issuer credential pair for native
control bootstrap and installer issuance. Kubernetes components reference the same
externally managed installation issuer Secret. Workload role selection remains
server-side; there is no issuer-role selector in settings or the OAuth API.

Live Tailscale API verification used a newly created client restricted to
`auth_keys` and `tag:tailscale-private-funnel-main-issuer`. Its access token reported
only `auth_keys`. It successfully minted one unused, preauthorized, ephemeral,
one-use, 900-second probe key for each of the five installation device roles.
All five probe keys were revoked immediately; no probe device joined.
Issuance for another installation was rejected (400). Policy, Service, device,
and user reads returned 404 with the restricted token; the setup credential
could read the policy and client metadata. The new client metadata confirmed
exactly the requested scope and issuer tag.

Policy comparison confirmed that only the issuer tag and its direct ownership of
the five device roles changed. Existing grants, Service auto-approvers, and other
policy sections were preserved. A temporary setup-tag ownership link was removed
immediately after client creation. The existing setup OAuth client was not rotated,
revoked, or modified. Credential values and private policy snapshots are excluded
from this repository. The new pair is staged in protected local files for a later
control deployment; running deployments have not been changed.

Single-issuer verification gates passed:

- `go test -p 2 ./...` and `go test -p 2 -tags caddy ./...`.
- `go vet -p 2 ./...` and `buf lint`.
- Protocol, operator/CRD/reference, and UI asset regeneration produced identical
  files on a second run; `git diff --check` passed.
- Browser checks of synthetic Tailscale settings at 1440px and 390px confirmed
  one Installation key issuer option, no role selector, blank credential fields,
  and no horizontal overflow. The generated policy returned eight tags and
  direct issuer ownership of exactly five device roles.

These checks validate development code and issuer permissions. They do not claim
that running control or workloads have been upgraded to this identity model.

## Authorized live development verification — 2026-10-19

After explicit deployment authorization, locally built development artifacts were
installed on control, native Docker gateways and the public load balancer, and four
Kubernetes installations. GitOps references were updated and reconciliation resumed.
No stable release was published during these checks.

- The new installation issuer minted real one-use keys using only `auth_keys` and
  the issuer tag. Service management independently required `services` plus
  `devices:core:read`, with its client tagged as the control role owning both Service
  tags. The issuer has neither Service nor policy permissions.
- Control's stable Service automatically approved its current tagged host. Real
  requests over its IPv6 TailVIP preserved the peer address for WhoIs. Runtime
  devices were denied human-management endpoints (403).
- A replaced Kubernetes forwarder received a new ephemeral device and process ID
  while retaining its logical forwarder ID. Existing application/exposure IDs and
  ledger ownership were preserved. All 23 baseline exposure IDs remained unchanged.
- A disposable real Docker backend used an actual container IP, with no published
  ports or relay container. Public HTTP/1.1 and HTTP/2 SSE each delivered 1,050,018
  bytes, 128 data frames and the final `[DONE]`. WebSocket echo passed. Echo checks
  preserved public Host, method, body and query while removing spoofed forwarding
  headers and generating trusted client-IP/HTTPS metadata.
- A separate kernel-networked tailscaled fixture forwarded a Tailscale Service
  directly to that Docker IP. A client on another host received the complete SSE
  stream. Host LocalAPI outbound Service dialing worked; management stayed denied.
  Cleanup preserved the unrelated Serve entry and removed only the fixture Service.
- Admissions, Eoffice, SSO, API, Voice, Harbor and Metube returned their expected
  statuses with valid public TLS. Disposable Pod replacement temporarily withdrew
  Voice while its sole forwarder recovered; authoritative DNS and HTTPS recovered.
  Recursive negative caching can outlive an allowed deployment interruption.

The live checks found and fixed expired-instance renewal after control outages,
missing forwarder hostnames during registration, PostgreSQL startup touching an
unmounted persistence path, and loss of peer identity through Service forwarding.
The native load balancer also required reauthentication because its previous
Tailscale device key had expired; this was distinct from IPv4/IPv6 selection.

### Host-reused control: acceptance failed, now rejected safely

Tailscale 1.102.2's daemon explicitly rejects PROXY protocol for Unix backends.
Both userspace and isolated kernel-daemon checks failed that proposed path. The
previous fixture-only test did not establish upstream support. Host-reused control
is now rejected before touching daemon configuration; its UI option is disabled.
Embedded control remains supported. No unprotected TCP or HTTP-header authentication
fallback was introduced. Release scope must acknowledge this limitation.

All temporary test keys and the isolated host-forwarding Service were revoked or
removed by fixture cleanup. Credential values and private operational receipts are
excluded from this repository.

### Final installer UI checks

The user selected embedded control for the release. Chrome DevTools checked the
synthetic control and Docker gateway setup flows at 1440×1024 and 390×844. Control's
host option is disabled; its protected-file installer command was generated by the
backend. Docker gateway instructions generated both embedded and host variants;
the host variant had no join credential. Settings offered one installation issuer.
There were no application console warnings/errors or horizontal overflow. These
checks used a local fixture because the development machine's runtime identity is
correctly denied human-management access. Private screenshot receipts remain
outside the repository. The architecture SVG now describes device roles, Service
auto-approval, disposable Pods and direct Docker forwarding.
