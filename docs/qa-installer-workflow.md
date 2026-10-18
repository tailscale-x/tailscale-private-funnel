# Installer workflow verification

Date: 2026-10-18 (Asia/Saigon)

## Verified behavior

- Public native and Kubernetes `create` generate installers; removed commands and public bootstrap flags are rejected.
- Non-control generation requests authorization once, without local installation, kubeconfig loading, or cluster access. Control/operator generation does not read target OAuth files.
- Scripts carry the complete canonical bundle through stdin to hidden installers. Shell tests execute the generated scripts with artifact download stubbed, including paths with spaces.
- Preview JSON and shell cannot install, including control/operator previews. Failed output delivery attempts enrollment revocation.
- Enrollment expiry is enforced at server-side redemption and atomic binding, not only by the target's clock. Existing enrolled identity is unaffected by expiry.
- Missing Gateway API discovery now fails Kubernetes installation before creating a component CR or bootstrap Secret.

## Real local enrollment and persistence

Used an independent disposable control, native Tailnet gateway, and two-node k3d cluster. Only the disposable control authorized the test workstation; the deployed control's access settings were untouched. No provider, public exposure, or DNS records were created.

1. Generated a control installer with protected OAuth-file references and initialized a separate encrypted database and tsnet identity.
2. Issued native and Kubernetes installers through that control's authorized Connect API.
3. Executed native installation through the hidden installer; the gateway joined its own embedded tsnet identity and submitted fresh reports.
4. Executed Kubernetes installation with the existing kubeconfig. A locally built operator reconciled the gateway agent and two forwarders. These Pods had no host Tailscale daemon or TUN access.
5. Verified readiness and automatic removal of the bootstrap Secret. Auth keys were absent from persistent native files and Kubernetes identity Secrets.
6. Restarted native gateway/control processes, replaced the Kubernetes agent Pod, and rolled both forwarder Pods. Control retained its address; all four enrolled node identities stayed unchanged.
7. Deleted the gateway CR through its finalizer, deleted both native deployments through the CLI, and confirmed all five disposable Tailscale devices returned HTTP 404. No gateway Secrets remained. Removed the operator, k3d cluster, temporary bootstrap bundles, exported Secret data, test units/state, and installed test binary cache.

The local chart and image were used for this test; no OCI package was published. Existing deployments were not upgraded. This was process/Pod restart verification, not a host reboot or new WAN/TLS acceptance test.

## Automated gates

Passed:

- `go test ./...`
- `go test -tags caddy ./...`
- `go vet ./...`
- envtest installer/API-boundary and operator lifecycle tests
- two-node k3d installer/API-boundary test plus real enrolled gateway/forwarder provisioning
- `buf lint`, reproducible protobuf/documentation/UI assets (46 generated or bundled files)
- Helm lint/template, local Linux binary build, Docker build and startup
- nonroot image preview and disconnected-cluster operator installer generation
- `git diff --check`

## Browser QA

Used the available Chrome DevTools MCP with synthetic fixture data; the Browser plugin was unavailable. Native load-balancer, Kubernetes gateway, and operator instruction flows correctly distinguish generator and execution machines. Copy and download actions work. Viewer access hides the generation form.

Desktop 1440×1024 and mobile 390px checks found no horizontal overflow. Fixed the resource-name pattern for Chrome's HTML `v` regex semantics; the final tested flow has no console errors. Screenshots were captured locally without credentials.

Final result: passed for the installer workflow described above. No stable tag, GitHub release, or production deployment was created.
