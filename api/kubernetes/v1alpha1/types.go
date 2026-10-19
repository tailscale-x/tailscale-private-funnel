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

// KeyIssuerReference points to the installation's shared auth_keys issuer.
// All components in an installation should reference the same Secret.
type KeyIssuerReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:default=client_id
	ClientIDKey string `json:"clientIDKey,omitempty"`
	// +kubebuilder:default=client_secret
	ClientSecretKey string `json:"clientSecretKey,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.controlRef) && has(self.managementURL))",message="Choose controlRef or managementURL"
type ComponentSpec struct {
	// ComponentID is the immutable logical control-plane identity. Defaults to the CR UID.
	// It is independent of disposable Tailscale device and Pod identities.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Logical component identity is immutable"
	ComponentID  string             `json:"componentID,omitempty"`
	KeyIssuerRef KeyIssuerReference `json:"keyIssuerRef"`

	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`
	// +kubebuilder:default=main
	// +kubebuilder:validation:MaxLength=24
	// +kubebuilder:validation:Pattern="^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Installation is immutable"
	Installation string `json:"installation,omitempty"`
	// +kubebuilder:validation:Pattern="^[a-z0-9]([a-z0-9-]*[a-z0-9])?$"
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Hostname is immutable; recreate to change identity"
	Hostname      string                     `json:"hostname,omitempty"`
	ManagementURL string                     `json:"managementURL,omitempty"`
	ControlRef    *core.LocalObjectReference `json:"controlRef,omitempty"`
	Placement     Placement                  `json:"placement,omitempty"`
	Resources     core.ResourceRequirements  `json:"resources,omitempty"`
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
	EncryptionSecretRef *core.LocalObjectReference `json:"encryptionSecretRef,omitempty"`
	UsersRef            core.LocalObjectReference  `json:"usersRef"`
	ServicesRef         core.LocalObjectReference  `json:"servicesRef"`
	ComponentSpec       `json:",inline"`
	// +kubebuilder:validation:MinLength=1
	Tailnet string `json:"tailnet"`
	// +kubebuilder:default={type:sqlite}
	// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type && (self.type != 'postgres' || self.postgres.connectionSecretRef == oldSelf.postgres.connectionSecretRef)",message="Database backend and connection Secret binding are immutable; recreate to change them"
	Database ControlDatabase `json:"database,omitempty"`
}

type LoadBalancerSpec struct {
	PublicHostname   string `json:"publicHostname"`
	CertificateEmail string `json:"certificateEmail,omitempty"`

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
	// RoutingID keeps application and Service identifiers independent of workload identity.
	// Defaults to the logical component ID.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Routing identity is immutable"
	RoutingID     string `json:"routingID,omitempty"`
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
