# DNS provider and zone management verification

Date: 2026-10-08

## Implemented flows

- DNS provider terminology and vendor-specific Add DNS provider dialog.
- Inline credential editing, blank-secret preservation, row refresh, Save/Cancel,
  duplicate-name rejection, and inline errors.
- Add zone dialog: provider preselection, read-only discovery, search,
  multi-selection, already-managed indicators, provider-discovered domains, and partial retries.
- Zone editing, enable/disable, read testing, removal and ledger-protected provider
  removal guards.
- Node-derived reconciliation ownership and mode. Removed request fields are
  rejected. Unknown nodes are rejected; revoked nodes accept cleanup only.
- Automatic internal ledger protection with no TXT markers or new owner IDs.

## Local gates

Passed `go test ./...`, `go test -tags caddy ./...`, `go vet ./...`,
`node --check internal/web/assets/dns-management.js`, and `git diff --check`.
Two local UI asset builds produced identical CSS:
`ade1e141b2b0bf353904a317f14f2c188d7e667280e85109febf3b5930bf2e8b`.

Regression tests cover old-schema migration with unchanged encrypted credentials
and ledger owners, removed API fields, duplicate zone/provider prevention,
node-derived ownership, revoked cleanup, admin-only mutation, provider secret
redaction and preservation, and cross-zone ownership boundaries. A live browser found SQLite contention;
serialized connections, WAL and a busy timeout prevent lost writes. Concurrent
settings/audit tests pass, and authorization settings errors now fail closed
without initiating first-time setup. Existing access roles were preserved.

Chrome QA used synthetic providers/domains at 1440×1024 and 390×844. Verified:

- Inline edit/save/cancel, blank secret fields and focus restoration.
- Dialog focus, Escape/Cancel, vendor changes and reopening with defaults.
- Loading, empty and permission-denied discovery states with vendor permission guidance.
- Multi-zone partial success; failed selections remain checked and retry succeeds.
- Zone edit/save, provider change without ledger entries, read test, disable/remove.
- Operations preview invalidation when inputs change.
- Read-only operator UI and server rejection of mutation.
- No horizontal overflow in pages/dialogs, and no runtime console errors.

Screenshots were visually inspected and temporary QA images removed. Expected
403/409 responses from deliberate authorization/duplicate tests are not runtime
JavaScript failures.

## Deployment

Only a locally prebuilt control artifact was deployed. Load balancer and ingress
runtimes were left running.

Image: `ghcr.io/tailscale-x/tailscale-private-funnel:dns-ui-20261008-r4`

Pinned digest:
`sha256:4c0eb800b928d662d61802c3ce7972851dd4913e97b0a641052ee5e044787c87`

Version: `dev-dns-ui-20261008-r4`.

Database checks compared the prior snapshot: managed-zone assignments, encrypted
provider blobs, reports and all three record-ledger rows were preserved exactly.
The managed-zone schema now contains only provider and domain.
During live QA, the former SQLite contention also caused a policy read to fall
back to an empty policy and withdraw ingress DNS. The saved rules were restored
through the control API (revision 2), and both exposures recovered. Policy reads
now never persist an empty fallback on failure. Reports and reconciliation stop
on unreadable policy; regression coverage verifies preservation of rules and DNS.

Live Cloudflare discovery works; it adds no zones. The existing ingress desired
state has a zero-add, zero-remove preview and correct management classifications.

Llama HTTPS health returns 200. Flue public HTTPS returns the same 404 as its
backend; routing is preserved, but this does not establish a working Flue page.

No stable tag or GitHub release was created. Existing uncommitted work is retained.

Final result: passed.
