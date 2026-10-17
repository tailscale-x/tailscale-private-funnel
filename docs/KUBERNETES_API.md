# Kubernetes resource reference

Generated from the pinned v1alpha1 CRD schemas. Application policy and DNS stay in SQLite.

## FunnelControl

| Spec field | Type | Description |
|---|---|---|
| `bootstrapSecretRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `controlRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `hostname` | string |  |
| `identitySecretRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `image` | string |  |
| `installation` | string |  |
| `managementURL` | string |  |
| `placement` | object |  |
| `resources` | object | ResourceRequirements describes the compute resource requirements. |
| `storage` | object |  |
| `tailnet` | string |  |

## FunnelGateway

| Spec field | Type | Description |
|---|---|---|
| `bootstrapSecretRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `controlRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `forwarderPlacement` | object |  |
| `forwarderResources` | object | ResourceRequirements describes the compute resource requirements. |
| `gatewayClass` | string |  |
| `hostname` | string |  |
| `identitySecretRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `image` | string |  |
| `ingressClass` | string |  |
| `installation` | string |  |
| `managementURL` | string |  |
| `placement` | object |  |
| `resources` | object | ResourceRequirements describes the compute resource requirements. |
| `storage` | object |  |
| `watchNamespaces` | array |  |

## FunnelLoadBalancer

| Spec field | Type | Description |
|---|---|---|
| `bootstrapSecretRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `controlRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `hostname` | string |  |
| `identitySecretRef` | object | LocalObjectReference contains enough information to let you locate the referenced object inside the same namespace. |
| `image` | string |  |
| `installation` | string |  |
| `managementURL` | string |  |
| `placement` | object |  |
| `publicService` | object |  |
| `resources` | object | ResourceRequirements describes the compute resource requirements. |
| `storage` | object |  |
