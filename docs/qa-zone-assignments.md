# Provider-discovered zone assignments

Verified 2026-10-08. Zone-management gates: passed. Live routing verification detected intermittent upstream HTTP 502 responses; see rollout notes.

## Behavior

Zones are immutable DNS-provider/domain assignments. There is no manual domain
entry, zone editor, reassignment, or zone enabled state. DNS-provider and policy
enable/disable controls remain.

Add zone performs read-only discovery, search and multi-selection. Saving repeats
discovery server-side and checks DNS read access. Undiscovered or ambiguous
domains, duplicate assignments and removed request fields are rejected.
A domain must already exist with the DNS vendor; this flow does not register it,
change nameservers or create TXT markers.

Removal requires an empty owned-record ledger. To change providers, clean up the
owned records, remove the assignment, then select it from the new provider.

## Automated checks

- Both complete Go test suites, including the Caddy build tag, passed.
- Go vet and git diff checks passed.
- Local asset builds reproduced the same CSS checksum.
- Cloudflare and GoDaddy pagination, authentication headers and permission failures
  passed against HTTP fixtures.
- Route 53 public-zone filtering, pagination, hosted-zone restriction, ambiguity
  rejection and exact public-zone record selection passed against SDK fixtures.
- API tests passed for fresh discovery, failed reads without mutation, retries,
  duplicates, removed fields, role enforcement and ledger removal guards.
- Migration tests refuse disabled assignments and preserve credentials, active
  assignments and existing ledger ownership.

Cloudflare was also verified live. GoDaddy and AWS checks use fixtures; no live
credentials for those vendors were supplied.

## Chrome checks

Verified at 1440×1024 and 390×844 using synthetic data:

- Search, multi-selection and Already managed indicators.
- A partially failed save retains only the failed selection; retry completes.
- Loading, empty and permission-error guidance; discovery retry recovers.
- Escape/Cancel and focus restoration.
- No manual domain input, editing or zone toggle.
- Read-only roles hide mutation controls.
- Stacked mobile rows and no horizontal overflow.
- No console errors.

Temporary QA screenshots were inspected and removed.

## Control rollout

Built locally, with no compilation on the VPS. Only the control container was
updated; existing load balancer and ingress services were retained.

Development artifact: `dev-zone-selection-20261008-r2`

Image digest:
`sha256:d21246f11499350dfd2abedd2f5ef1e0622cc8b90295b435253fe1d8074b697c`

A consistent SQLite baseline confirmed there were no disabled assignments before
migration. Afterwards the enabled column was absent, and the existing assignment,
encrypted credential bytes and all three ledger entries matched exactly.
Cloudflare discovery returned two accessible domains, with one already managed.
No additional domain was assigned during verification.

Public Llama health returned HTTP 200. Public Flue returned HTTP 404, matching
its backend response. Post-restart checks encountered transient HTTP 502 responses on both exposures (an upstream EOF was logged for Flue); subsequent requests recovered to Llama 200 and the backend-matching Flue 404. A four-round probe returned Llama 200 in every round; Flue returned 404 in three rounds and 502 in one. Routing is functional but the intermittent upstream error remains unresolved. TLS and forwarding were verified after recovery. A control DNS
preview for both existing aliases proposed zero additions and zero removals.

No stable tag or GitHub release was created.
