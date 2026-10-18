# PostgreSQL, CloudNativePG, and Argo CD

SQLite remains the default. PostgreSQL supports fresh control installations and explicit offline [control transfer](CONTROL_TRANSFER.md) from SQLite or PostgreSQL. There is no automatic backend switch or live migration. This support does not make control active-active: it remains one replica with a Recreate strategy.

## Database contract

Provide an existing, empty, dedicated PostgreSQL database owned by the application role. Funnel creates its tables and applies embedded migrations transactionally under a migration advisory lock. It does not create databases, users, CloudNativePG resources, or extensions, and requires no superuser privileges. PostgreSQL 16 and 18 are covered by storage contract tests.

Credentials remain encrypted in the database. The master encryption key, control signing identity, and Tailscale identity stay in the Kubernetes identity Secret (or protected native state/data directories). Losing that Secret cannot be repaired by restoring the database alone. Back up **the database and matching identity Secret together**. Never regenerate keys against an initialized database.

PostgreSQL connection failure does not create a SQLite fallback. Readiness fails while the database is unavailable; liveness remains independent so transient database failover does not deliberately reset the Tailscale identity. Existing connections reconnect through the configured database Service. Credentials and TLS configuration are reloaded on controlled process/Pod restart.

## CloudNativePG and GitOps

See [the operator Argo CD application](../examples/gitops/cloudnativepg/operator-application.yaml) and [the declarative example](../examples/gitops/cloudnativepg/control.yaml). It references CloudNativePG's application Secret (`uri` key), the read-write Service encoded in that URI, and its CA Secret. It uses `verify-full`; it never connects to a specific database Pod or uses superuser credentials.

1. Install CloudNativePG and the Funnel operator using your existing Argo CD applications. Configure Funnel's operator to manage the chosen installation namespace. Use the pinned v3.2.1 image/chart or a later compatible release; v3.1.0 does not include PostgreSQL support.
2. Manage the namespace and CNPG Cluster in Git. If the database already exists, reference its credentials instead of creating another Cluster. Secret references are namespace-local; arrange credential delivery into the control namespace through your existing secret-management process when the database is elsewhere.
3. Provision `funnel-control-oauth` through your secret manager, with `client-id` and `client-secret` keys. Do not commit OAuth or PostgreSQL credentials in plaintext. These are bootstrap inputs; repeat syncs and Pod restarts reuse the initialized identity and encrypted database settings. Externally managed bootstrap Secrets are not deleted by Funnel.
4. Apply `FunnelControl` after its dependencies. The example uses Argo sync waves. Merge the optional [health customization](../examples/gitops/cloudnativepg/argocd-health.yaml) into your existing Argo configuration; control is healthy only after its current generation reports Ready.
5. Follow `status.setupURL` to the tailnet-only management UI. Configure providers, zones and exposure policy there. A new installation does not own existing Funnel DNS records; any reclaim requires its own explicit overwrite preview and approval.

Argo owns the operator release, CNPG resources, referenced configuration and `FunnelControl`. Funnel owns its generated Deployment, identity Secret and RBAC. Do not separately render/manage those generated resources through Argo. Runtime-generated identity Secrets must not be overwritten with empty desired data. Externally supplied identity Secrets must have their runtime data excluded from Argo reconciliation.

PostgreSQL controls allocate no SQLite PVC. NetworkPolicy permits TCP 5432 to same-namespace Pods by default; use `postgres.egressPeers` and `port` to restrict it to the actual database, or explicitly select another namespace/IP block for external PostgreSQL. This rule applies only to control.

`spec.database` is immutable. Database changes require an explicit offline control transfer into a new deployment. Referenced PostgreSQL resources are never adopted or deleted by Funnel. Control deletion still completes owned node/DNS cleanup, then removes owned infrastructure; **the external database remains**. Back up the matching identity Secret before deletion, or supply an externally managed identity Secret if retention is needed. Removing an Argo application is not a database reset procedure.

## CLI and UI

Control creation offers SQLite or PostgreSQL. The UI accepts file/Secret references, not passwords, and displays backend/readiness under Controls. Generated commands use the shared typed deployment contract.

```sh
# Generates an installer; context and Secrets belong to the execution cluster.
tailscale-private-funnel k8s controls create main \
  --context TARGET_CONTEXT --namespace tailscale-private-funnel-main-system \
  --tailnet example.ts.net --database postgres \
  --database-secret funnel-db-app --database-secret-key uri \
  --database-ca-secret funnel-db-ca --database-ca-secret-key ca.crt \
  --output script

# Native input files must exist on the execution machine, not the generator.
tailscale-private-funnel native controls create main \
  --tailnet example.ts.net --database postgres \
  --database-url-file /etc/tailscale-private-funnel/database.url \
  --database-ca-file /etc/tailscale-private-funnel/postgres-ca.crt \
  --oauth-client-id-file /etc/tailscale-private-funnel/ts-client-id \
  --oauth-client-secret-file /etc/tailscale-private-funnel/ts-client-secret \
  --output script
```

Native connection files must be mode 0600. Installation copies them into protected, service-owned data storage; status reports the resulting paths. For native credential rotation, replace that owned connection file and restart control. For Kubernetes rotation, update the referenced Secret through its owner and restart the control Deployment. CA files are public certificates, but must remain readable by the service. TLS defaults to `verify-full`; `verify-ca` and `require` are explicit alternatives. Use `disable` only for isolated development.

`manage health --output json` reports `databaseBackend` and `databaseReady`; native status includes corresponding runtime fields. Connection strings and credentials are excluded from status, rendered commands, receipts, Events, and logs.

## Local verification

Run normal suites with SQLite. To additionally run the shared PostgreSQL contracts, provide `FUNNEL_TEST_POSTGRES_URL` pointing at a **disposable administrative test database**; tests create/drop isolated databases and restricted owner roles. Production control requires only the application owner.

```sh
FUNNEL_TEST_POSTGRES_URL=postgres://postgres:TEST_PASSWORD@localhost:5432/postgres \
  go test ./internal/store -run 'TestDatabaseContract|TestPostgres' -count=1
```

`TestPostgresNodeBundleRoundTrip` additionally uses a dedicated empty database through `FUNNEL_TEST_CONTROL_POSTGRES_URL`, with `FUNNEL_TEST_CONTROL_CA_FILE` and optional `FUNNEL_TEST_CONTROL_TLS_MODE` for fixture configuration. Never point these tests at an existing installation.
