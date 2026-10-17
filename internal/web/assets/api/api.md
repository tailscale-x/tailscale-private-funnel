# Funnel API reference

Generated from protobuf descriptors. Do not edit.

Management and Node services use verified Tailscale WhoIs. Enrollment additionally requires one-use proof. Broker methods are served by the installation HTTPS broker with projected Pod tokens, not by control.

Signed payload bytes must never be reserialized before signature verification.

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
- Output: `funnel.v1.SignedEnvelope`

No input fields.

Signed payload model: `funnel.v1.ConfigurationPayload`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
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

### WriteState



- RPC: `/funnel.v1.BrokerService/WriteState`
- Permission: `PERMISSION_BROKER`
- Input: `funnel.v1.StateUpdate`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `key` | string |  |
| `value` | bytes |  |

### ReleaseLease



- RPC: `/funnel.v1.BrokerService/ReleaseLease`
- Permission: `PERMISSION_BROKER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Empty`

No input fields.

## funnel.v1.DeploymentService

DeploymentService authorizes deployments. Execution is performed by the invoking CLI.

### Instructions

Resolve deployment choices without creating enrollment or bootstrap keys.

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

### Authorize

Create one-use enrollment and return credentials once, without server-side execution.

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

## funnel.v1.EnrollmentService



### BindSigningKey



- RPC: `/funnel.v1.EnrollmentService/BindSigningKey`
- Permission: `PERMISSION_ENROLLMENT`
- Input: `funnel.v1.BindSigningKeyRequest`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `enrollmentId` | string |  |
| `code` | string | Secret; file/stdin only.  |
| `publicKey` | bytes |  |

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

### RenameEnrollment



- RPC: `/funnel.v1.ManagementService/RenameEnrollment`
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

### ListEnrollments



- RPC: `/funnel.v1.ManagementService/ListEnrollments`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.ListNodesRequest`
- Output: `funnel.v1.Enrollments`

| Field | Type | Notes |
|---|---|---|
| `mode` | string |  |

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
- Input: `funnel.v1.EnrollmentDefaults`
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
| `clientId` | string | Secret; file/stdin only.  |
| `clientSecret` | string | Secret; file/stdin only.  |

### SaveOAuth



- RPC: `/funnel.v1.ManagementService/SaveOAuth`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.OAuthRequest`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `clientId` | string | Secret; file/stdin only.  |
| `clientSecret` | string | Secret; file/stdin only.  |

### ListExposures



- RPC: `/funnel.v1.ManagementService/ListExposures`
- Permission: `PERMISSION_VIEWER`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Exposures`

No input fields.

### RevokeNode



- RPC: `/funnel.v1.ManagementService/RevokeNode`
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

### RevokeEnrollment



- RPC: `/funnel.v1.ManagementService/RevokeEnrollment`
- Permission: `PERMISSION_ADMIN`
- Input: `funnel.v1.ResourceID`
- Output: `funnel.v1.Empty`

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



### GetConfig



- RPC: `/funnel.v1.NodeService/GetConfig`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.SignedEnvelope`

No input fields.

Signed payload model: `funnel.v1.ConfigurationPayload`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
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
- Output: `funnel.v1.SignedEnvelope`

| Field | Type | Notes |
|---|---|---|
| `hostname` | string |  |
| `backendPort` | uint32 |  |

Signed payload model: `funnel.v1.ConfigurationPayload`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
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



- RPC: `/funnel.v1.NodeService/SubmitReport`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.SignedEnvelope`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `timestamp` | int64 |  |
| `nonce` | string |  |
| `publicKey` | bytes |  |
| `payload` | bytes |  |
| `signature` | bytes |  |
| `purpose` | string |  |

Signed payload model: `funnel.v1.ExposureReport`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
| `exposures` | list of funnel.v1.ReportedExposure |  |
| `expiresAt` | int64 |  |

### SubmitPeers



- RPC: `/funnel.v1.NodeService/SubmitPeers`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.SignedEnvelope`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `timestamp` | int64 |  |
| `nonce` | string |  |
| `publicKey` | bytes |  |
| `payload` | bytes |  |
| `signature` | bytes |  |
| `purpose` | string |  |

Signed payload model: `funnel.v1.PeerSnapshot`. Verify exact bytes before decoding.

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
- Input: `funnel.v1.SignedEnvelope`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `timestamp` | int64 |  |
| `nonce` | string |  |
| `publicKey` | bytes |  |
| `payload` | bytes |  |
| `signature` | bytes |  |
| `purpose` | string |  |

Signed payload model: `funnel.v1.KubernetesPayload`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
| `errors` | map |  |
| `clusterId` | string |  |
| `hosts` | list of funnel.v1.KubernetesHost |  |
| `expiresAt` | int64 |  |

### SubmitPublicEndpoint



- RPC: `/funnel.v1.NodeService/SubmitPublicEndpoint`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.SignedEnvelope`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `timestamp` | int64 |  |
| `nonce` | string |  |
| `publicKey` | bytes |  |
| `payload` | bytes |  |
| `signature` | bytes |  |
| `purpose` | string |  |

Signed payload model: `funnel.v1.PublicEndpointPayload`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
| `namespace` | string |  |
| `service` | string |  |
| `uid` | string |  |
| `observedAt` | int64 |  |
| `ingress` | list of funnel.v1.PublicIngress |  |

### EnrollForwarder



- RPC: `/funnel.v1.NodeService/EnrollForwarder`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.SignedEnvelope`
- Output: `funnel.v1.ForwarderEnrollment`

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `timestamp` | int64 |  |
| `nonce` | string |  |
| `publicKey` | bytes |  |
| `payload` | bytes |  |
| `signature` | bytes |  |
| `purpose` | string |  |

Signed payload model: `funnel.v1.ForwarderEnrollmentPayload`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
| `nodeUid` | string |  |
| `hostname` | string |  |
| `publicKey` | string |  |

### ListForwarders



- RPC: `/funnel.v1.NodeService/ListForwarders`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.Empty`
- Output: `funnel.v1.Nodes`

No input fields.

### BindForwarder



- RPC: `/funnel.v1.NodeService/BindForwarder`
- Permission: `PERMISSION_ENROLLMENT`
- Input: `funnel.v1.SignedEnvelope`
- Output: `funnel.v1.Empty`

| Field | Type | Notes |
|---|---|---|
| `nodeId` | string |  |
| `timestamp` | int64 |  |
| `nonce` | string |  |
| `publicKey` | bytes |  |
| `payload` | bytes |  |
| `signature` | bytes |  |
| `purpose` | string |  |

Signed payload model: `funnel.v1.ForwarderBindingPayload`. Verify exact bytes before decoding.

| Field | Type | Notes |
|---|---|---|
| `bind` | bool |  |

### Shutdown



- RPC: `/funnel.v1.NodeService/Shutdown`
- Permission: `PERMISSION_NODE`
- Input: `funnel.v1.ShutdownRequest`
- Output: `funnel.v1.TestResult`

| Field | Type | Notes |
|---|---|---|
| `confirm` | bool |  |
