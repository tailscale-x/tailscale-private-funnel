# Report acceptance and external reconciliation

Runtime exposure reports and health heartbeats acknowledge durable local storage,
not completion of external API work. Report handling verifies WhoIs binding,
installation/role, active instance lease, policy, and application assignment.
It stores the accepted report and backend snapshot before responding. Inventory ordering, instance ownership and disabled-component checks remain
enforced by storage transactions. Receipt freshness is recorded by control.

Report serialization covers local state only. DNS reconciliation, Tailscale
endpoint discovery, and Service-host readiness must never run inside a report or
health request, or hold a lock needed by those requests. Otherwise an external
API outage can consume report freshness and incorrectly withdraw healthy routes.

Control uses bounded, single-flight periodic workers (15-second ticks, fresh
45-second pass budgets) for policy/DNS reconciliation, expiry, endpoint discovery,
revoked-node cleanup, and Service readiness. Persisted desired state is the retry
queue: no goroutine is spawned per heartbeat and no report waits for a provider.
Existing Docker label resolution reads an unchanged, authorized application binding
without acquiring the Service provisioning lock. Routine Kubernetes snapshots
reuse stored, control-owned Service targets rather than issuing an unchanged REST
mutation for every backend. New Service creation remains an external operation.

A worker which exhausts its context leaves pending cleanup for a later pass.
Workers recheck an expired context after acquiring mutation locks.

The five-minute report freshness window describes observed health. Expiry rechecks
freshness transactionally and discards stale reports, without revoking backend
assignments, node identities, DNS ownership, or previously ready routes. Explicit
inventory/configuration removal and revocation remain separate operations.

During external API outages, new Service readiness and DNS additions can remain
pending. Previously validated routing continues while its durable configuration
and exposure policy remain unchanged and authorized.

## DNS continuity and configuration requests

Existing owned DNS is retained through missing, expired, empty, and unreachable
health reports while the configured exposure remains authorized. Retention does
not create records or authorize unowned overwrites. Authoritative Docker
inventory removal, application/hostname removal, policy denial, disabling an
application, and component revocation still withdraw the affected records.
Stopped containers retain registration. Load-balancer revocation removes only its
address contribution. Missing health or ambiguous endpoints preserve its owned A.

Previously ready load-balancer routes survive health loss and control restart.
Each response rechecks current application membership, policy, managed zone,
gateway revocation, and exact route definition. Changed/new backends require
fresh readiness; a saved route is not a way to introduce a new backend.
Authoritative empty configurations replace the saved route set, preventing
withdrawn routes from reappearing. The load balancer also atomically saves its
successfully applied routes locally, bound to its component, installation and
control URL, for restart during a control outage. Startup loads that saved configuration before
attempting a control RPC; revalidation follows in the refresh loop.

Kubernetes forwarders retain listeners if control configuration/report delivery
fails. Their independent broker assignment lease still fences competing Pods;
losing that lease stops forwarding. At 40 seconds without successful renewal,
listeners are fenced before the broker's 60-second lease expires. The process
keeps its existing tsnet identity and retries broker assignment acquisition;
it does not exit, log out, or mint another device for a broker outage. Listeners
can reopen only after the same controller, logical node, Kubernetes Node UID,
node name, and control URL binding is reacquired. Readiness stays false while
fenced. Ordinary Pod deletion still retires its disposable identity.

Broker endpoint readiness reflects its ability to authenticate Pods and manage
leases, independently of the controller's Tailscale registration. New sessions
still wait for registration. Existing holders keep renewing through a controller
startup or a stalled configuration fetch. Cold configuration fetches have a
20-second budget; a valid warm cache keeps its two-second fallback and original
five-minute expiry, rechecked after fetch completes. Configuration HTTP has its own 30-second budget; initial
lease acquisition remains at 10 seconds and renew/reacquire calls at five.

Unchanged Service/port/backend assignments retain the same listener across
configuration polls. Backend pools update their endpoint sets in place. Changed
backends, authoritative removal, or lease fencing withdraw the affected
listeners; a control RPC failure alone does not. Backend failure is not made
healthy by retention: a request may fail even though its hostname and route
still exist.

