# Control transfer verification

Local development verification, 2026-10-18. This does not describe a published release or a migration of an existing deployment.

## Live disposable acceptance

A new native control was provisioned using protected OAuth files. Its single persistent Tailscale identity was then transferred seven times using generated installer payloads and the locally prebuilt binary:

1. Native SQLite → Kubernetes SQLite.
2. Kubernetes SQLite → native SQLite.
3. Native SQLite → native PostgreSQL, using a restricted database-owner role.
4. Native PostgreSQL → native SQLite.
5. Native SQLite → Kubernetes SQLite.
6. Kubernetes SQLite → Kubernetes SQLite in another namespace.
7. Kubernetes SQLite → native SQLite.

The IPv4 and IPv6 addresses stayed unchanged. Every saved transfer package retained identical master-key, configuration-signing-key and Tailscale machine-key hashes. Restored controls reached readiness. Source Kubernetes Pods, Secrets and owned SQLite PVCs were removed. The external PostgreSQL database remained and contained the transferred-source startup fence.

The test executed the generated payload with hidden restore commands from a prebuilt development binary. POSIX script delivery was additionally tested with a stubbed artifact downloader. Downloading this unpublished development version from a public registry/release was not claimed as acceptance.

The initial native test found that a fresh control's signing key was initialized lazily. Export refused deletion and retained the fenced source. Control startup now initializes the signing identity before joining/readiness, with regression coverage.

The disposable control was deleted through its normal lifecycle after testing; the Tailscale API confirmed that exact device returned HTTP 404. Test systemd units/masks, cluster, PostgreSQL containers/anonymous volumes and credential-bearing installers were removed. No existing application deployment, DNS record, or public TLS configuration was changed.

## Automated gates

- Portable database contracts passed against SQLite and PostgreSQL 16/18, including every application table, credential ciphertext, immutable IDs, DNS ownership, audit sequences, failed-import rollback and transferred-source startup rejection.
- Native lifecycle, output durability/permissions, shell/stdin delivery, malformed-package rejection and failure ordering tests passed.
- Operator/envtest gates covered durable suspension, preservation of external resources, finalizer behavior, and immutable explicit control-URL handoff for dependent components.
- Both Go suites, vet, generator reproducibility, Helm validation, diff checks and the final local image startup were checked.

See [control transfer instructions](../CONTROL_TRANSFER.md). Cross-cluster transfer uses explicit source and destination contexts; the live Kubernetes-to-Kubernetes test used separate namespaces in one disposable k3d cluster. Continuous public traffic during transfer is not guaranteed.
