# Exposure load-balancer mapping verification

Verified 10 October 2026 on prebuilt development artifacts. No stable tag or GitHub release was created.

## Behavior

- Ingress enrollment and generated CLI commands have no load-balancer assignment.
- Each public hostname has an immutable exposure ID. Null `loadbalancer_ids` means all eligible edges; a nonempty array overrides that hostname only.
- Control evaluates policy, node health, backend connectivity and public endpoint readiness per edge. Direct tailnet backends remain device-ID bound and use each edge's own tsnet dialer.
- Public exposures use deduplicated DNS-only A records. Docker applications retain their Services and ingress identities.
- Exposure DNS uses `exposure:<id>` ledger ownership; load-balancer host A records keep their node owner. No TXT marker or ACL mutation was added.

## Local gates

Passed `go test ./...`, `go test -tags caddy ./...`, `go vet ./...`, and `git diff --check`. The UI asset rebuild matched the existing CSS checksum.

New coverage includes multiple edges, new-edge inclusion, per-host overrides, policy restrictions, duplicate IPs, stale health/reports, endpoint ambiguity and prior-IP retention, administrator authorization, removed input fields, signed node-bound direct configuration, CNAME-to-A conversion, unrelated record protection, mutation failure compensation, transactional migration rollback, atomic application updates, and deleting/recreating an exposure after cleanup.

A disposable private copy of live control data passed migration: six exposures, six ownership partitions and one provider. Encrypted provider credentials remained readable. The load balancer's own A-record ownership was unchanged. The copy and its encryption key were removed afterward.

## Browser verification

Chrome at 1440px and 390px verified ingress enrollment, exposure selection controls, empty-selection validation, saving overrides, refreshed edge diagnostics, direct-tailnet editing and machine selection. No horizontal overflow or console errors were observed.

The live Llama alias was temporarily restricted to the existing edge using the UI. Its primary hostname remained on the default mapping. The alias was then restored to all edges; all live mappings are at the default.

## Deployment

Control uses the immutable development image:

```text
ghcr.io/tailscale-x/tailscale-private-funnel@sha256:531cb702e4052b1a561bb6b3d712c6d925356c7dd3aca681086ddffcebdb1f1a
```

Native load-balancer and ingress processes use `dev-fanout-20261010-r15`. The image and binary were built locally. Binaries extracted on xvps and 5090 matched the local SHA-256:

```text
229890ee9166be0f59aaa22851c479fc3861a11c819234aa2bdca30db59f6c10
```

Control, the xvps load balancer, and ingresses on 5090 and tiny were updated. Persistent identity directories, certificate storage and application containers were retained. Temporary staged binaries were removed.

Public HTTPS checks returned HTTP 200 with successful certificate verification for Llama, its alias, Flue `/api/ping`, MeTube and Voice. Their public A records resolve to the deployed edge. The inactive historical Flue hostname has no eligible backend and was not republished.

Multiple-edge routing was verified in local contract tests; the live installation has one load balancer. Multiple DNS addresses provide distribution rather than guaranteed client failover.
