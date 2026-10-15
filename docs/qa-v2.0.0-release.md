# v2.0.0 release review and acceptance

Date: 2026-10-15 (Asia/Saigon). Verification uses Pod replacement; no host reboot.

## Review scope and fixes

Reviewed the complete uncommitted gateway/Kubernetes refactor, including CLI/API
removals, storage migrations, identity bootstrap, broker authentication and leases,
node-local endpoints, Caddy generation/transport, DNS ownership, UI provisioning,
chart permissions/storage, generated assets, documentation, and release packaging.

Fixed during review:

- Alternative HTTPRoute matches now sort independently, with lexicographic Gateway
  API precedence instead of additive scores. Same-source ties retain route age/name
  order; ambiguous cross-source claims remain rejected.
- Status writes preserve foreign-controller entries, including entries referencing the
  same parent. Parent condition lookup and listener counts include namespace/section.
- Backend request timeout wrappers preserve duplex upgraded response bodies, verified
  by a real HTTP upgrade test and public WebSocket echo with the timeout enabled.
- The public edge retains its 404 catch-all even while named routes are configured;
  regression tests reproduce and prevent empty 200 responses for unconfigured hosts.
- Controller restore rejects a Secret missing the Tailscale machine key before writing
  runtime files. Direct Kubernetes identity clients have bounded request timeouts.
- Regenerated drifted embedded CSS and templ output; a second build is reproducible.
- Corrected outdated documentation about direct tailnet routing, Docker relays,
  controller Secret permissions, storage, namespace placement, and removed selectors.
- Release metadata includes the immutable image digest; CI verifies both Linux image
  architectures and publishes the matching OCI chart.

Existing Tailwind 3 development dependencies have upstream glob/parser advisories;
Node.js is build-time only and these tools process the trusted repository source tree.
This review does not claim a dependency-vulnerability-free build or Gateway API conformance.

## Local gates

Passed both Go suites, `go vet ./...`, targeted broker/gateway/Kubernetes race tests,
Helm lint/render/package contracts, container startup, generated CSS/templ checks,
relative documentation links, and diff checks. Chrome MCP checked all 14 control and
provisioning screens at 1440px and 390px, with no horizontal overflow; gateway selection
navigated to the correct form and inspected console logs contained no app errors.
New tests cover the fixes above.
Native Linux amd64/arm64 and macOS arm64 artifacts are also built by the tag workflow.
No builds run on xvps or 5090.

## Candidate image and live placement

The locally built `v2.0.0` executable was packaged and tested as development image
`ghcr.io/tailscale-x/tailscale-private-funnel:dev-v2-release-check-20261015`, digest
`sha256:6d7df8655bc5b8782dab5c6a9b1f060f8f2483d5a2e2f2e560c8f7dd3d191d4d`.
The load-balancer catch-all fix was then rebuilt and retested as
`dev-v2-release-check-20261015-r2`, digest
`sha256:de09ba781ca6dbb177c4e668efcb9117723f1b2243c96a7fb80a60e33d7850d9`.

Updated control and the local gateway on the development PC, the public load balancer
on xvps, and the 5090 controller/forwarder installation. All use the reviewed chart
with Recreate Deployments and the existing identity Secrets. Removed the local watch
assignment for the previously deleted `omni-development` namespace; no unrelated
namespace was recreated. Forwarder replacement waited for existing identity leases.

Compared machine keys, application/report signing keys, database encryption keys,
storage markers and controller enrollment/cluster IDs before and after replacement.
The existing values were preserved. Control kept its private address and database.

## Public acceptance

A disposable Kubernetes HTTPRoute in the verification namespace used one backend
Service, alternative path matches, a response-header modifier, and a 30-second backend
request timeout. Control provisioned its Tailscale Service and owned public DNS.

Passed:

- First-visit public certificate issuance with normal certificate validation.
- HTTPS proxying and HTTP 308 redirect preserving the request path.
- Actual public client IP in X-Real-IP/X-Forwarded-For, original public Host, trusted
  protocol/port/host metadata, and removal of malicious incoming forwarding headers.
- `/api` selects its more specific rule rather than an earlier catch-all alternative.
- Flushed streaming and WebSocket echo with the backend timeout enabled.
- Existing Voice public WSS TTS completed with 48 audio packets.
- Existing Flue HTTPS ping and Metube HTTPS routing remained operational.

The tag pipeline rebuilds clean, versioned native artifacts and the multi-architecture
image from the committed source. Published artifact digests/checksums are authoritative
in `release-metadata.json` and `SHA256SUMS`; the development digest above is not the final
release index digest. Post-publication acceptance is recorded in the release's
`release-verification.json` asset, after pulling the published image by digest.

## Recovery boundary

Recreate is singleton recovery, not zero downtime. Forwarder leases can delay identity
handover for up to their fencing interval. Protected backups, cluster Secrets encryption,
and the dedicated xvps client-IP/firewall setup remain as documented in
[the migration report](qa-secret-deployments.md). No ACL changes or direct DNS-provider
mutations were used. Disposable DNS cleanup is verified through control reconciliation.
