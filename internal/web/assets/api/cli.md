# Funnel CLI

Generated from protobuf/Cobra metadata. Do not edit.

Use `native` for this Linux machine, `k8s` with an explicit `--context` for operator-backed deployment, and `manage` for the Tailscale-authorized installation API. Public `native ... create` and `k8s ... create` generate installers; they never install locally, connect by SSH or contact Kubernetes. Non-control component generation needs authorized control connectivity. Use `--output script` and transfer its secret output to the execution machine; bootstrap expires after 15 minutes. The target uses embedded tsnet and needs no host Tailscale.

Use `--output json`, mutation `--dry-run`, target-side deployment `--wait` and `--timeout`, and `--yes` for destructive deletion. UI instruction generation is read-only; CLI create issues enrollment credentials, while hidden installers execute deployment. Stop preserves identity and data; delete revokes and removes owned state.

Management uses `--control-url` or XDG client.json. Secret inputs are file/stdin only. Exit codes: 0 success, 1 failure, 2 invalid input, 3 authorization, 4 transport unavailable, 5 conflict, 6 timeout.

## manage whoami

Identify the verified caller and its effective control roles.

Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage health

Inspect control version and resource health without starting another node.

Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage providers list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage providers save



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.SaveProviderRequest`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |
| `vendor` | string |  |
| `accountId` | string |  |
| `credentials` | map |  |
| `enabled` | bool |  |

## manage providers delete



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage providers enable



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.EnableRequest`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `enabled` | bool |  |

## manage providers test



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage zones discover



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage zones list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage zones add



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.AddZoneRequest`.

| Field | Type | Notes |
|---|---|---|
| `providerId` | string |  |
| `domain` | string |  |

## manage zones delete



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage zones test



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage nodes list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.ListNodesRequest`.

| Field | Type | Notes |
|---|---|---|
| `mode` | string |  |

## manage nodes get



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage nodes rename



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.RenameRequest`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |

## manage enrollments rename



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.RenameRequest`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |

## manage applications rename



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.RenameRequest`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |

## manage enrollments list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.ListNodesRequest`.

| Field | Type | Notes |
|---|---|---|
| `mode` | string |  |

## manage policies get



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage policies validate



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.Policy`.

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rules` | list of funnel.v1.Rule |  |

## manage policies test



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.PolicyTestRequest`.

| Field | Type | Notes |
|---|---|---|
| `hostname` | string |  |
| `gatewayIds` | list of string |  |
| `roles` | list of string |  |

## manage access get



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage settings get



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage applications list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage reports list

Inspect accepted exposure reports and their freshness without replaying them.

Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage services hosts

Inspect only application Service hosts managed by this control installation.

Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage audit list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.AuditRequest`.

| Field | Type | Notes |
|---|---|---|
| `limit` | uint32 |  |

## manage providers vendors



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage policies save



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.Policy`.

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rules` | list of funnel.v1.Rule |  |

## manage policies preview



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.Policy`.

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rules` | list of funnel.v1.Rule |  |

## manage policies rules save



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.SaveRuleRequest`.

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rule` | funnel.v1.Rule |  |

## manage policies rules delete



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.DeleteRuleRequest`.

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `id` | string |  |

## manage access save



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.AccessSettings`.

| Field | Type | Notes |
|---|---|---|
| `roles` | map |  |
| `capability` | string |  |
| `confirmSelfRemoval` | bool | One-shot confirmation; requires a verified active Owner in replacement roles. |

## manage settings defaults save



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.EnrollmentDefaults`.

| Field | Type | Notes |
|---|---|---|
| `managementUrl` | string |  |
| `releaseVersion` | string |  |

## manage settings oauth test



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.OAuthRequest`.

| Field | Type | Notes |
|---|---|---|
| `clientId` | string | Secret; file/stdin only.  |
| `clientSecret` | string | Secret; file/stdin only.  |

## manage settings oauth save



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.OAuthRequest`.

| Field | Type | Notes |
|---|---|---|
| `clientId` | string | Secret; file/stdin only.  |
| `clientSecret` | string | Secret; file/stdin only.  |

## manage exposures list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.Empty`.

No input fields.

