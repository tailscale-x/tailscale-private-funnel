# DNS recovery and control UI review

Date: 2026-10-09. Tool: Chrome DevTools MCP. Synthetic, redacted local fixture; 1440×1024 and 390×1024.

## Changes

DNS recovery now belongs to each managed zone: **DNS details → Preview changes / Retry sync**. Exposures link to their zone. Operations contains audit history, runtime information, and recovery guidance. The raw DNS editor route is removed. Desired records are reconstructed server-side; exact ledger ownership and preview-bound overwrite approval remain enforced. Endpoint ambiguity retains the existing load-balancer A record while independent valid partitions continue.

The screenshot review also corrected hidden controls, secondary-button borders, action spacing, mobile navigation focus and inert state, active navigation, misleading top-bar text, footer links, node-specific fields, policy selector labels, and a broken enrollment back link.

## Review steps

All steps passed the browser smoke checks: no horizontal overflow at either width and no browser console errors. Keyboard checks covered mobile navigation, dialog Escape/Cancel, and DNS dialog focus restoration after table replacement. This is not a screen-reader or WCAG certification.

### 1. Dashboard — passed

Clear health and next actions.

![Dashboard, desktop](qa/dns-zones/01-1440.png)

[Mobile screenshot](qa/dns-zones/01-390.png)

### 2. Load balancers — passed

Node details and technical API links are distinguished.

![Load balancers, desktop](qa/dns-zones/02-1440.png)

[Mobile screenshot](qa/dns-zones/02-390.png)

### 3. Ingress nodes — passed

Name and hostname are separate; source-specific details are available.

![Ingress nodes, desktop](qa/dns-zones/03-1440.png)

[Mobile screenshot](qa/dns-zones/03-390.png)

### 4. Applications — passed

Rename controls are collapsed instead of distracting from inventory.

![Applications, desktop](qa/dns-zones/04-1440.png)

[Mobile screenshot](qa/dns-zones/04-390.png)

### 5. Exposures — passed

Backend health and DNS recovery links are shown separately.

![Exposures, desktop](qa/dns-zones/05-1440.png)

[Mobile screenshot](qa/dns-zones/05-390.png)

### 6. Tailnet exposures — passed

Peer dropdown, policy check, connectivity test, save and cancel were exercised.

![Tailnet exposures, desktop](qa/dns-zones/06-1440.png)

[Mobile screenshot](qa/dns-zones/06-390.png)

### 7. DNS providers — passed

Inline editing preserves blank secrets; Cancel restores the row.

![DNS providers, desktop](qa/dns-zones/07-1440.png)

[Mobile screenshot](qa/dns-zones/07-390.png)

### 8. Tailscale settings — passed

Forms retain existing settings and distinguish server-side credentials.

![Tailscale settings, desktop](qa/dns-zones/08-1440.png)

[Mobile screenshot](qa/dns-zones/08-390.png)

### 9. Access roles — passed

Operator preview and administrator apply permissions are explained.

![Access roles, desktop](qa/dns-zones/09-1440.png)

[Mobile screenshot](qa/dns-zones/09-390.png)

### 10. Exposure policy — passed

Display labels remain readable; hidden Cancel controls and policy test labels were corrected.

![Exposure policy, desktop](qa/dns-zones/10-1440.png)

[Mobile screenshot](qa/dns-zones/10-390.png)

### 11. Load balancer enrollment — passed

Generated command and correct inventory back link were checked.

![Load balancer enrollment, desktop](qa/dns-zones/11-1440.png)

[Mobile screenshot](qa/dns-zones/11-390.png)

### 12. Ingress enrollment — passed

Tailnet-only generated command and corrected ingress back link were checked.

![Ingress enrollment, desktop](qa/dns-zones/12-1440.png)

[Mobile screenshot](qa/dns-zones/12-390.png)

### 13. Operations — passed

Audit, runtime and rollback replace the raw DNS editor.

![Operations, desktop](qa/dns-zones/13-1440.png)

[Mobile screenshot](qa/dns-zones/13-390.png)

### 14. Zones — passed

Add zone discovery is read-only; Already managed selections are disabled.

![Zones, desktop](qa/dns-zones/14-1440.png)

[Mobile screenshot](qa/dns-zones/14-390.png)

### 15. Ingress details — passed

Mode-specific deployment choices and backend access are shown.

