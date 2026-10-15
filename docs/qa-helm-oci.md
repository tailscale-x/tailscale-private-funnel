# Helm OCI packaging verification

Initial local validation on 2026-10-15 was performed without publication. The subsequent development publication and live verification are recorded below. No GitHub tag or GitHub release was created.

- Tag CI packages `vX.Y.Z` as chart `X.Y.Z`, app version `vX.Y.Z`, and matching default image.
- Publishes to `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`, verifies a pulled archive byte-for-byte, and attaches the archive to the release with chart metadata and combined checksums.
- UI-generated Helm instructions use an explicit OCI chart version and check availability before cluster/Secret mutation; no checkout path is used.
- Development tags map to `0.0.0-<dev-tag>`; they require a separately published matching chart. No development chart was published during the initial local-only validation.
- Verified stable/prerelease and development package versions, matching image defaults, Helm lint/template, and a push/pull/template round trip through a disposable loopback-only OCI registry. The registry was removed afterward.
- Generated-command regression tests cover OCI reference, version, image, and preflight ordering. Both Go suites and vet pass.
- Actionlint, shell syntax, asset reproducibility, release metadata/checksum execution, and `git diff --check` pass.
- First publication must make the new GHCR chart package public for anonymous installation and retain repository Actions access. That access was subsequently verified in the publication below.

## Published development end-to-end verification — 2026-10-15 (Asia/Saigon)

The development chart is now published, public, and associated with `tailscale-x/tailscale-private-funnel`:

- Reference: `oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel`
- Verified version: `0.0.0-dev-oci-helm-20261015-r2`
- OCI digest: `sha256:06098352ceb0e0d9a486a632050e14bcdcf083cde6ed15fe9003b3793d1e98ff`
- Archive SHA256: `207bda48ce4004edf26995d39b5962c9702ca7a5bd2675b407f1f18498413c24`
- Matching image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-oci-helm-20261015-r2`
- Image digest: `sha256:998589503d67cde5f0cdab0a23c7b674275775a18cb38cf3a5e7c125a8bc54ff`

Anonymous chart pull was verified with empty Helm and Docker credential configuration. The downloaded package matched the packaged archive byte-for-byte.

### Generated UI instructions

Chrome MCP used the deployed control UI's Add Kubernetes gateway form. The generated script was captured to a temporary 0600 file without printing bootstrap credentials and executed unchanged against a dedicated disposable k3d cluster. The final enrollment selected a custom namespace and Gateway/Ingress class. Helm installed directly from GHCR with no checkout path and no registry authentication. The UI reported a fresh signed report from the joined node.

Only the prebuilt control artifact was deployed to xvps; no remote compilation. Existing gateways and load balancer were not restarted. No ACL, OAuth scope, or unowned DNS overwrite changes were made.

### Runtime checks

- Gateway API v1.5.1 CRDs, k3s v1.31.5, bundled Traefik disabled.
- Bootstrap Secret removed after enrollment; Helm values contained only its name and selected deployment values.
- Pod replacement, OCI Helm upgrade, and rollback retained the Tailscale device and signing identity with no bootstrap Secret.
- UI-generated Gateway/HTTPRoute and Ingress examples were applied with disposable managed hostnames and an echo Service.
- Both public HTTPS paths returned 200; HTTP redirected with 308.
- HTTPRoute reached Programmed=True only after load-balancer acknowledgment.
- Verified trusted forwarding headers, spoofed-header removal, WebSocket echo, and a 1 MiB streamed response.
- HTTPRoute and Ingress reused one backend Tailscale Service.

### Fix found during verification

The former UNIQUE constraint on application Service names blocked two hostnames sharing one backend Service. A transactional startup migration now preserves all application columns/IDs, exposure IDs, credentials, and ledger ownership while removing that constraint. Store and signed-snapshot regression tests cover shared Services and migration preservation. Both Go suites, vet, asset reproducibility, and diff checks pass. The corrected development build is r2; r1 remains an earlier development artifact.

The shared host filesystem required a 2% kubelet disk-eviction threshold only in this disposable cluster. No production setting was changed.

### Cleanup and final checks

Deleting HTTPRoute withdrew only its owned hostname; the independent Ingress and shared Service stayed available. Removing the remaining routing resources disabled both applications, removed all four disposable authoritative DNS names, and deleted both test backend Services. The DNS ledger contains zero entries for these names.

Both test nodes were revoked through control with `pending=false`; their reports were removed. Helm releases, PVCs, namespaces, and the dedicated k3d cluster were removed. Temporary bootstrap scripts/credentials and remote binary transfer files were removed. Existing credential files and persistent application data were retained.

The controller service account cannot read Secrets. Its runtime container has no bootstrap Secret mount, hostPath, Docker socket, or TUN, and drops all capabilities. An additional OCI upgrade with imagePullPolicy=Always successfully pulled the public image anonymously from GHCR.

Final checks: Voice, Flue, and MeTube all returned HTTP 200. No stable Git tag or GitHub release was created. Development image/chart artifacts remain published for review.
