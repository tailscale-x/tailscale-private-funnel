package operator

// Cluster routing and identity validation; no global Secret or workload permissions.
// +kubebuilder:rbac:groups="",resources=namespaces;nodes;pods;services,verbs=get;list;watch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gatewayclasses/status;gateways/status;httproutes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingressclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways;httproutes;referencegrants,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// Namespace-scoped component ownership and delegated workload roles.
// +kubebuilder:rbac:namespace=FUNNEL_MANAGED_NAMESPACE,groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace=FUNNEL_MANAGED_NAMESPACE,groups=tailscale-private-funnel.io,resources=funnelcontrols;funnelloadbalancers;funnelgateways;funnelcontrols/status;funnelloadbalancers/status;funnelgateways/status;funnelcontrols/finalizers;funnelloadbalancers/finalizers;funnelgateways/finalizers,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:namespace=FUNNEL_MANAGED_NAMESPACE,groups="",resources=secrets;serviceaccounts;services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace=FUNNEL_MANAGED_NAMESPACE,groups=apps,resources=deployments;daemonsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace=FUNNEL_MANAGED_NAMESPACE,groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace=FUNNEL_MANAGED_NAMESPACE,groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:namespace=FUNNEL_MANAGED_NAMESPACE,groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