![Ingress details, desktop](qa/dns-zones/15-1440.png)

[Mobile screenshot](qa/dns-zones/15-390.png)

### 16. Load balancer details — passed

Certificate settings are separated from ingress-only fields.

![Load balancer details, desktop](qa/dns-zones/16-1440.png)

[Mobile screenshot](qa/dns-zones/16-390.png)

### 17. Zone DNS recovery — passed

Preview and Retry use server-derived records; closing after Retry restores focus.

![Zone DNS recovery, desktop](qa/dns-zones/17-1440.png)

[Mobile screenshot](qa/dns-zones/17-390.png)

## Verification

- `go test ./...`: passed.
- `go test -tags caddy ./...`: passed.
- `go vet ./...`: passed.
- `git diff --check`: passed.
- Tailwind asset build and templ generation: passed; output hashes unchanged on repeat.
- Focused DNS tests cover preview/apply, stale overwrite approval, unowned conflicts, endpoint retention, expired reports, policy withdrawal, removed raw fields, unknown zones and role enforcement.
- Browser fixture tests provider edit/cancel, discovered-zone dialog, peer dropdown, tailnet exposure creation, node commands, zone preview/retry and mobile navigation. Vendor calls in this fixture are mocked; live rollout checks are recorded below.

## Deployment

Development control artifact: `dev-zone-dns-20261010`, digest `sha256:56258937e8f77b6c89b3bb986198d4e65c4e96eaecfbf6a95236971652c38820`. Built locally; only the control container is replaced. No stable tag or release.

Live verification: control is running the exact digest above; the ingress resumes authenticated configuration/report calls. Llama `/health` and Flue `/api/ping` both succeed over public HTTPS. Ledger count remains 3, managed-zone count remains 2, and the ledger hash is unchanged (`822fa192ef2b17579f669d9f9bf7a2ff1019b8b3bfbf57cd09fa1464b95c9ae5`).

Live management preview was blocked by authorization: the diagnostic browser uses a tagged machine identity without a management viewer role. Authorization was preserved. The complete management flow was verified against the synthetic local fixture; a live administrator-side preview remains unverified.

final result: passed (local UI and handler gates; live management-preview limit noted above).

## Follow-up review — 10 October 2026

1. DNS provider table — passed. Recaptured the final desktop view with correct status-column spacing.
2. Zone DNS recovery — passed. Desktop hostname and target values stay intact instead of breaking mid-word; mobile values retain wrapping. Chrome verified both widths without page overflow. Updated screenshots above. Asset output is reproducible.
3. Live management authorization — blocked for this diagnostic identity. The diagnostic PC is tagged, so it does not inherit human `autogroup:owner` privileges. No access mapping or ACL was broadened. An administrator can verify live Preview changes from their authorized browser.

### Follow-up rollout verification

The control container now runs `dev-zone-dns-20261010` at the digest above. Flue `/api/ping` passes. The first Llama check revealed a runtime/network issue: `llama-router` was attached only to Docker’s excluded default bridge. Its expose label and policy remained valid. Attached a dedicated user-defined `llama-private-funnel` network; ingress created its credential-free relay and control automatically restored the owned `llm.dlkn.dev` CNAME. No direct vendor mutation was used.

DNS through `1.1.1.1` resolves the CNAME to the load balancer, and HTTPS with the correct SNI/Host reaches Llama `/health` (`status: ok`). The local resolver temporarily cached the earlier absent record, so the initial TLS check used `curl --resolve` with the published IP; the final ordinary HTTPS request also passed. There are now four owned ledger records in two zones, including the restored Llama CNAME. This intentionally differs from the earlier three-record baseline. Existing container network membership persists across restart; replacement launchers must retain the user-defined network. The earlier unchanged-ledger result refers to the 9 October rollout, before this restoration.

### Authorized live preview — 10 October 2026

At the user’s explicit request, granted admin to the diagnostic machine’s immutable verified Tailscale node ID while preserving `autogroup:owner`. Bootstrapped the access-role setting through authenticated SSH with a transactional change and audit event; no shared tag grant or tailnet ACL was broadened. Live Chrome management access now succeeds and administrator recovery controls render. Dry-run previews for both managed zones returned HTTP 200, no pending errors, zero additions and zero removals. No DNS apply was performed. This resolves the earlier live-management verification blocker.
