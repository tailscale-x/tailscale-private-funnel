# HTTP path routing and prefix replacement

The public load balancer matches the original request and rewrites its path
before proxying. Docker and Kubernetes gateways continue forwarding raw TCP;
neither adds an HTTP proxy hop or changes Tailscale identity.

## Docker gateways

Declare named routes on each application container:

```yaml
services:
  api:
    image: your-api:version
    labels:
      tailscale-private-funnel.routes.api.hostname: app.example.com
      tailscale-private-funnel.routes.api.port: "8080"
      tailscale-private-funnel.routes.api.path-prefix: /api
      tailscale-private-funnel.routes.api.replace-prefix: /
  admin:
    image: your-admin:version
    labels:
      tailscale-private-funnel.routes.admin.hostname: app.example.com
      tailscale-private-funnel.routes.admin.port: "9090"
      tailscale-private-funnel.routes.admin.path-prefix: /admin
      tailscale-private-funnel.routes.admin.replace-prefix: /internal
```

Each route requires a hostname, container port and path prefix. HTTP is the
protocol. Route names group fields locally; they do not name an application,
Tailscale Service, or DNS record. Only literal, unescaped absolute paths are
accepted (maximum 2048 bytes); queries, fragments, control characters,
placeholders, and regex/capture substitutions are rejected.

Omit `replace-prefix` to preserve the path. Use `/` to strip the prefix.
A configured alias such as `vendor.expose` also enables `vendor.routes.<name>.*`.
Conflicting alias fields and duplicate hostname/path claims are rejected.

The existing label still declares a whole-host catch-all:

```yaml
tailscale-private-funnel.expose: "8000:app.example.com:http"
```

A catch-all may coexist with more-specific routes. Distinct containers on the
same Docker gateway can serve distinct prefixes on the same hostname. The
same hostname/path cannot select competing containers or backend ports.

Exposure policy and managed-zone checks still apply. The supervisor validates
Docker networks and backend reachability and sends signed route reports.
Removing a label withdraws that route; sibling routes and their shared DNS
ownership remain. A backend-port change that conflicts with an existing
assignment is rejected rather than silently retargeting its Service.

## Kubernetes HTTPRoute

Use the standard Gateway API path filter:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: api
spec:
  parentRefs:
    - name: public
  hostnames:
    - app.example.com
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api
      filters:
        - type: URLRewrite
          urlRewrite:
            path:
              type: ReplacePrefixMatch
              replacePrefixMatch: /
      backendRefs:
        - name: api
          port: 8080
```

Prefix replacement requires exactly one explicit PathPrefix match.
ReplaceFullPath, hostname rewriting, and combining rewrite with redirect are
unsupported and reported through conditions/Events. Namespace authorization,
ReferenceGrant, endpoint validation, and acknowledgment-based Programmed status
remain required.

For networking.k8s.io/v1 Ingress, use
`tailscale-private-funnel.io/replace-prefix: "/"` in metadata annotations.
Every affected path must use `pathType: Prefix`; Exact and
ImplementationSpecific paths cannot use this annotation. Without the annotation,
existing Exact/Prefix matching preserves the request path.

## Request behavior

| Incoming path | Match | Replacement | Backend URI |
| --- | --- | --- | --- |
| `/api/users?q=1` | `/api` | omitted | `/api/users?q=1` |
| `/api/users?q=1` | `/api` | `/` | `/users?q=1` |
| `/api/users` | `/api` | `/internal` | `/internal/users` |
| `/api` | `/api` | `/` | `/` |
| `/apiculture` | `/api` | any | Does not match |

Matching uses path-segment boundaries and is case-sensitive. Trailing slashes
on configured prefixes and replacements are normalized. Exact matches precede
prefix matches, and longer prefixes precede shorter ones; existing Kubernetes
tie-breaking remains stable. Unmatched requests return 404 unless another route,
such as a whole-host catch-all, matches.

Query strings, methods, bodies, original Host, trusted forwarding headers,
WebSockets, and streaming are preserved. Escaped suffix characters remain
escaped. Prefix replacement is not a client redirect and does not rewrite
HTML, response Location headers, or cookie paths. Applications may still need
a configured public base path.

The Exposures and Applications views list each configured Docker path, backend,
and prefix replacement. Readiness is shown at hostname level: one healthy,
authorized path can keep its hostname published while another path is offline.

## Updating an installation

Update control and load balancers before gateways start reporting the new
route fields. Existing routes without rewrite settings retain their behavior.
No identity reset, DNS ownership migration, release publication, or deployment
change is required merely to build this feature.

Local verification includes route parsing and translation, signed control
reports, hostname ownership retention, and Caddy requests to disposable echo
backends. No real certificate or public DNS mutation is needed for path
semantics.

Run the disposable Docker check with:

```sh
scripts/test-path-routing.sh
```

It uses two unprivileged, socket-free Node echo containers on a temporary
network, reads their actual route labels, and tests the shared Caddy rules
against them, including prefix replacement, headers, WebSockets, streaming,
and reloads. The test substitutes a local dial callback for tsnet; it does not
enroll devices or test WAN/TLS. Its containers and network are removed on exit.
Kubernetes translation tests use disposable in-memory Gateway/HTTPRoute/Ingress
fixtures; no existing cluster or live routing resource is modified.
