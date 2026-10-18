# Nested provider DNS names: controlled patch verification

## Defect and correction

`NormalizeRecords` previously treated a dot anywhere in a provider-relative owner
name as evidence that the name was already qualified. Cloudflare can return
nested relative names, so existing records failed to match desired records and
reconciliation attempted duplicate additions (`81058`).

Qualify every relative owner name outside the managed zone, including nested
labels. Preserve same-zone qualified names, explicit trailing-dot absolute names,
apex handling, and record data. Ownership checks and overwrite approval remain
unchanged. Regression tests cover normalization and repeated ownership-scoped
reconciliation with no duplicate additions.

## Deployment

- Control: existing native `main` on xvps.
- Artifact: `dev-v3.1.0-nested-dns-20261018`, built locally from exact `v3.1.0`
  plus the DNS fix; no unrelated version/schema upgrade.
- SHA-256: `41456671b3f4461e0246008e9b8adcffdad0028e8fb931005cc4a9be2b718a38`.
- Deployed using native control update, preserving the original state/data paths.
- Same control addresses after restart: `100.116.115.48` and
  `fd7a:115c:a1e0::3236:7332`.
- Encryption-key hash, encrypted-credential hash, and enrolled node identities
  match before and after update.
- Stopped-state recovery archive is root-only on xvps at
  `/var/backups/tailscale-private-funnel/pre-nested-dns-20261018.tar.gz`.
- No control transfer, identity reset, Cloudflare Access change, or direct DNS
  provider mutation.

## Live acceptance

After restarting `ulaw-local-caddy-edge-1`:

| Request | Expected / observed |
| --- | --- |
| `https://tuyensinhhcmulaw.troly.ai.vn/login` | 200 |
| `https://sso.tuyensinhhcmulaw.troly.ai.vn/login` | 200 |
| `https://api.tuyensinhhcmulaw.troly.ai.vn/health/ready` | 200 |
| `https://api.tuyensinhhcmulaw.troly.ai.vn/` | 404 |
| `https://api.tuyensinhhcmulaw.troly.ai.vn/api/crm/me` | 404 |
| `https://metube.troly.ai.vn/` | 200 |

HTTPS probes use normal certificate verification. Local detailed evidence is
under `/tmp/funnel-dns-verification/`; it contains no credential values.

The normalization fix exposed two pre-existing orphaned API/SSO A records as
unmanaged, rather than trying to append duplicates. The user approved adoption
of exactly those two records at `160.191.243.219`. Both were adopted using fresh
control previews and their approval tokens. Each adoption required zero provider
additions/removals; the existing Zalo verification TXT record was preserved.

A subsequent zone preview returned no pending work, no conflicts, and zero
additions/removals for all six active exposure partitions. The control's
`exposure_mapping_sync` state reports an empty error list and `pending=false`.

Recovery was not uninterrupted: initial probes before ownership recovery included
timeouts and temporary missing routes. Final acceptance uses a separate
post-adoption observation window, including a second edge-container restart.

The temporary QA administrator selector was removed through the authorized API.
The admin role again contains only `autogroup:owner`; a subsequent request from
this PC was denied. No enduring tagged-device administrator access was added.

Verification completed locally:

- `go test ./...`
- `go test -tags caddy ./...`
- `go vet ./...`
- `git diff --check`

Source changes remain in the product checkout. The deployed artifact is a
controlled v3.1.0 patch, not a newly published stable release.

Final observation: 60 HTTPS checks passed across ten rounds from 2026-10-18T06:39:04.215713+00:00 to 2026-10-18T06:41:42.812504+00:00. At least 24 further successful reconciliation cycles per admissions/Metube exposure left the ledger unchanged. No `81058` errors occurred in the patched process. The second edge-container restart occurred within this observation window.
