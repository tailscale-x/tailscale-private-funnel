# Funnel API reference

Generated from protobuf descriptors. Do not edit.

Management and Node services use verified Tailscale WhoIs. Runtime calls require exact installation/role tags and an active logical-component assignment. Human management authorization is separate. No enrollment proof, component signatures or signing keys are used. Broker methods use projected Pod tokens on the installation HTTPS broker; WorkloadBootstrapService uses a separate projected audience on the operator TLS endpoint, not control.

## funnel.v1.BrokerService

BrokerService is served only by the installation's HTTPS broker, not control.

### OpenSession



- RPC: `/funnel.v1.BrokerService/OpenSession`
- Permission: `PERMISSION_BROKER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.BrokerSession`

No input fields.

### GetConfig



- RPC: `/funnel.v1.BrokerService/GetConfig`
- Permission: `PERMISSION_BROKER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.ConfigurationPayload`

No input fields.

Typed runtime payload model: `funnel.v1.ConfigurationPayload`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `kubernetesErrors` | map | Per-resource reconciliation failures; unrelated hosts remain operational. |
| `publishedAddresses` | list of string |  |
| `publishedHostnames` | list of string |  |
| `routes` | list of funnel.v1.HTTPRoute |  |
| `probes` | list of funnel.v1.Probe |  |
| `nodeId` | string |  |
| `revision` | string |  |
| `applications` | list of funnel.v1.RuntimeApplication |  |
| `labelAliases` | list of string |  |
| `issuedAt` | int64 |  |

### RenewLease



- RPC: `/funnel.v1.BrokerService/RenewLease`
- Permission: `PERMISSION_BROKER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Empty`

No input fields.

### ReleaseLease



- RPC: `/funnel.v1.BrokerService/ReleaseLease`
- Permission: `PERMISSION_BROKER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Empty`

No input fields.

## funnel.v1.DeploymentService

DeploymentService authorizes deployments. Execution is performed by a generated installer on the target.

### BootstrapTunnel

Phone home using existing host connectivity. Issues only the installation
 Tailnet gateway role, never a caller-selected role or management permission.

- RPC: `/funnel.v1.DeploymentService/BootstrapTunnel`
- Permission: `PERMISSION_TAILNET`
- Input: `funnel.v1.TunnelBootstrapRequest`
- Output: `funnel.v1.TunnelBootstrap`

| Field | Type | Notes |
|---|---|---|
| `installation` | string |  |
| `name` | string |  |
| `hostname` | string |  |
| `port` | uint32 |  |
| `localAddress` | string |  |

### TunnelInstructions

Render a secret-free foreground command; no registration or DNS mutation.

- RPC: `/funnel.v1.DeploymentService/TunnelInstructions`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.TunnelInstructionsRequest`
- Output: `funnel.v1.DeploymentInstructions`

| Field | Type | Notes |
|---|---|---|
| `hostname` | string |  |
| `port` | uint32 |  |
| `localAddress` | string |  |
| `name` | string |  |
| `tailscaleConnection` | string |  |

### Instructions

Resolve deployment choices without creating bootstrap keys.

- RPC: `/funnel.v1.DeploymentService/Instructions`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.DeploymentRequest`
- Output: `funnel.v1.DeploymentInstructions`

| Field | Type | Notes |
|---|---|---|
| `mode` | string |  |
| `target` | string |  |
| `runtime` | string |  |
| `name` | string |  |
| `hostname` | string |  |
| `stateDir` | string |  |
| `dataDir` | string |  |
| `installation` | string |  |
| `resourceName` | string |  |
| `gatewayClass` | string |  |
| `ingressClass` | string |  |
| `watchNamespaces` | list of string |  |
| `nodeSelector` | map |  |
| `certificateEmail` | string |  |
| `publicHostname` | string |  |
| `labelKeys` | list of string |  |
| `denyNetworks` | list of string |  |
| `namespace` | string |  |
| `context` | string |  |
| `version` | string |  |
| `displayName` | string |  |
| `tailnet` | string |  |
| `oauthClientIdFile` | string |  |
| `oauthClientSecretFile` | string |  |
| `nodeId` | string |  |
| `managedNamespaces` | list of string |  |
| `wait` | bool |  |
| `timeoutSeconds` | uint32 |  |
| `currentControl` | bool |  |
| `administrators` | list of string |  |
| `storageClass` | string |  |
| `storageSize` | string |  |
| `placementJson` | bytes | Resolved placement, carried to the executor without local file references. |
| `forwarderPlacementJson` | bytes |  |
| `database` | funnel.v1.DatabaseConfiguration |  |
| `tailscaleConnection` | string |  |
| `tailscaleSocket` | string |  |
| `keyIssuerSecret` | string |  |
| `servicesSecret` | string |  |
| `servicesClientIdFile` | string |  |
| `servicesClientSecretFile` | string |  |
| `usersSecret` | string |  |
| `usersClientIdFile` | string |  |
| `usersClientSecretFile` | string |  |