Disconnected nodes cannot receive new control-policy revocations until they
reconnect. Tailscale network policy remains independently enforced. Administrators
requiring immediate isolation during disconnection must use Tailscale grants or
stop the component; report freshness alone is no longer a revocation mechanism.
Disposable Kubernetes identities remain disposable; no network identity is made
persistent by this change.

Configuration handlers authenticate and renew the caller on the live store, then
use one context-bound metadata view per request. Failed or canceled reads return
an error without replacing the last sent configuration. Authorization changes
are read again on the next request; no cache crosses requests. Requests taking
three seconds or more log their duration and cancellation state.

Disposable embedded Kubernetes identities explicitly log out before their tsnet
transport closes. Persistent native and borrowed host identities are never logged
out by ordinary shutdown. Failure to log out is bounded and reported; transport
closure still completes. No additional OAuth scope or ACL grant is needed.

## Verification

Tests hold the external mutation lock while submitting reports/health for all
node modes, exercise a stalled Tailscale transport, preserve inventory ordering and instance/disable
checks, verify existing label resolution while provisioning is locked, verify unchanged
Kubernetes snapshots with a stalled REST transport and untrusted supplied Service
targets, verify worker independence, and cover renewal-versus-expiry ordering
through the shared SQLite/PostgreSQL storage contract.

Run fault verification in a disposable installation. Stall external APIs and
control connectivity beyond the report window, including fresh negative/empty
reports; assert stable DNS ledger ownership and previously ready routes while
new routes stay pending. Verify explicit deletion, policy denial, assignment
fencing, and recovery separately. Do not inject faults into shared live routes.

For the outage investigation, artifact history, consolidation decisions and
remaining transport boundary, see the [Service-route review](verification/service-route-consolidation-20261020.md).

See [Service-route diagnostics](service-route-diagnostics.md) for bounded runtime events and log collection.

## Kubernetes hostname conflicts and removed gateways

A valid Kubernetes snapshot is processed per hostname. A hostname already owned
by another gateway or backend source is excluded from the incoming forwarding
configuration; its owner and DNS ledger are untouched. Other authorized hostnames
in the same snapshot still reconcile. Control returns `kubernetes_errors` keyed
by Kubernetes resource ID and hostname. The gateway reports `Programmed=False`
with the conflict on the affected route, while unrelated routes can remain
`Programmed=True`. Malformed or unauthenticated snapshots are still rejected.

Uninstalling k3s or deleting an entire cluster cannot notify control. An offline
node, expired report, missing Pod, or negative health does **not** release a
hostname claim. This prevents transient failures from becoming ownership changes.
When a logical Kubernetes gateway has been deliberately removed, an authorized
administrator must explicitly disable it:

```sh
tailscale-private-funnel manage nodes disable --id <removed-gateway-id> \
  --control-url http://<installation-control-service>:8080
```

Inspect the result. If cleanup remains pending, resolve its diagnostic and retry:

```sh
tailscale-private-funnel manage nodes cleanup --id <removed-gateway-id> --yes \
  --control-url http://<installation-control-service>:8080
```

Cleanup revokes only that logical gateway and its registered child forwarders,
withdraws its exclusive applications, and removes only its owned DNS records.
It deletes only control-created Kubernetes Services with no references from
other applications and no hosts outside the revoked gateway. Shared applications
and Services remain protected. Claims are released transactionally **last**, after
owned cleanup succeeds. Failed cleanup retains claims and history for retry;
a disabled application alone is not a released hostname. A successful retry is
idempotent. Application records, retirement history, and exposure IDs are retained. Completed cleanup returns its recorded success without repeating external deletion. Gateway cleanup reconciles only its assigned exposures, so an unavailable unrelated zone cannot block retirement.

After completion, another policy-authorized gateway may claim the hostname through
normal reporting and readiness checks. This is cleanup followed by fresh
provisioning, not an implicit transfer or an ownership bypass. No SQL edits,
unowned-record deletion, or Tailscale ACL changes are required.
