# Kubernetes resource reference

Generated from the pinned v1alpha1 CRD schemas. Application policy and DNS stay in SQLite.

## FunnelControl

| Spec field | Type | Description |
|---|---|---|
| `componentID` | string | ComponentID is the immutable logical control-plane identity. Defaults to the CR UID. It is independent of disposable Tailscale device and Pod identities. |
| `controlRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `database` | object |  |
| `encryptionSecretRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `hostname` | string |  |
| `image` | string |  |
| `installation` | string |  |
| `keyIssuerRef` | object | KeyIssuerReference points to the installation's shared auth_keys issuer. All components in an installation should reference the same Secret. |
| `managementURL` | string |  |
| `placement` | object |  |
| `resources` | object | ResourceRequirements describes the compute resource requirements. |
| `servicesRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `storage` | object |  |
| `tailnet` | string |  |
| `usersRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |

## FunnelGateway

| Spec field | Type | Description |
|---|---|---|
| `componentID` | string | ComponentID is the immutable logical control-plane identity. Defaults to the CR UID. It is independent of disposable Tailscale device and Pod identities. |
| `controlRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `forwarderPlacement` | object |  |
| `forwarderResources` | object | ResourceRequirements describes the compute resource requirements. |
| `gatewayClass` | string |  |
| `hostname` | string |  |
| `image` | string |  |
| `ingressClass` | string |  |
| `installation` | string |  |
| `keyIssuerRef` | object | KeyIssuerReference points to the installation's shared auth_keys issuer. All components in an installation should reference the same Secret. |
| `managementURL` | string |  |
| `placement` | object |  |
| `resources` | object | ResourceRequirements describes the compute resource requirements. |
| `routingID` | string | RoutingID keeps application and Service identifiers independent of workload identity. Defaults to the logical component ID. |
| `storage` | object |  |
| `watchNamespaces` | array |  |

## FunnelLoadBalancer

| Spec field | Type | Description |
|---|---|---|
| `certificateEmail` | string |  |
| `componentID` | string | ComponentID is the immutable logical control-plane identity. Defaults to the CR UID. It is independent of disposable Tailscale device and Pod identities. |
| `controlRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `hostname` | string |  |
| `image` | string |  |
| `installation` | string |  |
| `keyIssuerRef` | object | KeyIssuerReference points to the installation's shared auth_keys issuer. All components in an installation should reference the same Secret. |
| `managementURL` | string |  |
| `placement` | object |  |
| `publicHostname` | string |  |
| `publicService` | object |  |
| `resources` | object | ResourceRequirements describes the compute resource requirements. |
| `storage` | object |  |