### Authorize

Mint one-use Tailscale bootstrap credentials, without server-side execution.

- RPC: `/funnel.v1.DeploymentService/Authorize`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.DeploymentRequest`
- Output: `funnel.v1.DeploymentInstructions`

| Field | Type | Notes |
|---|---|---|
| `mode` | string |  |
| `target` | string |  |
| `runtime` | string |  |
| `name` | string |  |
| `hostname` | string |  |
| `stateDir` | string |  |
| `dataDir` | string |  |
| `installation` | string |  |
| `resourceName` | string |  |
| `gatewayClass` | string |  |
| `ingressClass` | string |  |
| `watchNamespaces` | list of string |  |
| `nodeSelector` | map |  |
| `certificateEmail` | string |  |
| `publicHostname` | string |  |
| `labelKeys` | list of string |  |
| `denyNetworks` | list of string |  |
| `namespace` | string |  |
| `context` | string |  |
| `version` | string |  |
| `displayName` | string |  |
| `tailnet` | string |  |
| `oauthClientIdFile` | string |  |
| `oauthClientSecretFile` | string |  |
| `nodeId` | string |  |
| `managedNamespaces` | list of string |  |
| `wait` | bool |  |
| `timeoutSeconds` | uint32 |  |
| `currentControl` | bool |  |
| `administrators` | list of string |  |
| `storageClass` | string |  |
| `storageSize` | string |  |
| `placementJson` | bytes | Resolved placement, carried to the executor without local file references. |
| `forwarderPlacementJson` | bytes |  |
| `database` | funnel.v1.DatabaseConfiguration |  |
| `tailscaleConnection` | string |  |
| `tailscaleSocket` | string |  |
| `keyIssuerSecret` | string |  |
| `servicesSecret` | string |  |
| `servicesClientIdFile` | string |  |
| `servicesClientSecretFile` | string |  |
| `usersSecret` | string |  |
| `usersClientIdFile` | string |  |
| `usersClientSecretFile` | string |  |

## funnel.v1.ManagementService

ManagementService is the tailnet-only interface shared by the CLI and UI.

### WhoAmI

Identify the verified caller and its effective control roles.

- RPC: `/funnel.v1.ManagementService/WhoAmI`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Identity`

No input fields.

### Health

Inspect control version and resource health without starting another node.

- RPC: `/funnel.v1.ManagementService/Health`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.HealthResponse`

No input fields.

### ListProviders



- RPC: `/funnel.v1.ManagementService/ListProviders`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Providers`

No input fields.

### SaveProvider



- RPC: `/funnel.v1.ManagementService/SaveProvider`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.SaveProviderRequest`
- Output: `funnel.v1.Provider`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |
| `vendor` | string |  |
| `accountId` | string |  |
| `credentials` | map |  |
| `enabled` | bool |  |

### DeleteProvider



- RPC: `/funnel.v1.ManagementService/DeleteProvider`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### EnableProvider



- RPC: `/funnel.v1.ManagementService/EnableProvider`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.EnableRequest`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `enabled` | bool |  |

### TestProvider



- RPC: `/funnel.v1.ManagementService/TestProvider`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### DiscoverZones



- RPC: `/funnel.v1.ManagementService/DiscoverZones`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.DiscoveredZones`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### ListZones



- RPC: `/funnel.v1.ManagementService/ListZones`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Zones`

No input fields.

### AddZone



- RPC: `/funnel.v1.ManagementService/AddZone`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.AddZoneRequest`
- Output: `funnel.v1.Zone`

| Field | Type | Notes |
|---|---|---|
| `providerId` | string |  |
| `domain` | string |  |

### DeleteZone



- RPC: `/funnel.v1.ManagementService/DeleteZone`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### TestZone



- RPC: `/funnel.v1.ManagementService/TestZone`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### ListNodes



- RPC: `/funnel.v1.ManagementService/ListNodes`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.ListNodesRequest`
- Output: `funnel.v1.Nodes`

| Field | Type | Notes |
|---|---|---|
| `mode` | string |  |

### GetNode



- RPC: `/funnel.v1.ManagementService/GetNode`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.Node`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### RenameNode



- RPC: `/funnel.v1.ManagementService/RenameNode`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.RenameRequest`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |

