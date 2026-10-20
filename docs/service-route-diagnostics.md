# Service-route diagnostics

Funnel emits structured `slog` events on stdout/stderr. These observations do not
retry requests, refresh identities, alter Service approval, or change DNS.

## Events to capture

| Event | Meaning and fields |
| --- | --- |
| `tailnet route dial failed` | First failed userspace dial and periodic summary. `target`, `network`, `error_class`, `dial_duration`, `failure_duration`, `failed_attempts`. |
| `tailnet route dial recovered` | A previously failing target connected again. Includes failure duration and attempt count. This means TCP connected, not that the HTTP application passed acceptance. |
| `tailnet route map sample` | Bounded asynchronous read from that process's identity: `target`, `device_id`, matching `candidates` with advertised `Allowed` and selected `Primary`, and `map_available`. No active connection or ping is issued by this sampler. |
| `tailnet Service listener opened` | Service/port/backend and embedded or host transport. Unchanged configuration polls produce no new listener event. |
| `tailnet Service listener withdrawn` | Assignment/backend and reason: `backend_changed`, `runtime_remove`, `configuration_withdrawal`, `lease_fenced`, or `shutdown`. Withdrawal alone does not establish hostname deletion intent. |
| `forwarder assignment fenced…` / `…lease reacquired…` | Component, controller, Kubernetes node UID and last renewal. Broker failure fences listeners while retaining the same process-owned identity. |
| `slow runtime configuration` | Control configuration duration, component and cancellation state. Correlate with broker/lease events to distinguish control delay from a missing data-plane route. |

Dial events are limited to an initial failure and one summary per minute per
network/target, plus recovery. A shared limiter permits four initial events and
one event per second thereafter. At most 128 failed targets and one two-second
map sample are retained/in flight per embedded connection wrapper. Under heavy
failure or rapid flapping some observations are deliberately dropped. Caller
cancellations, including losing dual-stack races, are ignored.

No raw netmaps, private keys, credential values, HTTP headers, bodies, request
URLs or arbitrary dial-error strings are logged by these events. Operational
Service names, target IPs, device IDs and selected backend addresses are logged.
A missing peer must fail without borrowing the host's network/identity.

## Collecting an incident

Native systemd component:

```sh
sudo journalctl -u tailscale-private-funnel-<deployment-id>.service \
  --since '30 minutes ago' --no-pager -o short-iso
```

Kubernetes agent and each affected forwarder:

```sh
kubectl --context <context> -n <namespace> logs <pod> --timestamps --since=30m
kubectl --context <context> -n <namespace> logs <pod> --previous --timestamps
```

Record the exact released binary version/image digest, timestamps, device IDs,
application Service name/VIP, route conditions and affected public hostname.
Collect both failed and recovered intervals. `Programmed=True` or Service API
approval is not proof that the load balancer received a usable VIP peer route.

Logs use the host/cluster's existing retention. This release adds no separate log
files or collection agent. Kubernetes container logs do not survive arbitrary
Pod deletion; use the installation's log collector for longer retention.

## Explicit diagnosis on the running edge

The load balancer provides a private `0600` Unix socket in its `0700` data
subdirectory. Use its service user or root, and the actual application's VIP/port:

```sh
sudo curl --unix-socket <data-dir>/diagnostics/service.sock \
  'http://localhost/service?ip=<tailvip>&port=<service-port>'
```

This explicit diagnostic additionally probes the existing identity's LocalAPI
dial plan and Disco path (direct, DERP or peer relay). It refuses kernel fallback.
It never starts a second tsnet identity. Unlike passive logging, this command
performs bounded active diagnostics; correlate its own timestamps with the failure.

## Verified policy prerequisite

The October investigation found that Service-only access omitted replacement
Service hosts from existing clients' peer maps. Use both installation-scoped
ICMP-only discovery grants in [Tailscale roles](TAILSCALE-ROLES.md):
load balancers to gateway devices, and runtime consumers to control devices.
These grants open no device TCP/UDP application ports. Funnel generates the
policy; an administrator reviews and applies it.

Check this prerequisite before pursuing transport or SDK recovery. An absent
peer precedes direct/DERP/peer-relay selection. Once policy is correct, correlate
map observations with Service approval, backend readiness, lease fencing and
cluster events. Do not replace identities, reset devices, or create diagnostic
hosts automatically as a routing repair.

The [peer-discovery acceptance](verification/service-peer-discovery-20261020.md),
[control replacement test](verification/control-peer-discovery-20261020.md), and
[released rollout](verification/released-v4.0.6-20261020.md) supersede the earlier
unresolved-policy hypothesis. Historical investigation notes retain their original
test observations; they are not current operational instructions.
