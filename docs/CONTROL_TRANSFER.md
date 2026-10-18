# Transfer control to another deployment or database

`transfer-remote` moves an existing control. It stops the source, saves a complete private destination installer, verifies that file, and removes the source deployment and application-owned local state. It does not execute the destination installer or connect over SSH.

**Expect control downtime from source shutdown until the destination is ready.** Existing nodes retain their identities and reconnect to the restored control. Routing recovery depends on current reports, Service readiness and existing leases; transfer does not promise uninterrupted traffic.

Use the same pinned transfer-capable version for the generator, source Kubernetes operator and destination operator. Upgrade operators and CRDs first. Version v3.1.0 predates this feature; v3.2.1 includes it. Upgrade and start the native source at v3.2.1 before running transfer. See the [release instructions](releases/v3.2.1.md).

## Native source → Kubernetes and PostgreSQL

Run on the source control host, with permission to manage its system service:

```sh
sudo tailscale-private-funnel transfer-remote main k8s \
  --source native \
  --context DESTINATION_CONTEXT \
  --namespace tailscale-private-funnel-main-system \
  --database postgres \
  --database-secret funnel-db-app --database-secret-key uri \
  --database-ca-secret funnel-db-ca --database-ca-secret-key ca.crt \
  --version VERSION --wait \
  --out /secure/export/restore-main.sh --yes
```

The generator does not load destination kubeconfig or contact the destination cluster. Secret names and context are destination references. Execute the saved installer on a machine with that context, the matching Funnel operator, a managed namespace, and an empty dedicated database:

```sh
sh /secure/export/restore-main.sh
```

The execution machine does not need Tailscale or private-control connectivity. Control restores its embedded Tailscale state before starting. The operator retains its normal restricted identity/database permissions. Transfer helpers are temporary Pods; database data travels over the Kubernetes exec stream rather than Pod logs or a large Secret.

## Kubernetes source → native

Run from a machine with source Kubernetes administrative access and `kubectl`:

```sh
tailscale-private-funnel transfer-remote main native \
  --source k8s --source-context SOURCE_CONTEXT \
  --source-namespace tailscale-private-funnel-main-system \
  --version VERSION --wait \
  --out ./restore-main.sh --yes

ssh ubuntu@destination 'sh -s' < ./restore-main.sh
```

The default destination backend is SQLite. For native PostgreSQL, add `--database postgres --database-url-file /etc/tailscale-private-funnel/database.url` and, when needed, `--database-ca-file`. These protected files must exist on the destination host. The installer installs a system-level native service using the standard Linux state/data paths; `--state-dir` and `--data-dir` can select other safe paths.

For Kubernetes → Kubernetes, use both `--source-context`/`--source-namespace` and destination `--context`/`--namespace`. For native → native, execute the generator on the source and the saved script on the destination. `--source-kubeconfig` optionally selects the source configuration file; destination installers use their execution machine's kubeconfig.

## What moves and what remains

The versioned, database-neutral package preserves resource IDs, encrypted OAuth/provider credentials, policies, nodes, enrollments, reports, DNS ownership ledger, control signing key, encryption master key, storage binding, and full Tailscale state. SQLite/PostgreSQL schema history is not copied as SQL. Application data is imported transactionally into the destination backend's current schema.

OAuth normally does not need to be supplied again. To rotate it during transfer, specify both `--oauth-client-id-file` and `--oauth-client-secret-file`; paths refer to protected files on the destination execution machine, and replacement values are encrypted before import. Existing enrolled-node trust and the Tailscale device registration remain intact. The same Tailscale device ID/addresses are expected while that registration remains valid.

The source is fenced before export. Native systemd services are stopped, disabled and masked. Kubernetes reconciliation holds the control at zero replicas. Transfer removal never performs ordinary deletion: it does not revoke the Tailscale device, disapprove Service hosts, or delete DNS records. Referenced external databases, Secrets and volumes remain; a retained source database is marked transferred and refuses normal startup. The old immutable systemd unit mask remains as a non-secret restart fence.

For GitOps sources, suspend automatic reconciliation and remove the old control declaration from its source of truth before transfer. Source components using `controlRef` are changed to the same explicit management URL before the source CR is removed. Update their GitOps declarations to retain that URL binding before resuming sync. Reapplying the old control declaration or recreating credentials/state could defeat source fencing. No CLI can fence an independent administrator or a copied identity bundle.

## Output, failures, and recovery

- `--out` is required and must name a new file outside source state/data. Existing files and symlinks are rejected. The parent directory must exist. The file is saved atomically, synchronized and verified with mode `0600` before any removal.
- The installer contains sensitive credentials and identity material. Store it securely, copy it through an authenticated channel, and never commit or upload it as a release asset. When generated with sudo, read/execute it with appropriate privileges. Remove copies securely after acceptance.
- `--dry-run` inspects and previews only. It does not stop control, export secrets, create a file or modify resources. `--yes` confirms destructive source removal; `--wait` and timeout apply to readiness during destination execution, with the timeout also bounding source transfer work.
- Before the installer is saved and verified, failure leaves source data retained and control stopped. No error restarts the source automatically. After verified export, a removal error identifies the saved installer and leaves remaining source resources fenced.
- If destination restore fails, it stays fenced and will not bootstrap a fresh identity. Inspect the failure, remove only the failed destination's application state without invoking identity/DNS revocation, and execute the saved installer against a new empty destination. There is no public resume or restore command.
- Destination names and databases must be unused. A copied installer must be executed for only one destination. Its credentials are an existing persistent identity, so it has no enrollment-key expiry; file access is the security boundary.

After activation, verify control readiness, Tailscale identity/IP, enrolled-node reports, DNS ownership and public routes before discarding the installer. Normal backups of the destination database and matching identity material remain necessary.