### RenameApplication



- RPC: `/funnel.v1.ManagementService/RenameApplication`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.RenameRequest`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `name` | string |  |

### GetPolicy



- RPC: `/funnel.v1.ManagementService/GetPolicy`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Policy`

No input fields.

### ValidatePolicy



- RPC: `/funnel.v1.ManagementService/ValidatePolicy`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.Policy`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rules` | list of funnel.v1.Rule |  |

### TestPolicy



- RPC: `/funnel.v1.ManagementService/TestPolicy`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.PolicyTestRequest`
- Output: `funnel.v1.PolicyTestResult`

| Field | Type | Notes |
|---|---|---|
| `hostname` | string |  |
| `gatewayIds` | list of string |  |
| `roles` | list of string |  |

### GetAccess



- RPC: `/funnel.v1.ManagementService/GetAccess`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.AccessSettings`

No input fields.

### RolePolicy



- RPC: `/funnel.v1.ManagementService/RolePolicy`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.RolePolicyRequest`
- Output: `funnel.v1.RolePolicyTemplate`

| Field | Type | Notes |
|---|---|---|
| `controlPort` | uint32 |  |

### GetSettings



- RPC: `/funnel.v1.ManagementService/GetSettings`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Settings`

No input fields.

### ListApplications



- RPC: `/funnel.v1.ManagementService/ListApplications`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Applications`

No input fields.

### ListReports

Inspect accepted exposure reports and their freshness without replaying them.

- RPC: `/funnel.v1.ManagementService/ListReports`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Reports`

No input fields.

### ListServiceHosts

Inspect only application Service hosts managed by this control installation.

- RPC: `/funnel.v1.ManagementService/ListServiceHosts`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.ServiceHosts`

No input fields.

### ListAudit



- RPC: `/funnel.v1.ManagementService/ListAudit`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.AuditRequest`
- Output: `funnel.v1.AuditEvents`

| Field | Type | Notes |
|---|---|---|
| `limit` | uint32 |  |

### ListVendors



- RPC: `/funnel.v1.ManagementService/ListVendors`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Vendors`

No input fields.

### SavePolicy



- RPC: `/funnel.v1.ManagementService/SavePolicy`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.Policy`
- Output: `funnel.v1.PolicySaveResult`

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rules` | list of funnel.v1.Rule |  |

### PreviewPolicy



- RPC: `/funnel.v1.ManagementService/PreviewPolicy`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.Policy`
- Output: `funnel.v1.PolicyImpact`

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rules` | list of funnel.v1.Rule |  |

### SaveRule



- RPC: `/funnel.v1.ManagementService/SaveRule`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.SaveRuleRequest`
- Output: `funnel.v1.PolicySaveResult`

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `rule` | funnel.v1.Rule |  |

### DeleteRule



- RPC: `/funnel.v1.ManagementService/DeleteRule`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.DeleteRuleRequest`
- Output: `funnel.v1.PolicySaveResult`

| Field | Type | Notes |
|---|---|---|
| `revision` | uint64 |  |
| `id` | string |  |

### SaveAccess



- RPC: `/funnel.v1.ManagementService/SaveAccess`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.AccessSettings`
- Output: `funnel.v1.AccessSettings`

| Field | Type | Notes |
|---|---|---|
| `roles` | map |  |
| `capability` | string |  |
| `confirmSelfRemoval` | bool | One-shot confirmation; requires a verified active Owner in replacement roles. |

### SaveDefaults



- RPC: `/funnel.v1.ManagementService/SaveDefaults`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.DeploymentDefaults`
- Output: `funnel.v1.Settings`

| Field | Type | Notes |
|---|---|---|
| `managementUrl` | string |  |
| `releaseVersion` | string |  |

### TestOAuth



- RPC: `/funnel.v1.ManagementService/TestOAuth`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.OAuthRequest`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `purpose` | string |  |
| `clientId` | string | Secret; file/stdin only.  |
| `clientSecret` | string | Secret; file/stdin only.  |

### SaveOAuth



- RPC: `/funnel.v1.ManagementService/SaveOAuth`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.OAuthRequest`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `purpose` | string |  |
| `clientId` | string | Secret; file/stdin only.  |
| `clientSecret` | string | Secret; file/stdin only.  |

### ListExposures



- RPC: `/funnel.v1.ManagementService/ListExposures`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Exposures`

No input fields.

### DisableNode



- RPC: `/funnel.v1.ManagementService/DisableNode`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### CleanupNode



- RPC: `/funnel.v1.ManagementService/CleanupNode`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### GetPeers



