# Temporary HTTPRoute lifecycle fix

Scope: preserve the existing Harbor catch-all when adding or removing a
cert-manager-shaped exact challenge route on the same hostname. This change is
prepared locally; it does not deploy or repeat the disruptive live probe.

## Findings

The incident report records endpoint readiness at 13:13:32 UTC, successful public
solver forwarding at 13:14:41, then transient Harbor 404s after solver deletion.
It does not measure a continuous outage or establish its exact duration.

A local control regression reproduces a concrete failure: an application stores
all rules for one hostname, but continuity compares the complete rule set and its
arbitrary primary backend. Adding/removing a solver changes both and fails that
comparison. Until fresh readiness reports catch up, the next edge configuration
can omit the entire hostname, including its unchanged Harbor catch-all.

Two status defects are independently visible in source:

- Translation sorts rules by match precedence without moving the parallel resource
  IDs. An exact solver can be associated with the catch-all resource ID.
- Publication compares a hostname's entire application against the latest *sent*
  configuration and its acknowledgment. Polling can supersede that configuration
  before acknowledgment, and hostname acknowledgment cannot identify which
  resource's rules were installed.

Readiness also probes only the primary backend chosen by sorted Service keys.
That is insufficient for a hostname containing multiple independent backends.
Observed forwarding latency includes the controller's 15-second status requeue,
forwarders' 10-second poll, and the edge's 30-second route/probe poll. Previously,
sequential five-second backend probes could add further delay. These intervals
explain possible latency; no exact attribution of the live 69 seconds is claimed.

## Change

- Retain unchanged Kubernetes rules independently while still applying current
  application membership, gateway authorization, hostname policy and revocation.
  Removed or changed rules are not retained. New rules wait for all their backend
  Services to be reachable from that edge; weighted backends stay one readiness unit.
- Probe every Kubernetes backend with bounded concurrency and authenticate each
  backend report against its control-owned application binding. This does not
  create a new Tailscale identity or bypass Service access/approval.
- Keep resource IDs paired with sorted rules. Include resource generation in the
  publication identity, with a digest of its effective rules across hostnames.
  Pod endpoints and control-assigned Service addresses are excluded from that
  digest, while Service UID/port, weights and HTTP semantics remain included.
- Persist the edge's acknowledged route snapshot separately from its latest sent
  configuration. Serialize sending and acknowledgment with component-scoped locks;
  reject an acknowledgment of a superseded revision. Status remains conservative
  until the next successful apply/ack, without withdrawing unchanged rules.
- Publish per-resource revisions through the typed runtime contract. Harbor can
  remain Programmed while its solver is pending. Neither sending configuration nor
  acknowledging an earlier resource generation marks the solver Programmed.

## Regression coverage

- Pending solver insertion even when its Service key becomes the primary.
- Solver readiness followed by edge acknowledgment, not hostname-only readiness.
- Pod endpoint replacement/gaps, route removal and subsequent empty-host withdrawal.
- Forged backend reports, stale acknowledgments and explicit policy denial.
- Resource/rule sorting, sibling status independence and resource generation changes.
- Real local Caddy forwarding with concurrent Harbor probes across pending/ready
  solver configurations, backend replacement, route deletion and namespace-deletion
  equivalent configurations. This uses local backends, not real Tailscale or ACME.

A coordinated live acceptance remains necessary after a reviewed deployment:
continuous Harbor health and registry probes during route/Pod/namespace lifecycle,
then full cert-manager staging issuance/renewal with split-DNS self-checks. No live
routes, DNS, policies, identities or services were mutated for this investigation.

Validation: the initial control regression failed on the released baseline with
an empty route list at solver insertion; the patched control regression passes.
The ordinary Go suite, vet, schema lint and UI typecheck passed. Targeted lifecycle
regressions pass with the race detector. The real Caddy lifecycle test passed with
51 concurrent successful Harbor requests in the first run. The host disables IPv6
loopback, so the existing IPv6 proxy tests require an isolated network namespace
with IPv6 loopback enabled; that proxy suite passes there. No host networking
settings were changed.