## manage nodes revoke



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage nodes cleanup



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage enrollments revoke



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage nodes peers



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage nodes probe



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ProbeRequest`.

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `deviceId` | string |  |
| `port` | uint32 |  |

## manage exposures tailnet save



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.TailnetExposureRequest`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |
| `nodeId` | string |  |
| `backendDeviceId` | string |  |
| `backendPort` | uint32 |  |
| `publicHostname` | string |  |
| `enabled` | bool |  |
| `revision` | int64 |  |
| `probeId` | string |  |

## manage exposures tailnet delete



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.ResourceRevision`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `revision` | int64 |  |

## manage exposures aliases add



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.AliasRequest`.

| Field | Type | Notes |
|---|---|---|
| `applicationId` | string |  |
| `hostname` | string |  |

## manage dns list



Required permission: `PERMISSION_VIEWER`. Request: `funnel.v1.ResourceID`.

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

## manage dns preview



Required permission: `PERMISSION_OPERATOR`. Request: `funnel.v1.DNSRequest`.

| Field | Type | Notes |
|---|---|---|
| `zoneId` | string |  |
| `approvals` | map |  |

## manage dns apply



Required permission: `PERMISSION_ADMIN`. Request: `funnel.v1.DNSRequest`.

| Field | Type | Notes |
|---|---|---|
| `zoneId` | string |  |
| `approvals` | map |  |

# Actual command help

This section is generated by walking the executable's Cobra management tree.

## tailscale-private-funnel k8s controls create

```text
Usage:
  tailscale-private-funnel k8s controls create NAME [flags]

Flags:
      --administrator stringArray         additional first-bootstrap administrator selector; default autogroup:owner remains
      --context string                    explicit Kubernetes context (required)
      --control-url string                Funnel control URL
      --display-name string               editable display label
      --dry-run                           validate and render without mutation
  -h, --help                              help for create
      --hostname string                   Tailscale hostname; defaults to resource name
      --installation string               installation trust boundary (default "main")
      --namespace string                  installation namespace (default "tailscale-private-funnel-main-system")
      --node-selector stringToString      component placement (gateway: forwarders); key=value pairs (default [])
      --oauth-client-id-file string       protected OAuth client ID file
      --oauth-client-secret-file string   protected OAuth client secret file
      --output string                     output: instructions, script, or json (installer generation) (default "instructions")
      --placement-file string             component Pod placement JSON (nodeSelector, affinity, tolerations)
      --storage-class string              PVC StorageClass; omit only when the cluster has a default
      --storage-size string               persistent volume size (default "1Gi")
      --tailnet string                    Tailscale tailnet name
      --timeout duration                  target execution timeout; generation does not wait for deployment (default 10m0s)
      --version string                    pinned component version (default "VERSION")
      --wait                              wait for readiness during target-side execution
```

## tailscale-private-funnel k8s controls delete

```text
Usage:
  tailscale-private-funnel k8s controls delete NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for delete
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
      --yes                   confirm deletion of owned identities, storage and exposures
```

## tailscale-private-funnel k8s controls get

