# Development installer rollout

Date: 2026-10-18

## Artifacts and deployed scope

Development version: `dev-installer-20261018-r1`. Built on the development machine; no remote compilation, stable tag, or GitHub release.

- Image: `ghcr.io/tailscale-x/tailscale-private-funnel:dev-installer-20261018-r1`
- Image digest: `sha256:ab80e2bf064773945dc6cbabdc48983f4d9d09e0f00f4e5b44f2adbc9e56f689`
- OCI chart version: `0.0.0-dev-installer-20261018-r1`
- Chart digest: `sha256:321829fbdf919b09f3c6f81a7ada0e5a5a9a24002dcc7b07ccffced914965a8f`

Updated native control and load balancer on xvps, native Docker gateways on Tiny and 5090, and the operator, Kubernetes gateway agent, and forwarder on 5090. Existing identities, provider settings, and ownership ledger remained intact. Native services reported ready and the Kubernetes gateway reported Ready on the development image. This build targets Linux amd64; this rollout does not establish multi-architecture acceptance.

## UI and CLI acceptance

Used the deployed control UI to generate native Tailnet and Kubernetes gateway commands. Instruction generation created no enrollment. Executing those commands issued installers; executing the installers downloaded the versioned binary from GHCR and successfully enrolled disposable gateways.

- Kubernetes installer generation succeeded with a nonexistent generator kubeconfig.
- The Kubernetes execution host could not connect to private control. Its generated installer still created the resources successfully; the workloads used embedded tsnet to enroll.
- Native gateway and Kubernetes gateway/forwarder reached readiness.
- Kubernetes bootstrap Secret was removed automatically.
- Issued bootstrap auth keys were absent from scanned native persistent files and decoded Kubernetes identity Secrets.
- Desktop 1440×1024 and mobile 390px creation flows had no horizontal overflow or browser console warnings/errors.

This complements the automated and disposable-local checks in [Installer workflow verification](qa-installer-workflow.md).

## Public routing and cleanup

Final checks: Voice and MeTube returned HTTPS 200; Flue's application ping returned HTTPS 200. All three certificate verification results were zero. Voice HTTP redirected with 308. Updates briefly interrupted traffic; these results establish recovery, not uninterrupted availability. No host reboot was performed during this rollout.

Deleted both disposable deployments through supported lifecycle commands. Their three Tailscale devices returned HTTP 404 afterward. Removed the temporary Kubernetes namespace, restored the operator's original namespace scope, and removed generated credential-bearing installers and exported kubeconfigs. No public QA DNS records were created.

Removed the exact temporary workstation administrator selector through the authorized API, preserving Owner access. Confirmed subsequent CLI access was denied and browser access returned HTTP 403. Live screenshots remain local rather than publishing private control addresses.

Final result: passed for development deployment and the installer workflow above. No stable release was created.
