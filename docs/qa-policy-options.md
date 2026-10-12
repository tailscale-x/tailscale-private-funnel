# Optional exposure restrictions and JSON formatting

Verified 2026-10-08.

- Hostname regex remains required, full-match and default-deny.
- Blank roles and ingress IDs allow any enrolled ingress matching the hostname.
  If either selector list is populated, the existing role-or-node matching applies.
- Blank load-balancer hostnames impose no load-balancer restriction.
- Rule tables and help text explain the unrestricted defaults.
- Existing saved rules are not rewritten or broadened automatically.
- Advanced policy JSON is indented on rendering, valid paste, blur, validation
  and saving. Format JSON prettifies without mutation. Invalid input is preserved.
- Management JSON responses are indented, including displayed validation and
  operation results.

Both Go test suites, go vet and diff checks passed. Focused tests cover optional
selectors, restricted matching, invalid hostnames, disabled rules, unchanged
default-deny, rule API creation and formatted responses.

Chrome verified a hostname-only rule can be created through the guided form,
JSON formatting does not change the policy revision, invalid JSON remains intact,
and the page fits at 390px with no console errors. Browser data was synthetic.

Control artifact built locally:
`dev-policy-options-20261008`

Image:
`ghcr.io/tailscale-x/tailscale-private-funnel@sha256:cf9b87cb022031d595046d5f9d2ac2e1cbbba19272a75ad4d2f7b66bae18fc5a`

Only control was updated. No stable tag or GitHub release was created.

Live verification confirmed the saved policy was unchanged (revision 4), formatted
JSON and optional field labels were present, and existing assignments, encrypted
credentials and all ledger entries matched the prior baseline.
