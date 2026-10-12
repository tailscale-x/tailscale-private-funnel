# Tailnet exposure and dropdown QA — 2026-10-09

## Implemented and checked

- Docker/tailnet/both sources, with saved source settings loaded before defaults.
  Tailnet-only preflight and generated instructions require no Docker.
- Caller-bound signed peer snapshots, shared nonce replay rejection, stale peer
  rejection, externally assigned effective tags, and administrator-only probes.
- Stable device selection, fresh connection tests, policy/zone validation,
  exposure creation/editing, stale-edit rejection, disable and delete.
- A single ingress identity and combined Docker/tailnet Service/report state.
- Native selects have visible browser arrows across the UI. Known ingress and
  load-balancer choices use selects in enrollment and policy forms; policy rules
  support multiple selections without changing their array serialization.
- Policy editing retains selected values, including references to inactive nodes.
- Caddy removes untrusted forwarding metadata and preserves the public Host.
  Actual Caddy handler tests cover IPv4 and IPv6 client addresses.
- Raw forwarding half-close test transfers a streamed response after request EOF.
- Deletion keeps the disabled owned application when Service cleanup fails;
  retries remove its Service definition before deleting application state.
  Idempotent cleanup requires the exact deletion audit record. Names/tags alone
  never authorize deleting an unrelated Service.

## Live verification

Locally built development artifacts were deployed to the existing control and
load balancer on the VPS and native ingress on 5090. No build ran on either host.

A disposable HTTP echo backend on this development machine was selected from the
5090 ingress's signed peer snapshot. Control created its application, approved
Service host, and owned public CNAME. Public HTTPS, HTTP 308 redirect, backend
failure (502), chunked streaming, and WebSocket upgrade/echo passed. Forged
Forwarded, X-Forwarded-* and X-Real-IP values were removed; the backend received
its public Host, actual IPv4 client address, HTTPS scheme, and listener port 443.

The same signed report contained the existing Docker-backed Llama exposure and
the disposable tailnet exposure. Llama's public health check remained successful.
External ingress tags and its persistent device identity survived native restarts.

Disable/delete ran through control. The disposable CNAME and route were withdrawn;
its Service advertisement was removed. Its Service definition was also deleted through the audited control delete
operation, and absence was verified through control Service inventory. The original managed zone,
encrypted provider credentials, encrypted settings and exact two DNS ledger
entries matched the pre-rollout snapshot.

## Browser and local gates

Chrome verified native dropdown arrows, policy edit selection, connection-test
status, exposure save/edit, disable/delete confirmations, enrollment selects,
and no horizontal overflow at 1440px and 390px. No console warnings/errors.
Synthetic screenshots were captured locally; no private machine inventory is
included in repository screenshots.

Passed: both Go suites, go vet, git diff --check and reproducible UI CSS build.
The native installer also retrieved the matching executable from public GHCR
without requiring Docker and verified referenced manifest/config/layer digests.

## Coverage boundaries

Public HTTPS used IPv4. IPv6 forwarding-header behavior was verified locally,
not against an IPv6 public DNS record. Existing persistent nodes were updated;
this rollout did not provision a separate clean-host tailnet-only node. Existing
expiry/revocation and ownership tests ran, but no live five-minute outage was
introduced solely to expire the shared development ingress report.
The development image is linux/amd64; no stable tag or GitHub release was created.

## Deployed development artifact

`dev-tailnet-backends-20261009-r7`

Image digest: `sha256:cde10ec0cd5932fd92e45b9f1fd55763b455172e0ba1f6ca8379a5b9409f0590`

Native SHA-256: `26b7729dbb9c1ee360a5a21c46e6b69b09a7e28fd250a89c2d1a9a4d0a32b45e`

The installed native executable on both hosts and the binary obtained without
Docker from the public image match this SHA-256.