- RPC: `/funnel.v1.ManagementService/GetPeers`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.PeerSnapshot`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### ProbeBackend



- RPC: `/funnel.v1.ManagementService/ProbeBackend`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ProbeRequest`
- Output: `funnel.v1.Probe`

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `deviceId` | string |  |
| `port` | uint32 |  |

### SaveTailnetExposure



- RPC: `/funnel.v1.ManagementService/SaveTailnetExposure`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.TailnetExposureRequest`
- Output: `funnel.v1.Application`

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

### DeleteTailnetExposure



- RPC: `/funnel.v1.ManagementService/DeleteTailnetExposure`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceRevision`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |
| `revision` | int64 |  |

### AddAlias



- RPC: `/funnel.v1.ManagementService/AddAlias`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.AliasRequest`
- Output: `funnel.v1.Application`

| Field | Type | Notes |
|---|---|---|
| `applicationId` | string |  |
| `hostname` | string |  |

### ZoneDNS



- RPC: `/funnel.v1.ManagementService/ZoneDNS`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.ZoneDNSState`

| Field | Type | Notes |
|---|---|---|
| `id` | string |  |

### PreviewDNS



- RPC: `/funnel.v1.ManagementService/PreviewDNS`
- Permission: `PERMISSION_OPERATOR`
- Input: `funnel.v1.DNSRequest`
- Output: `funnel.v1.DNSResult`

| Field | Type | Notes |
|---|---|---|
| `zoneId` | string |  |
| `approvals` | map |  |

### ApplyDNS



- RPC: `/funnel.v1.ManagementService/ApplyDNS`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.DNSRequest`
- Output: `funnel.v1.DNSResult`

| Field | Type | Notes |
|---|---|---|
| `zoneId` | string |  |
| `approvals` | map |  |

## funnel.v1.NodeService



### AdvertiseTunnel

Advertise one foreground localhost tunnel; requires the Tailnet gateway role.

- RPC: `/funnel.v1.NodeService/AdvertiseTunnel`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.LocalTunnelRequest`
- Output: `funnel.v1.LocalTunnelStatus`

| Field | Type | Notes |
|---|---|---|
| `hostname` | string |  |
| `port` | uint32 |  |
| `localAddress` | string |  |

### WithdrawTunnel

Retire only the caller's local tunnel; DNS cleanup is asynchronous.

- RPC: `/funnel.v1.NodeService/WithdrawTunnel`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.TestResult`

No input fields.

### RegisterComponent

Role-authorized registration. Identity and role come only from WhoIs.

- RPC: `/funnel.v1.NodeService/RegisterComponent`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.RegisterComponentRequest`
- Output: `funnel.v1.RegisteredComponent`

| Field | Type | Notes |
|---|---|---|
| `componentId` | string |  |
| `instanceId` | string |  |
| `deployment` | funnel.v1.DeploymentRequest |  |
| `parentId` | string |  |
| `nodeUid` | string |  |

### GetConfig



- RPC: `/funnel.v1.NodeService/GetConfig`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.ConfigurationPayload`

No input fields.

Typed runtime payload model: `funnel.v1.ConfigurationPayload`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `kubernetesErrors` | map | Per-resource reconciliation failures; unrelated hosts remain operational. |
| `publishedAddresses` | list of string |  |
| `publishedHostnames` | list of string |  |
| `routes` | list of funnel.v1.HTTPRoute |  |
| `probes` | list of funnel.v1.Probe |  |
| `nodeId` | string |  |
| `revision` | string |  |
| `applications` | list of funnel.v1.RuntimeApplication |  |
| `labelAliases` | list of string |  |
| `issuedAt` | int64 |  |

### ResolveApplication



- RPC: `/funnel.v1.NodeService/ResolveApplication`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.ResolveApplicationRequest`
- Output: `funnel.v1.ConfigurationPayload`

| Field | Type | Notes |
|---|---|---|
| `pathPrefix` | string |  |
| `rewrite` | funnel.v1.PrefixRewrite |  |
| `hostname` | string |  |
| `backendPort` | uint32 |  |

Typed runtime payload model: `funnel.v1.ConfigurationPayload`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `kubernetesErrors` | map | Per-resource reconciliation failures; unrelated hosts remain operational. |
| `publishedAddresses` | list of string |  |
| `publishedHostnames` | list of string |  |
| `routes` | list of funnel.v1.HTTPRoute |  |
| `probes` | list of funnel.v1.Probe |  |
| `nodeId` | string |  |
| `revision` | string |  |
| `applications` | list of funnel.v1.RuntimeApplication |  |
| `labelAliases` | list of string |  |
| `issuedAt` | int64 |  |

