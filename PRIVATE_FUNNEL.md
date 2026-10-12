# tailscale-private-funnel operations

Local Funnel has one self-hosted executable with three explicit modes:

```text
tailscale-private-funnel control ...
tailscale-private-funnel loadbalancer ...
tailscale-private-funnel ingress ...
```

The control service is tailnet-only. It owns applications, Docker-backed Tailscale Services,
node enrollment, exposure policy, and the DNS ledger. A load balancer runs Caddy
for public TLS. Docker routes use approved Services on ingress; tailnet-machine routes dial the selected backend device directly from the load balancer. An ingress supervisor is
the only Docker API client and forwards each approved labelled backend assignment directly to its
selected Docker container address. No relay sidecars are started.

Load balancer Caddy uses the load balancer process's already-open `tsnet.Server`
through a small native Caddy transport module. Route reloads reuse one
persistent Tailscale identity instead of starting a second `tsnet.Server` against
the same state directory. One persistent ingress identity owns Docker Services and forwards to Docker containers without another Tailscale identity.

## Tags and Service approval

The supported node tags are:

- `tag:tailscale-private-funnel`

Devices and generated Tailscale Services share this single tag. The tailnet policy can therefore grant access to the Service group without listing each `svc:` name.

Configure one grants rule in the tailnet policy:

```json
{"src":["tag:tailscale-private-funnel"],"dst":["tag:tailscale-private-funnel"],"ip":["*"]}
```

The control plane does not edit the tailnet policy. Its OAuth client needs
`auth_keys`, `devices:core`, `services`, and **`users:read`**. Device and Service
permissions use `tag:tailscale-private-funnel`.

`users:read` is required to verify Tailscale Owner/Admin roles for control UI
administrator access. It is read-only; the broader `users` write scope is not
needed. WhoIs identifies the caller but does not supply their account role.
Existing installations must add `users:read` to their control OAuth client.
Use a user-owned Tailscale device to access the UI with your account role:
tagged service devices do not inherit human Owner/Admin permissions.

Control creates each application Service before ingress advertises it. The
ingress node advertises it with `tsnet.ListenService`; control then verifies the
exact Service, ingress device, enrollment, and shared Private Funnel tag before approving that
Service/device pair through the Tailscale Services API. The shared tag is
assigned to both devices and Services; no `autoApprovers.services` policy is
required.

## Control and node settings

The Tailscale settings page displays the current identity without pretending to
reconnect it. OAuth changes are tested against Devices Read and `users:read`
before saving; **Test only** never replaces stored credentials. Blank replacement
fields preserve encrypted secrets.

Enrollment defaults choose the management URL and a published binary release for
new nodes. Existing services are not upgraded or reconfigured by this form. Each
node enrollment selects its load-balancer hostname and the applicable certificate
email or local Docker label/network options. Generated scripts use those values.

Load balancer **Inspect settings** shows public IPv4 endpoints fetched by control
from the enrolled Tailscale device. There is no manual override or node-reported
IP source. Exactly one distinct public IPv4 is required; missing or ambiguous
endpoints and API failures retain the previously owned A record while the node
health report is fresh. Expired or revoked nodes still trigger owned-record cleanup.
Local certificate and Docker deployment changes require updating and restarting
the node service or creating a replacement enrollment.

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

