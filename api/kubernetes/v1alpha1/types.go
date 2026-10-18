// Package v1alpha1 defines deployment resources. Application policy and DNS state remain in Funnel control.
// +kubebuilder:object:generate=true
// +groupName=tailscale-private-funnel.io
package v1alpha1

import (
	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var GroupVersion = schema.GroupVersion{Group: "tailscale-private-funnel.io", Version: "v1alpha1"}
var SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &FunnelControl{}, &FunnelControlList{}, &FunnelLoadBalancer{}, &FunnelLoadBalancerList{}, &FunnelGateway{}, &FunnelGatewayList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})
var AddToScheme = SchemeBuilder.AddToScheme

type Placement struct {
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	Affinity     *core.Affinity    `json:"affinity,omitempty"`
	Tolerations  []core.Toleration `json:"tolerations,omitempty"`
}
type Storage struct {
	// +kubebuilder:default="1Gi"
	Size             string  `json:"size,omitempty"`
	StorageClassName *string `json:"storageClassName,omitempty"`
	// An externally supplied PVC is never deleted by the operator.
	ExistingClaim string `json:"existingClaim,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.controlRef) && has(self.managementURL))",message="Choose controlRef or managementURL"
// +kubebuilder:validation:XValidation:rule="has(self.identitySecretRef) == has(oldSelf.identitySecretRef) && (!has(self.identitySecretRef) || self.identitySecretRef == oldSelf.identitySecretRef)",message="Identity binding is immutable"
type ComponentSpec struct {
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
	// +kubebuilder:default=main
	Installation string `json:"installation,omitempty"`
	// +kubebuilder:validation:Pattern="^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Hostname is immutable; recreate to change identity"
	Hostname string `json:"hostname,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Control binding is immutable"
	ManagementURL      string                     `json:"managementURL,omitempty"`
	ControlRef         *core.LocalObjectReference `json:"controlRef,omitempty"`
	BootstrapSecretRef *core.LocalObjectReference `json:"bootstrapSecretRef,omitempty"`
	IdentitySecretRef  *core.LocalObjectReference `json:"identitySecretRef,omitempty"`
	Placement          Placement                  `json:"placement,omitempty"`
	Resources          core.ResourceRequirements  `json:"resources,omitempty"`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Storage binding is immutable"
	Storage Storage `json:"storage,omitempty"`
}
type PostgresDatabase struct {
	ConnectionSecretRef core.SecretKeySelector  `json:"connectionSecretRef"`
	CASecretRef         *core.SecretKeySelector `json:"caSecretRef,omitempty"`
	// +kubebuilder:default="verify-full"
	// +kubebuilder:validation:Enum=verify-full;verify-ca;require;disable
	TLSMode string `json:"tlsMode,omitempty"`
	// Empty peers allow PostgreSQL only within this namespace.
	EgressPeers []networking.NetworkPolicyPeer `json:"egressPeers,omitempty"`
	// +kubebuilder:default=5432
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.type == 'postgres' ? has(self.postgres) : !has(self.postgres)",message="PostgreSQL requires connection settings; SQLite must not have them"
type ControlDatabase struct {
	// +kubebuilder:default=sqlite
	// +kubebuilder:validation:Enum=sqlite;postgres
	Type     string            `json:"type,omitempty"`
	Postgres *PostgresDatabase `json:"postgres,omitempty"`
}
type ControlSpec struct {
	// Transfer fences the source during an offline control transfer.
	Transfer      *ControlTransferState `json:"transfer,omitempty"`
	ComponentSpec `json:",inline"`
	// +kubebuilder:validation:MinLength=1
	Tailnet string `json:"tailnet"`
	// +kubebuilder:default={type:sqlite}
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Database binding is immutable; recreate to change it"
	Database ControlDatabase `json:"database,omitempty"`
}
type ControlTransferState struct {
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`
	// +kubebuilder:validation:Enum=Stopped;Transferred
	Phase string `json:"phase"`
	// Digest of the durably saved installer, required before transfer deletion.
	InstallerSHA256 string `json:"installerSHA256,omitempty"`
}
type LoadBalancerSpec struct {
	ComponentSpec `json:",inline"`
	PublicService PublicServiceSpec `json:"publicService,omitempty"`
}
type PublicServiceSpec struct {
	Annotations       map[string]string `json:"annotations,omitempty"`
	LoadBalancerClass *string           `json:"loadBalancerClass,omitempty"`
	SourceRanges      []string          `json:"sourceRanges,omitempty"`
	// +kubebuilder:default=Local
	// +kubebuilder:validation:Enum=Local;Cluster
	ExternalTrafficPolicy core.ServiceExternalTrafficPolicy `json:"externalTrafficPolicy,omitempty"`
}
type GatewaySpec struct {
	ComponentSpec `json:",inline"`
	// +kubebuilder:validation:MinLength=1
	GatewayClass       string                    `json:"gatewayClass"`
	IngressClass       string                    `json:"ingressClass,omitempty"`
	WatchNamespaces    []string                  `json:"watchNamespaces,omitempty"`
	ForwarderPlacement Placement                 `json:"forwarderPlacement,omitempty"`
	ForwarderResources core.ResourceRequirements `json:"forwarderResources,omitempty"`
}
type ComponentStatus struct {
	Initialized        bool  `json:"initialized,omitempty"`
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +listType=map
	// +listMapKey=type
	Conditions        []metav1.Condition `json:"conditions,omitempty"`
	NodeID            string             `json:"nodeID,omitempty"`
	SetupURL          string             `json:"setupURL,omitempty"`
	ReadyForwarders   int32              `json:"readyForwarders,omitempty"`
	DesiredForwarders int32              `json:"desiredForwarders,omitempty"`
	PublicAddresses   []string           `json:"publicAddresses,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
type FunnelControl struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ControlSpec     `json:"spec"`
	Status            ComponentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type FunnelControlList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FunnelControl `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
type FunnelLoadBalancer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              LoadBalancerSpec `json:"spec"`
	Status            ComponentStatus  `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type FunnelLoadBalancerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FunnelLoadBalancer `json:"items"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type=='Ready')].status"
type FunnelGateway struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              GatewaySpec     `json:"spec"`
	Status            ComponentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type FunnelGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FunnelGateway `json:"items"`
}