### Acknowledge



- RPC: `/funnel.v1.NodeService/Acknowledge`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.ConfigAck`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `revision` | string |  |
| `services` | list of string |  |

### ReportHealth

Persist liveness without waiting for endpoint discovery or external APIs.

- RPC: `/funnel.v1.NodeService/ReportHealth`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.NodeHealth`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `lastReport` | int64 |  |
| `revision` | string |  |
| `listeners` | list of string |  |
| `backends` | uint32 |  |
| `reachable` | uint32 |  |
| `loadbalancerHostname` | string |  |
| `online` | bool |  |
| `errors` | list of string |  |
| `nodeUid` | string |  |
| `nodeName` | string |  |
| `localEndpoints` | uint32 |  |
| `withdrawalReason` | string |  |
| `advertisedServices` | list of string |  |

### SubmitReport

Accept authenticated desired state durably. Success acknowledges storage,
 not completed DNS or Service publication; bounded control workers reconcile it.

- RPC: `/funnel.v1.NodeService/SubmitReport`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.ExposureReport`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `exposures` | list of funnel.v1.ReportedExposure |  |
| `expiresAt` | int64 |  |

Typed runtime payload model: `funnel.v1.ExposureReport`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `exposures` | list of funnel.v1.ReportedExposure |  |
| `expiresAt` | int64 |  |

### SubmitDockerInventory

Complete Docker declarations, including stopped containers. No external API work.

- RPC: `/funnel.v1.NodeService/SubmitDockerInventory`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.DockerInventoryPayload`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `revision` | int64 |  |
| `complete` | bool |  |
| `containers` | list of funnel.v1.DockerInventoryContainer |  |
| `labelKeys` | list of string |  |

Typed runtime payload model: `funnel.v1.DockerInventoryPayload`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `revision` | int64 |  |
| `complete` | bool |  |
| `containers` | list of funnel.v1.DockerInventoryContainer |  |
| `labelKeys` | list of string |  |

### SubmitPeers



- RPC: `/funnel.v1.NodeService/SubmitPeers`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.PeerSnapshot`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `peers` | list of funnel.v1.Peer |  |
| `tags` | list of string |  |
| `results` | list of funnel.v1.ProbeResult |  |
| `expiresAt` | int64 |  |
| `receivedAt` | int64 |  |
| `sources` | list of string |  |
| `roles` | list of string |  |

Typed runtime payload model: `funnel.v1.PeerSnapshot`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `peers` | list of funnel.v1.Peer |  |
| `tags` | list of string |  |
| `results` | list of funnel.v1.ProbeResult |  |
| `expiresAt` | int64 |  |
| `receivedAt` | int64 |  |
| `sources` | list of string |  |
| `roles` | list of string |  |

### SubmitKubernetes



- RPC: `/funnel.v1.NodeService/SubmitKubernetes`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.KubernetesPayload`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `errors` | map |  |
| `clusterId` | string |  |
| `hosts` | list of funnel.v1.KubernetesHost |  |
| `expiresAt` | int64 |  |

Typed runtime payload model: `funnel.v1.KubernetesPayload`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `errors` | map |  |
| `clusterId` | string |  |
| `hosts` | list of funnel.v1.KubernetesHost |  |
| `expiresAt` | int64 |  |

### SubmitPublicEndpoint



- RPC: `/funnel.v1.NodeService/SubmitPublicEndpoint`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.PublicEndpointPayload`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `namespace` | string |  |
| `service` | string |  |
| `uid` | string |  |
| `observedAt` | int64 |  |
| `ingress` | list of funnel.v1.PublicIngress |  |

Typed runtime payload model: `funnel.v1.PublicEndpointPayload`. Authenticate through WhoIs and exact installation-role tags.

| Field | Type | Notes |
|---|---|---|
| `namespace` | string |  |
| `service` | string |  |
| `uid` | string |  |
| `observedAt` | int64 |  |
| `ingress` | list of funnel.v1.PublicIngress |  |

### ListForwarders



- RPC: `/funnel.v1.NodeService/ListForwarders`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Nodes`

No input fields.

### Shutdown



- RPC: `/funnel.v1.NodeService/Shutdown`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.ShutdownRequest`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `remove` | bool |  |

## funnel.v1.WorkloadBootstrapService

WorkloadBootstrapService is served by the cluster operator over authenticated TLS.

### IssueKey



- RPC: `/funnel.v1.WorkloadBootstrapService/IssueKey`
- Permission: `PERMISSION_BROKER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.WorkloadBootstrap`

No input fields.
