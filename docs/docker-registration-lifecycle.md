# Docker registration lifecycle

Docker gateways send signed, complete declaration inventories before backend
inspection and Tailscale Service provisioning. The inventory includes stopped
containers and only Funnel forwarding labels; unrelated Docker labels are not
sent. Persisted revision counters and the existing WhoIs/signature/nonce checks
prevent old or foreign inventories from releasing ownership.

- Removing a container or its forwarding declaration retires its application
  registrations after the next successful scan (container events plus a
  ten-second polling fallback).
- Stopping a container retains its registration. Backend unavailability may
  withdraw routing, but it does not release the hostname to another owner.
- A replacement declaring the same hostname, port, and route keeps ownership.
- Failed Docker scans, invalid labels, network/probe failures, and offline
  gateways are not proof of removal. They cannot retire registrations.
- Removing one prefix preserves a hostname still used by another route. Aliases
  follow their source application's declaration.

Control hides retired registrations from active application/exposure inventory
and retains their history. Backend assignments are withdrawn locally; DNS and
unused Service cleanup run asynchronously. Shared Services, other applications,
and the gateway's persistent Tailscale identity remain intact. An old signed
report containing a known retired entry cannot starve unrelated applications on
the same gateway.

## Reclaiming a hostname from Kubernetes

There is no special transfer operation. After confirmed Docker removal, a
policy-authorized Kubernetes gateway can claim the hostname through its normal
signed snapshot. A competing registration still blocks the claim. The existing
exposure ID is reused and its `exposure:<id>` DNS ownership remains the same;
registration retirement does not rewrite the DNS ledger directly.

Ordinary cleanup removes unused owned public DNS. Kubernetes publishes it again
only after authorized backend readiness, Service approval, and load-balancer
acknowledgment. This is not a zero-downtime cutover or a DNS reservation: there can
be an unavailable interval while destination consumers are stopped or not ready,
and recursive DNS caches may extend that interval. Configure and verify the
Kubernetes workloads separately; Funnel does not start application consumers or
enable their business jobs.

## Upgrade and diagnostics

Update control first, then the native Docker gateway. Existing identities and
state directories must be retained, including `docker-inventory-revision` in the
gateway data directory. Missing inventory support never causes control to infer
removal from an empty exposure report. Revision corruption fails closed rather
than silently resetting the counter.

The generated API reference documents `NodeService.SubmitDockerInventory` and its
signed payload. Audit events `docker.application.retire:<id>` record confirmed
retirement; `docker.service.cleanup:<service>` records completed external cleanup.
External API failure leaves cleanup pending for a later bounded pass. No ACL
changes, node revocation, manual DNS edits, or control database edits are needed.
