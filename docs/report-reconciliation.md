# Report acceptance and external reconciliation

Runtime exposure reports and health heartbeats acknowledge durable local storage,
not completion of external API work. Report handling verifies WhoIs binding,
mode, signing key, signature, timestamp, nonce, policy, and application assignment.
It stores the accepted report and backend snapshot before responding. Replay and
revocation checks remain enforced by storage transactions.

Report serialization covers local state only. DNS reconciliation, Tailscale
endpoint discovery, and Service-host approvals must never run inside a report or
health request, or hold a lock needed by those requests. Otherwise an external
API outage can consume report freshness and incorrectly withdraw healthy routes.

Control uses bounded, single-flight periodic workers (15-second ticks, fresh
45-second pass budgets) for policy/DNS reconciliation, expiry, endpoint discovery,
revoked-node cleanup, and Service approvals. Persisted desired state is the retry
queue: no goroutine is spawned per heartbeat and no report waits for a provider.
Existing Docker label resolution reads an unchanged, authorized application binding
without acquiring the Service provisioning lock. Routine Kubernetes snapshots
reuse stored, control-owned Service targets rather than issuing an unchanged REST
mutation for every backend. New Service creation remains an external operation.

A worker which exhausts its context leaves pending cleanup for a later pass.
Workers recheck an expired context after acquiring mutation locks.

The five-minute report freshness window is unchanged. Expiry rechecks the report
or node heartbeat timestamp transactionally before deleting a report and revoking
backend assignments. A heartbeat arriving after the initial stale-candidate scan
therefore survives cleanup. Persistent device identity is not revoked by expiry.

During external API outages, existing healthy forwarding continues using fresh,
authenticated reports and the last owned load-balancer address. New enrollment,
Service approvals, DNS additions, and externally applied cleanup can remain
pending. An actual report expiry, revoked identity, or denied exposure policy
still withdraws the affected route. This is not a bypass of authorization and
is not protection against an unavailable control database or tailnet transport.

## Verification

Tests hold the external mutation lock while submitting reports/health for all
node modes, exercise a stalled Tailscale transport, preserve replay/revocation
checks, verify existing label resolution while provisioning is locked, verify unchanged
Kubernetes snapshots with a stalled REST transport and untrusted supplied Service
targets, verify worker independence, and cover renewal-versus-expiry ordering
through the shared SQLite/PostgreSQL storage contract.

For live fault verification, use a temporary CONNECT proxy that stalls only
`api.tailscale.com:443` and `api.cloudflare.com:443`. Do not block Tailscale's
control-plane/DERP transport, interfere with backend networking, intercept TLS,
or log credentials. Run the fault for longer than the report freshness window;
verify public HTTPS, fresh reports, stable node bindings and owned DNS, then
release the fault and remove the proxy configuration/workloads.