```text
Usage:
  tailscale-private-funnel k8s controls get NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for get
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s controls list

```text
Usage:
  tailscale-private-funnel k8s controls list [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for list
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s controls update

```text
Usage:
  tailscale-private-funnel k8s controls update NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
      --file string           partial component spec JSON; no credentials
  -h, --help                  help for update
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s gateways create

```text
Usage:
  tailscale-private-funnel k8s gateways create NAME [flags]

Flags:
      --context string                    explicit Kubernetes context (required)
      --control-url string                Funnel control URL
      --display-name string               editable display label
      --dry-run                           validate and render without mutation
      --forwarder-placement-file string   forwarder Pod placement JSON
      --gateway-class string              GatewayClass name
  -h, --help                              help for create
      --hostname string                   Tailscale hostname; defaults to resource name
      --ingress-class string              IngressClass name
      --installation string               installation trust boundary (default "main")
      --namespace string                  installation namespace (default "tailscale-private-funnel-main-system")
      --node-selector stringToString      component placement (gateway: forwarders); key=value pairs (default [])
      --output string                     output: instructions, script, or json (installer generation) (default "instructions")
      --placement-file string             component Pod placement JSON (nodeSelector, affinity, tolerations)
      --timeout duration                  target execution timeout; generation does not wait for deployment (default 10m0s)
      --version string                    pinned component version (default "VERSION")
      --wait                              wait for readiness during target-side execution
      --watch-namespace stringArray       routing namespace; repeatable
```

## tailscale-private-funnel k8s gateways delete

```text
Usage:
  tailscale-private-funnel k8s gateways delete NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for delete
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
      --yes                   confirm deletion of owned identities, storage and exposures
```

## tailscale-private-funnel k8s gateways get

```text
Usage:
  tailscale-private-funnel k8s gateways get NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for get
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s gateways list

```text
Usage:
  tailscale-private-funnel k8s gateways list [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for list
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s gateways update

```text
Usage:
  tailscale-private-funnel k8s gateways update NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
      --file string           partial component spec JSON; no credentials
  -h, --help                  help for update
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s loadbalancers create

```text
Usage:
  tailscale-private-funnel k8s loadbalancers create NAME [flags]

Flags:
      --certificate-email string       ACME contact email
      --context string                 explicit Kubernetes context (required)
      --control-url string             Funnel control URL
      --display-name string            editable display label
      --dry-run                        validate and render without mutation
  -h, --help                           help for create
      --hostname string                Tailscale hostname; defaults to resource name
      --installation string            installation trust boundary (default "main")
      --namespace string               installation namespace (default "tailscale-private-funnel-main-system")
      --node-selector stringToString   component placement (gateway: forwarders); key=value pairs (default [])
      --output string                  output: instructions, script, or json (installer generation) (default "instructions")
      --placement-file string          component Pod placement JSON (nodeSelector, affinity, tolerations)
      --public-hostname string         public DNS hostname
      --storage-class string           PVC StorageClass; omit only when the cluster has a default
      --storage-size string            persistent volume size (default "1Gi")
      --timeout duration               target execution timeout; generation does not wait for deployment (default 10m0s)
      --version string                 pinned component version (default "VERSION")
      --wait                           wait for readiness during target-side execution
```

## tailscale-private-funnel k8s loadbalancers delete

```text
Usage:
  tailscale-private-funnel k8s loadbalancers delete NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for delete
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
      --yes                   confirm deletion of owned identities, storage and exposures
```

## tailscale-private-funnel k8s loadbalancers get

```text
Usage:
  tailscale-private-funnel k8s loadbalancers get NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for get
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s loadbalancers list

```text
Usage:
  tailscale-private-funnel k8s loadbalancers list [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for list
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s loadbalancers update

```text
Usage:
  tailscale-private-funnel k8s loadbalancers update NAME [flags]

Flags:
      --context string        explicit Kubernetes context (required)
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
      --file string           partial component spec JSON; no credentials
  -h, --help                  help for update
      --hostname string       Tailscale hostname; defaults to resource name
      --kubeconfig string     Kubernetes configuration file
      --namespace string      installation namespace (default "tailscale-private-funnel-main-system")
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel k8s operator create

```text
Usage:
  tailscale-private-funnel k8s operator create [flags]

Flags:
      --context string                  explicit Kubernetes context (required)
      --control-url string              Funnel control URL
      --display-name string             editable display label
      --dry-run                         validate and render without mutation
  -h, --help                            help for create
      --hostname string                 Tailscale hostname; defaults to resource name
      --managed-namespace stringArray   namespace receiving operator permissions; repeatable
      --output string                   output: instructions, script, or json (installer generation) (default "instructions")
      --placement-file string           operator Pod placement JSON (nodeSelector, affinity, tolerations)
      --timeout duration                target execution timeout; generation does not wait for deployment (default 10m0s)
      --version string                  pinned component version (default "VERSION")
      --wait                            wait for readiness during target-side execution
```

## tailscale-private-funnel k8s operator delete

```text
Usage:
  tailscale-private-funnel k8s operator delete [flags]

Flags:
      --context string                  explicit Kubernetes context (required)
      --control-url string              Funnel control URL
      --display-name string             editable display label
      --dry-run                         validate and render without mutation
  -h, --help                            help for delete
      --hostname string                 Tailscale hostname; defaults to resource name
      --kubeconfig string               Kubernetes configuration file
      --managed-namespace stringArray   namespace receiving operator permissions; repeatable
      --output string                   output: table or json (default "table")
      --timeout duration                operation timeout (default 10m0s)
      --version string                  pinned component version (default "VERSION")
      --wait                            wait for reconciliation and readiness
      --yes                             confirm operator removal
```

## tailscale-private-funnel k8s operator get

```text
Usage:
  tailscale-private-funnel k8s operator get [flags]

Flags:
      --context string                  explicit Kubernetes context (required)
      --control-url string              Funnel control URL
      --display-name string             editable display label
      --dry-run                         validate and render without mutation
  -h, --help                            help for get
      --hostname string                 Tailscale hostname; defaults to resource name
      --kubeconfig string               Kubernetes configuration file
      --managed-namespace stringArray   namespace receiving operator permissions; repeatable
      --output string                   output: table or json (default "table")
      --timeout duration                operation timeout (default 10m0s)
      --version string                  pinned component version (default "VERSION")
      --wait                            wait for reconciliation and readiness
```

## tailscale-private-funnel k8s operator update

```text
Usage:
  tailscale-private-funnel k8s operator update [flags]

Flags:
      --context string                  explicit Kubernetes context (required)
      --control-url string              Funnel control URL
      --display-name string             editable display label
      --dry-run                         validate and render without mutation
  -h, --help                            help for update
      --hostname string                 Tailscale hostname; defaults to resource name
      --kubeconfig string               Kubernetes configuration file
      --managed-namespace stringArray   namespace receiving operator permissions; repeatable
      --output string                   output: table or json (default "table")
      --placement-file string           operator Pod placement JSON (nodeSelector, affinity, tolerations)
      --timeout duration                operation timeout (default 10m0s)
      --version string                  pinned component version (default "VERSION")
      --wait                            wait for reconciliation and readiness
```

## tailscale-private-funnel manage access get

```text
Usage:
  tailscale-private-funnel manage access get [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for get

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage access save

```text
Usage:
  tailscale-private-funnel manage access save [flags]

Flags:
      --capability string      capability
      --confirm-self-removal   confirm_self_removal
      --dry-run                validate input without sending a mutation
      --file string            protobuf JSON input file; - reads stdin
  -h, --help                   help for save

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage applications list

```text
Usage:
  tailscale-private-funnel manage applications list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage applications rename

```text
Usage:
  tailscale-private-funnel manage applications rename [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for rename
      --id string     id
      --name string   name

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage audit list

```text
Usage:
  tailscale-private-funnel manage audit list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list
      --limit uint    limit

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage dns apply

```text
Usage:
  tailscale-private-funnel manage dns apply [flags]

Flags:
      --dry-run          validate input without sending a mutation
      --file string      protobuf JSON input file; - reads stdin
  -h, --help             help for apply
      --yes              explicitly confirm this mutation
      --zone-id string   zone_id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage dns list

```text
Usage:
  tailscale-private-funnel manage dns list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list
      --id string     id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage dns preview

```text
Usage:
  tailscale-private-funnel manage dns preview [flags]

Flags:
      --dry-run          validate input without sending a mutation
      --file string      protobuf JSON input file; - reads stdin
  -h, --help             help for preview
      --zone-id string   zone_id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage enrollments list

```text
Usage:
  tailscale-private-funnel manage enrollments list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list
      --mode string   mode

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage enrollments rename

```text
Usage:
  tailscale-private-funnel manage enrollments rename [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for rename
      --id string     id
      --name string   name

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage enrollments revoke

```text
Usage:
  tailscale-private-funnel manage enrollments revoke [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for revoke
      --id string     id
      --yes           explicitly confirm this mutation

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage exposures aliases add

```text
Usage:
  tailscale-private-funnel manage exposures aliases add [flags]

Flags:
      --application-id string   application_id
      --dry-run                 validate input without sending a mutation
      --file string             protobuf JSON input file; - reads stdin
  -h, --help                    help for add
      --hostname string         hostname

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage exposures list

```text
Usage:
  tailscale-private-funnel manage exposures list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage exposures tailnet delete

```text
Usage:
  tailscale-private-funnel manage exposures tailnet delete [flags]

Flags:
      --dry-run        validate input without sending a mutation
      --file string    protobuf JSON input file; - reads stdin
  -h, --help           help for delete
      --id string      id
      --revision int   revision
      --yes            explicitly confirm this mutation

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage exposures tailnet save

```text
Usage:
  tailscale-private-funnel manage exposures tailnet save [flags]

Flags:
      --backend-device-id string   backend_device_id
      --backend-port uint          backend_port
      --dry-run                    validate input without sending a mutation
      --enabled                    enabled
      --file string                protobuf JSON input file; - reads stdin
  -h, --help                       help for save
      --id string                  id
      --name string                name
      --node-id string             node_id
      --probe-id string            probe_id
      --public-hostname string     public_hostname
      --revision int               revision

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage health

```text
Usage:
  tailscale-private-funnel manage health [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for health

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage nodes cleanup

```text
Usage:
  tailscale-private-funnel manage nodes cleanup [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for cleanup
      --id string     id
      --yes           explicitly confirm this mutation

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage nodes get

```text
Usage:
  tailscale-private-funnel manage nodes get [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for get
      --id string     id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage nodes list

```text
Usage:
  tailscale-private-funnel manage nodes list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list
      --mode string   mode

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage nodes peers

```text
Usage:
  tailscale-private-funnel manage nodes peers [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for peers
      --id string     id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage nodes probe

```text
Usage:
  tailscale-private-funnel manage nodes probe [flags]

Flags:
      --device-id string   device_id
      --dry-run            validate input without sending a mutation
      --file string        protobuf JSON input file; - reads stdin
  -h, --help               help for probe
      --node-id string     node_id
      --port uint          port

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage nodes rename

```text
Usage:
  tailscale-private-funnel manage nodes rename [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for rename
      --id string     id
      --name string   name

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage nodes revoke

```text
Usage:
  tailscale-private-funnel manage nodes revoke [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for revoke
      --id string     id
      --yes           explicitly confirm this mutation

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage policies get

```text
Usage:
  tailscale-private-funnel manage policies get [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for get

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage policies preview

```text
Usage:
  tailscale-private-funnel manage policies preview [flags]

Flags:
      --dry-run         validate input without sending a mutation
      --file string     protobuf JSON input file; - reads stdin
  -h, --help            help for preview
      --revision uint   revision

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage policies rules delete

```text
Usage:
  tailscale-private-funnel manage policies rules delete [flags]

Flags:
      --dry-run         validate input without sending a mutation
      --file string     protobuf JSON input file; - reads stdin
  -h, --help            help for delete
      --id string       id
      --revision uint   revision
      --yes             explicitly confirm this mutation

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage policies rules save

```text
Usage:
  tailscale-private-funnel manage policies rules save [flags]

Flags:
      --dry-run         validate input without sending a mutation
      --file string     protobuf JSON input file; - reads stdin
  -h, --help            help for save
      --revision uint   revision

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage policies save

```text
Usage:
  tailscale-private-funnel manage policies save [flags]

Flags:
      --dry-run         validate input without sending a mutation
      --file string     protobuf JSON input file; - reads stdin
  -h, --help            help for save
      --revision uint   revision

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage policies test

```text
Usage:
  tailscale-private-funnel manage policies test [flags]

Flags:
      --dry-run               validate input without sending a mutation
      --file string           protobuf JSON input file; - reads stdin
      --gateway-ids strings   gateway_ids
  -h, --help                  help for test
      --hostname string       hostname
      --roles strings         roles

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage policies validate

```text
Usage:
  tailscale-private-funnel manage policies validate [flags]

Flags:
      --dry-run         validate input without sending a mutation
      --file string     protobuf JSON input file; - reads stdin
  -h, --help            help for validate
      --revision uint   revision

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage providers delete

```text
Usage:
  tailscale-private-funnel manage providers delete [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for delete
      --id string     id
      --yes           explicitly confirm this mutation

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage providers enable

```text
Usage:
  tailscale-private-funnel manage providers enable [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --enabled       enabled
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for enable
      --id string     id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage providers list

```text
Usage:
  tailscale-private-funnel manage providers list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage providers save

```text
Usage:
  tailscale-private-funnel manage providers save [flags]

Flags:
      --account-id string   account_id
      --dry-run             validate input without sending a mutation
      --enabled             enabled
      --file string         protobuf JSON input file; - reads stdin
  -h, --help                help for save
      --id string           id
      --name string         name
      --vendor string       vendor

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage providers test

```text
Usage:
  tailscale-private-funnel manage providers test [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for test
      --id string     id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage providers vendors

```text
Usage:
  tailscale-private-funnel manage providers vendors [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for vendors

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage reports list

```text
Usage:
  tailscale-private-funnel manage reports list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage services hosts

```text
Usage:
  tailscale-private-funnel manage services hosts [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for hosts

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage settings defaults save

```text
Usage:
  tailscale-private-funnel manage settings defaults save [flags]

Flags:
      --dry-run                  validate input without sending a mutation
      --file string              protobuf JSON input file; - reads stdin
  -h, --help                     help for save
      --management-url string    management_url
      --release-version string   release_version

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage settings get

```text
Usage:
  tailscale-private-funnel manage settings get [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for get

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage settings oauth save

```text
Usage:
  tailscale-private-funnel manage settings oauth save [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for save

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage settings oauth test

```text
Usage:
  tailscale-private-funnel manage settings oauth test [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for test

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage whoami

```text
Usage:
  tailscale-private-funnel manage whoami [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for whoami

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage zones add

```text
Usage:
  tailscale-private-funnel manage zones add [flags]

Flags:
      --domain string        domain
      --dry-run              validate input without sending a mutation
      --file string          protobuf JSON input file; - reads stdin
  -h, --help                 help for add
      --provider-id string   provider_id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage zones delete

```text
Usage:
  tailscale-private-funnel manage zones delete [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for delete
      --id string     id
      --yes           explicitly confirm this mutation

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage zones discover

```text
Usage:
  tailscale-private-funnel manage zones discover [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for discover
      --id string     id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage zones list

```text
Usage:
  tailscale-private-funnel manage zones list [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for list

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel manage zones test

```text
Usage:
  tailscale-private-funnel manage zones test [flags]

Flags:
      --dry-run       validate input without sending a mutation
      --file string   protobuf JSON input file; - reads stdin
  -h, --help          help for test
      --id string     id

Global Flags:
      --control-url string   tailnet control URL (or XDG client.json)
      --output string        output: table or json (default "table")
      --timeout duration     RPC timeout (default 30s)
```

## tailscale-private-funnel native controls create

```text
Usage:
  tailscale-private-funnel native controls create NAME [flags]

Flags:
      --administrator stringArray         additional first-bootstrap administrator selector; default autogroup:owner remains
      --control-url string                Funnel control URL
      --data-dir string                   persistent application path
      --display-name string               editable display label
      --dry-run                           validate and render without mutation
  -h, --help                              help for create
      --hostname string                   Tailscale hostname; defaults to resource name
      --oauth-client-id-file string       protected OAuth client ID file
      --oauth-client-secret-file string   protected OAuth client secret file
      --output string                     output: instructions, script, or json (installer generation) (default "instructions")
      --state-dir string                  persistent identity path
      --tailnet string                    Tailscale tailnet name
      --timeout duration                  target execution timeout; generation does not wait for deployment (default 10m0s)
      --version string                    pinned component version (default "VERSION")
      --wait                              wait for readiness during target-side execution
```

## tailscale-private-funnel native controls delete

```text
Usage:
  tailscale-private-funnel native controls delete NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for delete
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
      --yes                   confirm revocation and removal of all owned state
```

## tailscale-private-funnel native controls get

```text
Usage:
  tailscale-private-funnel native controls get NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for get
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native controls list

```text
Usage:
  tailscale-private-funnel native controls list [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for list
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native controls logs

```text
Usage:
  tailscale-private-funnel native controls logs NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for logs
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native controls restart

```text
Usage:
  tailscale-private-funnel native controls restart NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for restart
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native controls start

```text
Usage:
  tailscale-private-funnel native controls start NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for start
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native controls stop

```text
Usage:
  tailscale-private-funnel native controls stop NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for stop
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native controls update

```text
Usage:
  tailscale-private-funnel native controls update NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for update
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native gateways create

```text
Usage:
  tailscale-private-funnel native gateways create NAME [flags]

Flags:
      --control-url string         Funnel control URL
      --data-dir string            persistent application path
      --deny-network stringArray   local Docker network deny pattern
      --display-name string        editable display label
      --dry-run                    validate and render without mutation
  -h, --help                       help for create
      --hostname string            Tailscale hostname; defaults to resource name
      --label-key stringArray      additional Docker label alias
      --output string              output: instructions, script, or json (installer generation) (default "instructions")
      --state-dir string           persistent identity path
      --timeout duration           target execution timeout; generation does not wait for deployment (default 10m0s)
      --type string                gateway type: docker or tailnet
      --version string             pinned component version (default "VERSION")
      --wait                       wait for readiness during target-side execution
```

## tailscale-private-funnel native gateways delete

```text
Usage:
  tailscale-private-funnel native gateways delete NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for delete
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
      --yes                   confirm revocation and removal of all owned state
```

## tailscale-private-funnel native gateways get

```text
Usage:
  tailscale-private-funnel native gateways get NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for get
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native gateways list

```text
Usage:
  tailscale-private-funnel native gateways list [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for list
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native gateways logs

```text
Usage:
  tailscale-private-funnel native gateways logs NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for logs
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native gateways restart

```text
Usage:
  tailscale-private-funnel native gateways restart NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for restart
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native gateways start

```text
Usage:
  tailscale-private-funnel native gateways start NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for start
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native gateways stop

```text
Usage:
  tailscale-private-funnel native gateways stop NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for stop
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native gateways update

```text
Usage:
  tailscale-private-funnel native gateways update NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for update
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native loadbalancers create

```text
Usage:
  tailscale-private-funnel native loadbalancers create NAME [flags]

Flags:
      --certificate-email string   ACME contact email
      --control-url string         Funnel control URL
      --data-dir string            persistent application path
      --display-name string        editable display label
      --dry-run                    validate and render without mutation
  -h, --help                       help for create
      --hostname string            Tailscale hostname; defaults to resource name
      --output string              output: instructions, script, or json (installer generation) (default "instructions")
      --public-hostname string     public DNS hostname
      --state-dir string           persistent identity path
      --timeout duration           target execution timeout; generation does not wait for deployment (default 10m0s)
      --version string             pinned component version (default "VERSION")
      --wait                       wait for readiness during target-side execution
```

## tailscale-private-funnel native loadbalancers delete

```text
Usage:
  tailscale-private-funnel native loadbalancers delete NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for delete
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
      --yes                   confirm revocation and removal of all owned state
```

## tailscale-private-funnel native loadbalancers get

```text
Usage:
  tailscale-private-funnel native loadbalancers get NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for get
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native loadbalancers list

```text
Usage:
  tailscale-private-funnel native loadbalancers list [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for list
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native loadbalancers logs

```text
Usage:
  tailscale-private-funnel native loadbalancers logs NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for logs
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native loadbalancers restart

```text
Usage:
  tailscale-private-funnel native loadbalancers restart NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for restart
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native loadbalancers start

```text
Usage:
  tailscale-private-funnel native loadbalancers start NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for start
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native loadbalancers stop

```text
Usage:
  tailscale-private-funnel native loadbalancers stop NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for stop
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```

## tailscale-private-funnel native loadbalancers update

```text
Usage:
  tailscale-private-funnel native loadbalancers update NAME [flags]

Flags:
      --control-url string    Funnel control URL
      --display-name string   editable display label
      --dry-run               validate and render without mutation
  -h, --help                  help for update
      --hostname string       Tailscale hostname; defaults to resource name
      --output string         output: table or json (default "table")
      --timeout duration      operation timeout (default 10m0s)
      --version string        pinned component version (default "VERSION")
      --wait                  wait for reconciliation and readiness
```
