# Policy management and multi-domain DNS verification

## Local gates

- `go test ./...`, `go test -tags caddy ./...`, and `go vet ./...`: passed.
- UI asset rebuild produces the same embedded CSS SHA-256.
- `git diff --check`: passed.
- Local binary and Docker image build: passed; no builds ran on the VPS.

Tests cover stable rule IDs, editing without duplication, revision conflicts,
validation without saving, enable/disable and deletion, administrator enforcement,
read-only UI roles, encrypted credential retention, provider-zone migration,
zone discovery failure, domain-boundary and most-specific matching, ambiguous
assignments, independent zone failures, signed-report reconciliation across zones,
retry cleanup, overlapping rules, unrelated record preservation, and keeping a
shared Service approved while an authorized assignment still needs it.

## Chrome UI checks

Synthetic fixture: `PF_UI_PREVIEW=1 go test ./internal/web -run TestBrowserPreview
-count=1 -v -timeout=30m`, bound only to localhost. The fixture is test-only and
is not linked into release binaries.

Verified at 1440×1024 and 390×844:

- Edit populates existing fields and retains one rule; Cancel clears edit state.
- Enable/disable and Delete use an affected-exposure confirmation.
- Validate only returns `saved: false`; policy JSON saving is separate.
- Cloudflare discovery offers multiple selectable zones; assignments appear in
  the refreshed table and are not created by discovery alone.
- Zone edit, owner update, toggle, and read-access testing work.
- Credential editing accepts blank replacements without losing secrets.
- Viewer/operator pages hide mutation controls and mutation endpoints reject them.
- Tables stack, form fields fit, and no horizontal overflow occurs at 390px.
- Browser console has no application errors or warnings.

Visual comparison used the existing `docs/screenshots/control-policy-v1.png`
reference and final Chrome screenshots, inspected with `view_image`.
The sidebar, neutral canvas, navy typography, blue controls, table/form spacing,
and responsive stacking retain the existing design. Fixed cramped action links,
section heading spacing, and credential groups whose `hidden` attribute was
previously overridden by component CSS. Copy and table actions intentionally
changed to describe the new operations. No new visual direction was introduced.

## Deployment scope

Development image: `ghcr.io/tailscale-x/tailscale-private-funnel:policy-zones-20261008-r3`.
Only control is updated; load balancer, ingress, and relay containers keep their
existing runtime deployments. No stable tag or GitHub release is created.

Live Cloudflare checks use the existing `dlkn.dev` zone. Multiple-zone behavior
is validated with local provider fixtures; no second live domain was supplied.

Live verification preserved the original managed-zone ownership ID and three
ledger records (two ingress CNAMEs and one load-balancer A). Cloudflare discovery
and read testing passed; the alias dry-run returned zero additions/removals and
required no overwrite approval. Llama HTTPS `/health` returned 200. Flue HTTPS
returned 404, matching the direct backend response; TLS and routing remained
operational. No live rule was disabled or deleted for destructive testing.

Verified image digest:
`sha256:a5d45a9719843594b64449332e9003c3da59a19913b8b81aa6c3db237ec36ff8`.