```

The UI produces the complete `join` command, release image, state/data paths,
labels, and systemd settings. State defaults follow XDG conventions:
`$XDG_STATE_HOME/tailscale-private-funnel/<mode>` and
`$XDG_DATA_HOME/tailscale-private-funnel/<mode>`.

The control endpoint returns a signed configuration envelope. Ingress verifies
it with the control public key from its generated command before accepting any
application assignment and acknowledges the configuration revision. Each
ingress node signs its reports with a persistent Ed25519 key. Docker application containers receive no Tailscale auth key or signing key.

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
Service listener is created. The supervisor selects user-defined networks
attached to the target container and excludes `host`, `none`, and locally denied patterns,
then probes the selected container address and port from the Docker host.
Different backend ports get separate Service listeners, with no HTTP host/path
dispatcher. No backend host port is published or relay container created.

## DNS and ownership

The control service changes only records bound to enrolled nodes in its internal
ownership ledger. Public DNS contains the
owned load balancer A record and per-exposure A records for all eligible load balancers. Each exposure defaults to all active edges; an administrator can restrict a hostname to selected edge IDs. Policy and fresh backend/edge health are enforced per edge.
Tailscale Service discovery stays private; no public SRV or backend A record is
created. Missing or ambiguous load balancer endpoints retain the previous owned
A record. Unowned records require an explicit administrator overwrite approval.
Expired or revoked reports remove only records in the corresponding ledger.

## Runtime data and rollback

Persist the control SQLite directory, each node's tsnet state, Caddy storage,
and the ingress report signing key. Pull immutable release images
and binaries; never compile on the VPS. Roll back by stopping the current unit,
restoring the previous image or binary tag, and restoring the control database
backup if required.

## Manage policy rules and additional domains

Open **Exposure policy** to create a rule, edit it in place, toggle its enabled
state, or delete it. The confirmation lists currently authorized exposures that
will lose their last matching rule. Another enabled matching rule preserves
access. **Save policy** imports JSON; **Validate only** does not save it. Revision
checks reject stale browser edits. Policy saves immediately recompute public
routes and start owned DNS cleanup; the status panel reports pending cleanup or
provider errors. Ingress and load balancer configuration polling can delay
listener/route withdrawal, and DNS caches can outlive record removal.

Configure a **DNS provider** once and manage multiple existing domains. Use
**Edit credentials** to edit it inline; blank secrets preserve their current values.
Open **Manage zones**, then **Add zone** to search and select accessible domains
in the dialog. Discovery makes no changes; **Add selected zones** creates assignments only after fresh discovery and a read-access check. Cloudflare, GoDaddy, and Route 53 support discovery; Route 53 lists public hosted zones and honors a configured hosted-zone restriction. Configure missing domains with your DNS vendor first. Internal DNS
ownership is derived from enrolled nodes; no ownership ID needs configuring. Cloudflare tokens need **Zone Read**
and **DNS Edit** on every selected zone. Adding a zone does not register a domain
or change its nameservers. Blank replacement credential fields retain existing
secrets; credentials are never rendered back to the browser.

Hostnames resolve to the most specific managed zone with an enabled
DNS provider. Equally specific assignments are rejected. Aliases in multiple
zones can point to the same load balancer; its A record stays in its own zone.
Preview/apply requests require `node_id`, `provider_id`, and an explicit `zone_id`.
The control derives record ownership and exposure mode from the stored node.
Unknown nodes are rejected; revoked nodes may only clean up empty desired state. DNS work and cleanup are
partitioned by provider, zone, and exact owner; one failing zone does not stop
independent zones. Zones have no editing or enabled state. To change providers, withdraw exposures and use **Zones → DNS details** to confirm owned-record cleanup, remove the assignment, then select the domain from the new provider. Removal is blocked while ledger entries exist.
Unowned overwrites require an administrator's approval of a specific preview.
Viewers and operators cannot change policy, DNS providers, or zone scope.


Exposure-rule node and load-balancer restrictions are optional. Leave both roles
and ingress node IDs blank to allow any enrolled ingress whose hostname matches
the rule. When either is specified, a matching role or node ID is required.
Leave load-balancer hostnames blank to allow any load balancer. An empty policy
still denies all exposures.

Advanced policy JSON is displayed with indentation, formats valid pasted
documents, and offers **Format JSON** without saving. **Validate only** also
makes no policy changes.

Each exposure rule supports multiple **Hostname patterns**, entered as individual
rows with **Add pattern** and **Remove**. Any one pattern may authorize a hostname;
every pattern matches the complete hostname. JSON uses an array, for example:

```json
"hostname_regexes": [
  "api[.]example[.]com",
  "app[0-9]{1,3}[.]example[.]net"
]
```

Commas are part of the regex, never separators. At least one pattern is required,
and all patterns must be valid before a rule can be saved.

## Expose another tailnet machine

Choose **Expose a tailnet machine** in Exposures. Select a load balancer and a backend from its signed peer snapshot, enter the
HTTP port and public hostname, check policy, and run the backend connection test.
Saving creates a direct route and internal application without a Tailscale Service.
Edit, disable, or delete the exposure there; DNS cleanup remains ownership-scoped.

Ingress always discovers Docker containers and needs Docker socket access. Direct
tailnet exposures do not use ingress.

Additional tags and access grants are configured externally in Tailscale. The
bootstrap key still requests only `tag:tailscale-private-funnel`; control never
modifies ACLs or requests access to unrelated devices through OAuth. Peer visibility
is not proof of port access. The connection test runs through the selected
persistent load-balancer identity and its tsnet dialer, not the control host's network.
Selections bind to the stable Tailscale device ID and stop when that peer vanishes.

Docker and tailnet assignments share one ingress identity and are reconciled as
one listener/report set. Docker uses selected container addresses directly;
tailnet assignments use tsnet directly. Caddy remains the only HTTP proxy:
it removes incoming `Forwarded`, `X-Forwarded-*`, and `X-Real-IP`, preserves the
public Host, and generates trusted forwarding headers from the public connection.

## Display names and resource IDs

Names are editable labels, not reference keys. DNS providers, enrolled nodes,
enrollments, applications, and policy rules retain immutable internal IDs when
renamed. Duplicate display names are allowed; selectors show operational context
and short IDs to distinguish them. DNS domains, Tailscale hostnames, tags, and
Service names remain operational values. Renaming does not change them.

Create DNS providers with `POST /api/providers` using `name` and `provider`
(the vendor), plus credential fields. The response supplies the generated `id`.
Edit using `PATCH /api/providers/{id}`; blank secret fields retain stored values.
Zone creation selects a freshly discovered domain using `{provider_id, zone}`
and returns an immutable assignment ID. Zone testing/removal address that ID.
DNS previews and applies use `zone_id`, never a display name or domain as a
resource reference. Ingress enrollment and policy restrictions use
immutable resource IDs. Ingress enrollment has no load-balancer assignment. Exposure overrides use `loadbalancer_ids` (null means all; a nonempty array selects edges). `--loadbalancer-hostname` is only a load-balancer DNS value.

Existing provider references migrate transactionally. Encrypted credentials,
node identities, original signed report envelopes, and DNS ownership values are
preserved. The control keeps a normalized report projection for reconciliation;
the reports API returns the original signed envelopes for inspection. Overwrite
approvals made before the ID migration require a fresh preview.


### Zone DNS status and recovery

DNS records are generated automatically from enrolled load balancers and fresh,
policy-authorized ingress reports. Open **Zones → DNS details** to inspect the
ownership ledger and last successful sync. **Preview changes** compares the
expected records with the DNS provider without mutating DNS. **Retry sync**
recalculates expected records server-side and applies ownership-scoped changes.
Ownership conflicts require administrator approval tied to the exact preview;
a changed preview cannot authorize a stale overwrite. Endpoint lookup failures
retain the previous managed load-balancer A record. Errors remain pending while
independent valid node partitions can reconcile. Exposures link to their zone's
DNS details. Operations contains audit history, version information, and recovery
guidance, rather than a raw DNS editor.

### Exposure load-balancer overrides

In **Exposures**, choose **All load balancers** (the default) or **Selected load balancers** for each public hostname. Aliases may select different edges while sharing a Docker application Service. The administrator API is `PUT /api/exposures/{id}/loadbalancers` with `{"loadbalancer_ids":null}` or a nonempty ID array. `GET /api/exposures` returns immutable exposure IDs and edge readiness diagnostics. Direct tailnet creation uses `discovery_loadbalancer_id` solely for peer selection and the initial connection test; it does not bind routing to that edge.

Control publishes deduplicated DNS-only A records for authorized, healthy, backend-reachable edges. Multiple DNS addresses distribute traffic; they do not guarantee client failover. Ambiguous endpoint discovery retains only the affected edge’s previously owned IP and marks it degraded. Exposure ledger ownership remains stable when nodes are added, removed, or overrides change.
