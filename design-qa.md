# Control UI design QA

Reference direction: selected second ideation direction, a light topology-led
operations console. The reference image is kept in the product-design working
area at `/home/ubuntu/.codex/generated_images/01a0fb67-cb0a-7753-bcaf-61cb404a16d6/exec-d09f3a9b-88d3-48de-8167-1c2745fb2d4d.png`.

Implementation evidence:

- `docs/screenshots/control-dashboard-v1.png` — dashboard and topology
- `docs/screenshots/control-policy-v1.png` — exposure policy editor
- `docs/screenshots/control-enrollment-v1.png` — enrollment command stepper
- `docs/screenshots/control-operations-v1.png` — ownership preview and apply state
- `docs/architecture.svg` — deterministic system diagram

## Checks

- Desktop viewport: 1440×1024, light color scheme.
- Sidebar groups Overview, Infrastructure, Access, and Operations and marks the
  current route.
- Dashboard has the four health summaries, control-to-exposure topology, next
  step panel, audit activity, tailnet identity, and ownership sync state.
- Navigation and infrastructure symbols use embedded Tabler outline assets.
- Mobile viewport: 390×844. The sidebar collapses behind the menu control,
  topology cards stack, tables use labeled rows, command blocks wrap, and the
  document scroll width equals the viewport width (390px).
- Keyboard focus is visible and status text is available in the accessibility
  tree. Credentials are not rendered in the synthetic preview.
- Provider, zone, policy, enrollment, exposure, and operations routes retain
  their existing API actions and ownership/authentication behavior.
- Synthetic preview data uses `preview.ts.net`, `preview.example.com`, and
  redacted credentials only; no live hostnames or secrets are captured.

## Chrome DevTools verification

- Chrome DevTools MCP inspected all 12 control routes at 1440×1024 and 390×844.
- Every route had a document title, an `h1`, a populated `main`, no horizontal
  overflow, and no console errors or warnings.
- The mobile navigation opened with the menu button and kept the document at
  the 390px viewport width.
- Ingress enrollment was exercised end to end: hostname entry generated the
  native deployment script and rendered the policy and label guidance.
- Lighthouse snapshot audits passed at both viewports: Accessibility 100,
  Best Practices 100, SEO 100, and Agentic Browsing 100.
- Spacing fixes cover policy and operations checkboxes, long enrollment
  commands, and low-contrast supporting text. Dashboard health labels now
  reflect missing reports and untested providers instead of claiming readiness.

## Result

final result: passed
