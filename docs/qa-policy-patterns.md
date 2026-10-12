# Multiple hostname patterns per exposure rule

Verified 2026-10-08.

- Rules serialize patterns as a `hostname_regexes` JSON array.
- Each expression occupies its own labeled row, with Add pattern and Remove.
- At least one and at most 50 valid patterns are required; commas are preserved.
- Any pattern can match, against the complete hostname. Optional role/node/load
  balancer restrictions remain unchanged. Invalid patterns reject the whole rule.
- Editing populates every row; removing a pattern updates the same rule.
- Existing saved scalar patterns migrate to one-item arrays while retaining rule
  IDs, order and restrictions. Revision advances once to reject stale editors.
- The removed scalar API field is rejected.

Both Go suites, vet, diff checks and reproducible UI asset checks passed.
Focused tests cover OR matching, full-host matching, comma quantifiers, empty and
invalid expressions, rule edits, serialization and migration idempotency.

Chrome checks used synthetic data at desktop and 390px: creation with two rows,
array serialization, edit prepopulation, removal, focus restoration, disabling
removal of the last row, no horizontal overflow and no console errors. Temporary
screenshots were reviewed and deleted.

Locally prebuilt control artifact: `dev-policy-patterns-20261008`

Image digest:
`sha256:404cd300c3f9a44a53a484c47b322bb332064d745a6be757d0ef87a34014bc93`

No stable tag or GitHub release was created.

Live verification confirmed the deployed row editor and exact migration of saved
rules: IDs, order and restrictions were unchanged, with revision advancing to 5.
Assignments, encrypted credentials and all DNS ledger entries matched the baseline.
