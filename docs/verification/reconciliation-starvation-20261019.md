# Reconciliation starvation and PostgreSQL write pressure

## Diagnosis

Two active Luong Gia exposures had no DNS ledger entries despite programmed
Kubernetes routes. This was pre-publication failure, not negative DNS caching.
The running control was v4.0.2. Read-only PostgreSQL samples observed its sole
connection waiting on `WalSync` in component lease renewal; other samples were
idle. This establishes contention, not a complete attribution of database I/O.

Every runtime request renewed a one-minute lease with a durable write. Exposure
readiness repeatedly loaded the same applications, reports, nodes, settings, and
zone metadata for each hostname. Store operations and provider-lock waits did
not honor the caller's context, so an expired pass could finally reach a provider
and misleadingly report that provider's request deadline. Unchanged DNS also
rewrote the ledger and appended audit events on every pass.

## Changes

- Authenticate every runtime request, but renew the verified holder's lease at
  most every fifteen seconds. Wrong/replaced devices and explicit release remain
  rejected; ordinary control-outage recovery is retained.
- Memoize routing metadata only within one readiness operation. Provider inventory
  and mutation-time ownership ledgers remain fresh; no shared authorization cache
  is introduced. Read errors prevent destructive readiness decisions.
- Propagate request context through database queries, connection waits and
  transactions. Provider-lock waits are cancellable.
- Continue a deadline-exhausted reconciliation from the next exposure, so earlier
  hostnames cannot indefinitely starve later ones.
- Skip ledger/audit rewrites for a verified no-op and skip unchanged nonsecret
  settings writes. Continue live provider inspection and exact ownership checks.
- After successful provider mutation, finish the ownership commit under a separate
  bounded context instead of abandoning it with an expired HTTP deadline.

SQLite and PostgreSQL retain their single-connection serialization. No credentials,
identities, ownership identifiers, schema, public routing contract or ACL changes
are required. The two Luong Gia exposure IDs remain `tc1cbUikRhFuR94qlAyPuQ` and
`iTr-D1qoHVTY1s5JKKa6gA`.

## Verification

Regression tests cover lease write coalescing and rejected holders, bounded pool
and provider-lock waits, request-local read caching, canceled reads preserving
owned records, fair progress after a stalled provider, no-op audit suppression,
and a successful DNS mutation retaining its ledger despite request cancellation.
Disposable PostgreSQL 16 and 18 storage contracts pass. Both Go suites and vet
are required before the development control rollout.
