# tailscale-private-funnel operations

Local Funnel has one self-hosted executable with three explicit modes:

```text
tailscale-private-funnel control ...
tailscale-private-funnel loadbalancer ...
tailscale-private-funnel ingress ...
```

The control service is tailnet-only. It owns applications, Tailscale Services,
node enrollment, exposure policy, and the DNS ledger. A load balancer runs Caddy
for public TLS and uses the approved Service routes. An ingress supervisor is
the only Docker API client and starts one isolated raw TCP sidecar per approved
labelled backend assignment. Sidecars never mount the Docker socket.

Load balancer Caddy uses the load balancer process's already-open `tsnet.Server`
through a small native Caddy transport module. Route reloads reuse one
persistent Tailscale identity instead of starting a second `tsnet.Server` against
the same state directory. Ingress sidecars use their own identities and never
share the load balancer state.

## Tags and Service approval

The supported node tags are:

- `tag:tailscale-private-funnel`

Devices and generated Tailscale Services share this single tag. The tailnet policy can therefore grant access to the Service group without listing each `svc:` name.

Configure one grants rule in the tailnet policy:

```json
{"src":["tag:tailscale-private-funnel"],"dst":["tag:tailscale-private-funnel"],"ip":["*"]}
```

The control plane does not edit the tailnet policy. Its OAuth client needs only
`auth_keys`, `devices:core`, and `services`.

Control creates each application Service before an ingress sidecar starts. A
sidecar advertises it with `tsnet.ListenService`; control then verifies the
exact Service, device, enrollment, and shared Private Funnel tag before approving that
Service/device pair through the Tailscale Services API. The shared tag is
assigned to both devices and Services; no `autoApprovers.services` policy is
required.

## CLI

Control bootstrap is non-interactive and uses protected OAuth files. Generated
node commands are non-interactive:

```sh
tailscale-private-funnel control join --tailnet example.ts.net --oauth-client-id-file ./ts-client-id --oauth-client-secret-file ./ts-client-secret
tailscale-private-funnel control serve

tailscale-private-funnel loadbalancer join --management-url http://100.x.y.z:8080 ...
tailscale-private-funnel loadbalancer serve

tailscale-private-funnel ingress join --management-url http://100.x.y.z:8080 ...
tailscale-private-funnel ingress serve

tailscale-private-funnel ingress sidecar serve --service-name svc:... --backend-ip 172.20.0.4 --backend-port 8080
```

The UI produces the complete `join` command, release image, state/data paths,
labels, and systemd settings. State defaults follow XDG conventions:
`$XDG_STATE_HOME/tailscale-private-funnel/<mode>` and
`$XDG_DATA_HOME/tailscale-private-funnel/<mode>`.

The control endpoint returns a signed configuration envelope. Ingress verifies
it with the control public key from its generated command before accepting any
application assignment and acknowledges the configuration revision. Each
sidecar creates a persistent Ed25519 identity before requesting its short-lived
Tailscale auth key; control binds that key to the exact sidecar device before
Service approval.

## Docker exposure label

Use only the consolidated label. Whitespace around commas is accepted:

```yaml
labels:
  tailscale-private-funnel.expose: >-
    8080:checkout.dev.example.com:http,
    8443:checkout-admin.dev.example.com:http
```

Each specification is `<container-port>:<public-hostname>:http`. Invalid,
conflicting, generic `caddy.*`, or policy-denied values are stopped before a
sidecar is created. The supervisor selects every user-defined network attached
to the target container, excludes `host`, `none`, and locally denied patterns,
then attaches the sidecar to those networks. Different backend ports get
separate sidecars and Services; no HTTP host/path dispatcher runs in a sidecar.

## DNS and ownership

The control service writes only its exact ownership ID. Public DNS contains the
owned load balancer A record and public-hostname CNAMEs to that load balancer.
Tailscale Service discovery stays private; no public SRV or backend A record is
created. Missing or ambiguous load balancer endpoints retain the previous owned
A record. Unowned records require an explicit administrator overwrite approval.
Expired or revoked reports remove only records in the corresponding ledger.

## Runtime data and rollback

Persist the control SQLite directory, each node's tsnet state, Caddy storage,
and the ingress sidecar state/data directories. Pull immutable release images
and binaries; never compile on the VPS. Roll back by stopping the current unit,
restoring the previous image or binary tag, and restoring the control database
backup if required.
