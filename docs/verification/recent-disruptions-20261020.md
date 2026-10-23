# Recent routing disruptions — 20 October 2026

Read-only investigation, captured approximately **14:42–14:46 ICT (UTC+7)**.
No service restart, rollout, identity replacement, ACL change, DNS mutation or
application change was performed. Raw logs and probe results are private under
`/tmp/funnel-incident-check-20261020/`; request headers/bodies are not reproduced
here. Sparse request failures establish observed events, not exact outage lengths.

## Luong Gia is in DLKN

Both `luonggia.troly.ai.vn` and `sso.luonggia.troly.ai.vn` use HTTPRoutes in
`luonggialogistics`, referencing `dlkn-public` and the DLKN Funnel gateway.
They are not attributed to the m090 gateway incidents below.

- **14:34:50–14:37:35:** new Explorer and SSO application Pods were created and
  started across DLKN nodes. Old Explorer Pods were stopped at 14:37:04 and
  14:37:40. One new Explorer Pod initially waited for anti-affinity scheduling;
  another pulled its image for roughly two minutes. These are application
  rollout events, not Funnel Pod restarts.
- **14:38:07 and 14:38:26:** the public SSO edge returned 502 with
  `Tailscale peer route pending` for VIP `100.81.89.210`.
- **14:38:27 and 14:38:50:** Explorer returned the same error for VIP
  `100.76.157.46`.
- **14:39:46:** SSO also logged a timed-out Service connection. Its recorded
  duration was 127 seconds; the event timestamp is completion, not request start.
- The native edge PID/start time and all DLKN Funnel Pod UIDs/restart counts
  remained unchanged throughout this period.
- **14:42 snapshot:** both HTTPRoutes were Accepted/ResolvedRefs but
  Programmed=False/Pending because control `SubmitKubernetes` timed out.
  Control logs separately show configuration handlers taking 16–30 seconds.
- **14:45:** both Kubernetes Service `/health/ready` checks returned
  `{"status":"ready"}`. Public readiness passed three consecutive checks for
  each hostname. The existing edge selected a primary DLKN node-1 host for SSO,
  successfully connected and reported direct UDP transport.

The chronology is consistent with application endpoint/Service-advertisement
changes plus slow control convergence. It does **not** prove the exact failed
map transition: the live r6 edge still has the redundant Status peer precheck,
and the new v4.0.3 passive logging/single authoritative route check is not yet
installed. A `peer route pending` message proves rejection by that older check;
it alone cannot distinguish a stale Status snapshot from an absent SDK route.
No DNS lookup failure was observed in these Luong Gia checks.

## Other confirmed interruptions

| Time (ICT) | Finding |
| --- | --- |
| 13:02:15–13:02:41 | DLKN forwarders fenced listeners after broker lease renewals timed out, then reacquired the same bindings. No Pod/device restart was required. Fencing can interrupt forwarding even without identity turnover. |
| Around 13:03 | m090dev forwarders restarted. Previous-container logs show lease renewal timeout, runtime cancellation and `forwarder stopped or lease lost`; these still run the older continuity-r1 image. |
| Around 14:10 | m090prd forwarders likewise exited after lease renewal timeouts and restarted on the older image. This is independent of Luong Gia's DLKN ownership. |
| From 11:01:48 | 5090 k3s was stopped cleanly; it remains inactive and port 6443 is not listening. Voice currently returns 502 and its Service VIP has no matching peer on the edge. The stop's actor/intention was not established; nothing was restarted. |

## Public checks and deployment state

- Admissions API, public site, SSO, Eoffice and Metube passed the initial verified
  HTTPS checks. Harbor passed initially, had one client connection timeout, then
  passed all three confirmation checks. That one frontend timeout is not
  sufficient evidence of another Service-route outage.
- Explorer, Luong Gia SSO, Harbor and admissions API passed all 12 confirmation
  requests at approximately 14:45:43–14:45:56 ICT.
- Voice failed both public checks. Its missing Service peer was verified using
  the existing edge's private diagnostic socket, without replacing an identity.
- Native edge remains `dev-service-route-20261020-r6`, PID 170782, started
  08:54:12 ICT, zero systemd automatic restarts. Caddy admin reload messages are
  not evidence of process restarts.
- DLKN gateway remains r8; control and m090 gateways remain continuity-r1.
  Published v4.0.3 has not been deployed by this investigation.

The observations confirm real interruptions without a new Funnel deployment.
Application endpoint movement, assignment fencing and older lease-driven process
exits are separate mechanisms. Control-handler latency remains an additional
unresolved contributor; this log review does not establish its underlying cause.
