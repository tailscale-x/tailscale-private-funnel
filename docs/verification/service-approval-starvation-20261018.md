# Authenticated Tailscale API timeout investigation

## Live evidence

DLKN control is Ready on v3.2.2, while Gateway programming waits for Service
approval. Basic outbound HTTPS was independently verified by the cluster task.
An additional probe in the **current control Pod's network namespace**, using
Funnel's available OAuth credentials, returned:

- Service-host list: HTTP 200, 0.716 seconds.
- Load-balancer device details: HTTP 200, 0.666 seconds.

The token was held in memory and passed to curl through stdin; it was not put
in arguments, Kubernetes resources, files, or diagnostic output. The temporary
diagnostic container has a five-minute sleep lifetime and holds no credentials.

Control logs show a load-balancer DNS timeout immediately followed by a batch
of Service-list context-deadline errors. The sequential maintenance loop used
one 45-second context for policy/DNS, report expiry, stale-node cleanup,
load-balancer DNS, and finally Service approval. Policy reconciliation also
attempted Service approval after its own DNS work. Earlier work can exhaust
that shared context before the authenticated Service calls start; the resulting
URL-bearing errors do not establish that the remote endpoint is unreachable.

## Local correction

- Give periodic Service approval its own serialized loop and fresh 45-second
  context, independently of slow DNS/provider work.
- Prevent concurrent policy and periodic Service passes from queuing behind
  each other while their deadlines expire.
- Reject already-expired contexts before loading credentials or issuing calls;
  stop iterating Service records once the context expires.
- Stop subsequent maintenance phases when their pass budget is exhausted.

Regression coverage verifies that blocked DNS cannot prevent repeated Service
approval with live contexts, canceled contexts perform no credential/API work,
and overlapping Service passes do not queue. Existing policy/ownership and
Service authorization checks are retained.

This correction is **not deployed or released**. No stale Service/device records
were deleted, no ACL/network settings were changed, and no control restart was
performed by this investigation. Cluster rollout remains a separate operation.
