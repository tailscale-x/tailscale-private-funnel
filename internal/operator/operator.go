// Package operator reconciles infrastructure; application authorization remains in Funnel control.
package operator

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-logr/logr"
	"log/slog"
	"reflect"
	"strings"
	"time"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	apps "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const Finalizer = "tailscale-private-funnel.io/cleanup"
const OwnerLabel = "tailscale-private-funnel.io/owner-uid"

type Reconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Kind       string
	Namespaces map[string]bool
}

func (r *Reconciler) object() client.Object {
	switch r.Kind {
	case "FunnelControl":
		return &funnel.FunnelControl{}
	case "FunnelLoadBalancer":
		return &funnel.FunnelLoadBalancer{}
	default:
		return &funnel.FunnelGateway{}
	}
}
func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	if !r.Namespaces[request.Namespace] {
		return ctrl.Result{}, nil
	}
	object := r.object()
	if e := r.Get(ctx, request.NamespacedName, object); e != nil {
		return ctrl.Result{}, client.IgnoreNotFound(e)
	}
	if object.GetDeletionTimestamp() != nil {
		return r.cleanup(ctx, object)
	}
	if !controllerutil.ContainsFinalizer(object, Finalizer) {
		controllerutil.AddFinalizer(object, Finalizer)
		if err := r.Update(ctx, object); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Millisecond}, nil
	}
	if control, ok := object.(*funnel.FunnelControl); ok && control.Spec.Transfer != nil {
		return r.reconcileTransfer(ctx, control)
	}
	_, spec, _ := component(object)
	if spec.Image == "" || strings.HasSuffix(spec.Image, ":latest") || strings.HasSuffix(spec.Image, ":dev") {
		return r.condition(ctx, object, false, "InvalidConfiguration", "A pinned component image is required")
	}
	if spec.ControlRef != nil && spec.ManagementURL != "" {
		return r.condition(ctx, object, false, "InvalidConfiguration", "Choose controlRef or managementURL")
	}
	if spec.ControlRef != nil {
		var control funnel.FunnelControl
		if e := r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: spec.ControlRef.Name}, &control); e != nil {
			return r.condition(ctx, object, false, "ControlUnavailable", "Referenced control is unavailable")
		}
		if control.Status.SetupURL == "" {
			return r.condition(ctx, object, false, "ControlUnavailable", "Referenced control has not published its setup URL")
		}
	}
	desired, e := Render(object)
	if e != nil {
		return r.condition(ctx, object, false, "InvalidConfiguration", e.Error())
	}
	mode, _, _ := component(object)
	// Reserve the durable Secret without overwriting any runtime-written data.
	_, _, previous := component(object)
	if previous.Initialized {
		var durable core.Secret
		if err := r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: identityName(object)}, &durable); apierrors.IsNotFound(err) {
			return r.condition(ctx, object, false, "IdentityMissing", "Durable identity is missing; restore it or explicitly delete and recreate this component")
		} else if err != nil {
			return ctrl.Result{}, err
		}
	}
	secret := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: identityName(object), Namespace: object.GetNamespace(), Labels: labels(object)}}
	if spec.IdentitySecretRef != nil {
		var external core.Secret
		if e = r.Get(ctx, client.ObjectKeyFromObject(secret), &external); e != nil {
			return r.condition(ctx, object, false, "IdentityUnavailable", "Referenced identity Secret is unavailable")
		}
	} else if e = r.ensure(ctx, object, secret); e != nil {
		return r.condition(ctx, object, false, "OwnershipConflict", e.Error())
	}
	// A gateway with initialized identity does not need to rerun first-time bootstrap.
	var identity core.Secret
	_ = r.Get(ctx, client.ObjectKeyFromObject(secret), &identity)
	if !previous.Initialized && len(identity.Data["initialized"]) == 0 && len(identity.Data["node-id"]) == 0 {
		if spec.BootstrapSecretRef == nil {
			return r.condition(ctx, object, false, "BootstrapRequired", "An existing bootstrap Secret is required for first initialization")
		}
		var bootstrap core.Secret
		if err := r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: spec.BootstrapSecretRef.Name}, &bootstrap); apierrors.IsNotFound(err) {
			return r.condition(ctx, object, false, "BootstrapRequired", "Waiting for the referenced bootstrap Secret")
		} else if err != nil {
			return ctrl.Result{}, err
		}
	}
	failedBootstrap := ""
	for _, resource := range desired {
		kind := resource.GetObjectKind().GroupVersionKind().Kind
		if mode == "k8s-gateway" {
			if (kind == "Deployment" || kind == "DaemonSet") && len(identity.Data["node-id"]) == 0 {
				continue
			}
			if kind == "DaemonSet" {
				var ca core.Secret
				if err := r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName() + "-broker-tls"}, &ca); apierrors.IsNotFound(err) || len(ca.Data["ca.crt"]) == 0 {
					continue
				} else if err != nil {
					return ctrl.Result{}, err
				}
			}
		}
		if resource.GetObjectKind().GroupVersionKind().Kind == "Job" && mode == "k8s-gateway" && len(identity.Data["node-id"]) > 0 {
			continue
		}
		if e = r.ensure(ctx, object, resource); e != nil {
			return r.condition(ctx, object, false, "ReconciliationFailed", e.Error())
		}
		if kind == "Job" {
			var job batch.Job
			if e := r.Get(ctx, client.ObjectKeyFromObject(resource), &job); e != nil {
				return ctrl.Result{}, e
			}
			for _, condition := range job.Status.Conditions {
				if condition.Type == batch.JobFailed && condition.Status == core.ConditionTrue {
					failedBootstrap = job.Name
				}
			}
		}
	}
	if failedBootstrap != "" {
		return r.condition(ctx, object, false, "BootstrapFailed", "Bootstrap Job "+failedBootstrap+" failed. Inspect its logs, correct the reported prerequisite or replace expired bootstrap credentials, then retry bootstrap.")
	}
	_, _, status := component(object)
	status.Initialized = status.Initialized || len(identity.Data["initialized"]) > 0 || len(identity.Data["node-id"]) > 0
	if status.Initialized && spec.BootstrapSecretRef != nil {
		var bootstrap core.Secret
		if r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: spec.BootstrapSecretRef.Name}, &bootstrap) == nil && bootstrap.Labels[OwnerLabel] == string(object.GetUID()) {
			_ = r.deleteOwned(ctx, &bootstrap)
		}
	}
	status.NodeID = string(identity.Data["node-id"])
	status.SetupURL = string(identity.Data["setup-url"])
	workload := object.GetName() + "-" + mode
	if mode == "k8s-gateway" {
		workload = object.GetName() + "-agent"
	}
	var deployment apps.Deployment
	ready := r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: workload}, &deployment) == nil && deployment.Status.ObservedGeneration >= deployment.Generation && deployment.Status.ReadyReplicas == 1
	if mode == "k8s-gateway" {
		var ds apps.DaemonSet
		if r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName() + "-forwarder"}, &ds) == nil {
			status.ReadyForwarders = ds.Status.NumberReady
			status.DesiredForwarders = ds.Status.DesiredNumberScheduled
			ready = ready && ds.Status.ObservedGeneration >= ds.Generation && ds.Status.NumberReady == ds.Status.DesiredNumberScheduled && ds.Status.DesiredNumberScheduled > 0
		} else {
			ready = false
		}
	}
	if mode == "loadbalancer" {
		var service core.Service
		if r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName() + "-public"}, &service) == nil {
			status.PublicAddresses = nil
			for _, a := range service.Status.LoadBalancer.Ingress {
				if a.IP != "" {
					status.PublicAddresses = append(status.PublicAddresses, a.IP)
				} else if a.Hostname != "" {
					status.PublicAddresses = append(status.PublicAddresses, a.Hostname)
				}
			}
			publicStatus, reason, message := metav1.ConditionFalse, "AddressPending", "Waiting for a public Service address; control validates signed endpoint reports before DNS publication"
			if len(dns.PublicIPv4Endpoints(status.PublicAddresses)) > 0 {
				publicStatus = metav1.ConditionTrue
				reason = "PublicIPv4Allocated"
				message = "Public IPv4 allocated; verify DNS, WAN connectivity and ACME independently"
			} else if len(status.PublicAddresses) > 0 {
				reason = "AddressNotVerified"
				message = "Allocated address is private, invalid or requires hostname resolution by control; public DNS is not proven ready"
			}
			meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: "PublicAddressReady", Status: publicStatus, Reason: reason, Message: message, ObservedGeneration: object.GetGeneration()})
			ready = ready && len(status.PublicAddresses) > 0
		}
	}
	if (mode == "control" && object.(*funnel.FunnelControl).Spec.Database.Type != "postgres") || mode == "loadbalancer" {
		claimName := object.GetName() + "-data"
		if spec.Storage.ExistingClaim != "" {
			claimName = spec.Storage.ExistingClaim
		}
		var claim core.PersistentVolumeClaim
		if e := r.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: claimName}, &claim); e == nil && claim.Status.Phase == core.ClaimPending {
			return r.condition(ctx, object, false, "StoragePending", "PVC "+claimName+" is not bound; verify the StorageClass and selected node storage. Clusters without a default class require an explicit storageClassName.")
		}
	}
	if ready {
		return r.condition(ctx, object, true, "Ready", "Component workload is ready; Funnel reports provide application and public-readiness diagnostics")
	}
	return r.condition(ctx, object, false, "Progressing", "Waiting for bootstrap, component readiness, or public Service allocation")
}
func (r *Reconciler) condition(ctx context.Context, o client.Object, ready bool, reason, message string) (ctrl.Result, error) {
	_, _, status := component(o)
	status.ObservedGeneration = o.GetGeneration()
	value := metav1.ConditionFalse
	if ready {
		value = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: "Ready", Status: value, Reason: reason, Message: message, ObservedGeneration: o.GetGeneration()})
	if e := r.Status().Update(ctx, o); e != nil {
		return ctrl.Result{}, e
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}
func (r *Reconciler) ensure(ctx context.Context, owner, desired client.Object) error {
	if desired.GetNamespace() == owner.GetNamespace() {
		if e := controllerutil.SetControllerReference(owner, desired, r.Scheme); e != nil {
			return e
		}
	}
	existing := desired.DeepCopyObject().(client.Object)
	if e := r.Get(ctx, client.ObjectKeyFromObject(desired), existing); apierrors.IsNotFound(e) {
		return r.Create(ctx, desired, client.FieldOwner("tailscale-private-funnel-k8s-operator"))
	} else if e != nil {
		return e
	}
	if existing.GetLabels()[OwnerLabel] != string(owner.GetUID()) {
		return fmt.Errorf("%s/%s is not owned by this resource", desired.GetNamespace(), desired.GetName())
	}
	// Secrets and PVC identities are never reconstructed from an empty template.
	switch desired.(type) {
	case *core.Secret:
		return nil
	}
	if desired.GetObjectKind().GroupVersionKind().Kind == "Job" {
		var want, have batch.Job
		raw, _ := runtime.DefaultUnstructuredConverter.ToUnstructured(desired)
		if e := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &want); e != nil {
			return e
		}
		raw, _ = runtime.DefaultUnstructuredConverter.ToUnstructured(existing)
		if e := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &have); e != nil {
			return e
		}
		if !apiequality.Semantic.DeepDerivative(want.Spec.Template, have.Spec.Template) {
			if e := r.deleteOwned(ctx, existing, client.PropagationPolicy(metav1.DeletePropagationForeground)); e != nil && !apierrors.IsNotFound(e) {
				return e
			}
			return fmt.Errorf("waiting for old bootstrap Job to stop before replacement")
		}
		return nil
	}
	if desired.GetObjectKind().GroupVersionKind().Kind == "PersistentVolumeClaim" {
		return nil
	}
	if controller := metav1.GetControllerOf(existing); controller != nil && controller.UID != owner.GetUID() {
		return fmt.Errorf("%s is controlled by another resource", desired.GetName())
	}
	return r.Patch(ctx, desired, client.Apply, client.ForceOwnership, client.FieldOwner("tailscale-private-funnel-k8s-operator"))
}
func (r *Reconciler) cleanup(ctx context.Context, o client.Object) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(o, Finalizer) {
		return ctrl.Result{}, nil
	}
	if control, ok := o.(*funnel.FunnelControl); ok && control.Spec.Transfer != nil {
		return r.cleanupTransferred(ctx, control)
	}
	mode, _, status := component(o)
	var durable core.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: o.GetNamespace(), Name: identityName(o)}, &durable); err != nil && (status.Initialized || !apierrors.IsNotFound(err)) {
		return r.condition(ctx, o, false, "CleanupBlocked", "Durable identity is unavailable; cleanup cannot safely complete")
	}
	if mode == "control" {
		var s core.Secret
		if e := r.Get(ctx, types.NamespacedName{Namespace: o.GetNamespace(), Name: identityName(o)}, &s); e != nil && !apierrors.IsNotFound(e) {
			return ctrl.Result{}, e
		}
		if len(s.Data) > 0 && (string(s.Data["active-components"]) != "0" || string(s.Data["pending-node-cleanups"]) != "0") {
			return r.condition(ctx, o, false, "DeletionBlocked", "Control must report zero active enrolled components and zero pending node cleanups before deletion")
		}
	}
	// Stop identity owners before creating a cleanup Job with the same durable identity.
	var deployments apps.DeploymentList
	if e := r.List(ctx, &deployments, client.InNamespace(o.GetNamespace()), client.MatchingLabels{OwnerLabel: string(o.GetUID())}); e != nil {
		return ctrl.Result{}, e
	}
	for i := range deployments.Items {
		d := &deployments.Items[i]
		if d.Spec.Replicas == nil || *d.Spec.Replicas != 0 {
			zero := int32(0)
			d.Spec.Replicas = &zero
			if e := r.Update(ctx, d); e != nil {
				return ctrl.Result{}, e
			}
		}
	}
	var sets apps.DaemonSetList
	if e := r.List(ctx, &sets, client.InNamespace(o.GetNamespace()), client.MatchingLabels{OwnerLabel: string(o.GetUID())}); e != nil {
		return ctrl.Result{}, e
	}
	for i := range sets.Items {
		if e := r.deleteOwned(ctx, &sets.Items[i], client.PropagationPolicy(metav1.DeletePropagationForeground)); e != nil && !apierrors.IsNotFound(e) {
			return ctrl.Result{}, e
		}
	}
	var pods core.PodList
	if e := r.List(ctx, &pods, client.InNamespace(o.GetNamespace()), client.MatchingLabels{OwnerLabel: string(o.GetUID())}); e != nil {
		return ctrl.Result{}, e
	}
	for _, p := range pods.Items {
		if p.Labels["tailscale-private-funnel.io/cleanup"] != "true" && (p.Status.Phase == core.PodRunning || p.Status.Phase == core.PodPending) {
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		}
	}
	var identity core.Secret
	e := r.Get(ctx, types.NamespacedName{Namespace: o.GetNamespace(), Name: identityName(o)}, &identity)
	if e != nil && !apierrors.IsNotFound(e) {
		return ctrl.Result{}, e
	}
	if len(identity.Data) > 0 {
		job, e := r.cleanupJob(o)
		if e != nil {
			return ctrl.Result{}, e
		}
		var existing batch.Job
		e = r.Get(ctx, client.ObjectKeyFromObject(job), &existing)
		if apierrors.IsNotFound(e) {
			if e = r.ensure(ctx, o, job); e != nil {
				return ctrl.Result{}, e
			}
			return r.condition(ctx, o, false, "CleanupPending", "Revoking node identities, advertisements, and owned DNS")
		}
		if e != nil {
			return ctrl.Result{}, e
		}
		if existing.Status.Succeeded == 0 {
			if existing.Status.Failed > 0 {
				if time.Since(existing.CreationTimestamp.Time) > time.Minute {
					_ = r.deleteOwned(ctx, &existing, client.PropagationPolicy(metav1.DeletePropagationForeground))
				}
				return r.condition(ctx, o, false, "CleanupFailed", "Remote cleanup failed; durable state is retained and cleanup will retry")
			}
			return ctrl.Result{RequeueAfter: 3 * time.Second}, nil
		}
	}
	// Delete only objects bearing this exact CR UID, including runtime-created child identities.
	desired, e := Render(o)
	if e != nil {
		return ctrl.Result{}, e
	}
	for _, v := range desired {
		var u unstructured.Unstructured
		u.SetGroupVersionKind(v.GetObjectKind().GroupVersionKind())
		if e = r.Get(ctx, client.ObjectKeyFromObject(v), &u); e == nil && u.GetLabels()[OwnerLabel] == string(o.GetUID()) {
			if e = r.deleteOwned(ctx, &u, client.PropagationPolicy(metav1.DeletePropagationForeground)); e != nil && !apierrors.IsNotFound(e) {
				return ctrl.Result{}, e
			}
		} else if e != nil && !apierrors.IsNotFound(e) {
			return ctrl.Result{}, e
		}
	}
	for _, list := range []client.ObjectList{&core.SecretList{}, &core.PersistentVolumeClaimList{}, &batch.JobList{}, &coordination.LeaseList{}} {
		if e = r.List(ctx, list, client.InNamespace(o.GetNamespace()), client.MatchingLabels{OwnerLabel: string(o.GetUID())}); e != nil {
			return ctrl.Result{}, e
		}
		items, e := meta.ExtractList(list)
		if e != nil {
			return ctrl.Result{}, e
		}
		for _, v := range items {
			if e = r.deleteOwned(ctx, v.(client.Object), client.PropagationPolicy(metav1.DeletePropagationForeground)); e != nil && !apierrors.IsNotFound(e) {
				return ctrl.Result{}, e
			}
		}
	}
	controllerutil.RemoveFinalizer(o, Finalizer)
	return ctrl.Result{}, r.Update(ctx, o)
}
func (r *Reconciler) cleanupJob(o client.Object) (*batch.Job, error) {
	desired, e := Render(o)
	if e != nil {
		return nil, e
	}
	for _, v := range desired {
		if v.GetObjectKind().GroupVersionKind().Kind != "Deployment" {
			continue
		}
		raw, e := runtime.DefaultUnstructuredConverter.ToUnstructured(v)
		if e != nil {
			return nil, e
		}
		var deployment apps.Deployment
		if e = runtime.DefaultUnstructuredConverter.FromUnstructured(raw, &deployment); e != nil {
			return nil, e
		}
		pod := deployment.Spec.Template
		pod.Labels = labels(o)
		pod.Labels["tailscale-private-funnel.io/cleanup"] = "true"
		pod.Spec.RestartPolicy = core.RestartPolicyNever
		pod.Spec.InitContainers = nil
		if len(pod.Spec.Containers) != 1 {
			return nil, errors.New("unexpected cleanup workload")
		}
		pod.Spec.Containers[0].Args = append(pod.Spec.Containers[0].Args, "--cleanup-only")
		pod.Spec.Containers[0].ReadinessProbe = nil
		pod.Spec.Containers[0].LivenessProbe = nil
		pod.Spec.Containers[0].StartupProbe = nil
		zero := int32(0)
		return &batch.Job{ObjectMeta: metav1.ObjectMeta{Name: o.GetName() + "-cleanup", Namespace: o.GetNamespace(), Labels: labels(o)}, Spec: batch.JobSpec{BackoffLimit: &zero, Template: pod}}, nil
	}
	return nil, errors.New("cleanup workload unavailable")
}

func Run(ctx context.Context, namespaces []string, probe string) error {
	ctrl.SetLogger(logr.FromSlogHandler(slog.Default().Handler()))
	if len(namespaces) == 0 {
		return errors.New("at least one --managed-namespace is required")
	}
	scheme := runtime.NewScheme()
	_ = core.AddToScheme(scheme)
	_ = apps.AddToScheme(scheme)
	_ = batch.AddToScheme(scheme)
	_ = coordination.AddToScheme(scheme)
	_ = funnel.AddToScheme(scheme)
	scoped := map[string]cache.Config{}
	allowed := map[string]bool{}
	for _, ns := range namespaces {
		if strings.TrimSpace(ns) == "" {
			return errors.New("empty managed namespace")
		}
		scoped[ns] = cache.Config{}
		allowed[ns] = true
	}
	manager, e := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{Scheme: scheme, Cache: cache.Options{DefaultNamespaces: scoped}, HealthProbeBindAddress: probe, Metrics: metrics.Options{BindAddress: "0"}})
	if e != nil {
		return e
	}
	for _, kind := range []string{"FunnelControl", "FunnelLoadBalancer", "FunnelGateway"} {
		r := &Reconciler{Client: manager.GetClient(), Scheme: scheme, Kind: kind, Namespaces: allowed}
		if e = ctrl.NewControllerManagedBy(manager).For(r.object(), builder.WithPredicates(predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() || !reflect.DeepEqual(e.ObjectOld.GetFinalizers(), e.ObjectNew.GetFinalizers()) || !e.ObjectOld.GetDeletionTimestamp().Equal(e.ObjectNew.GetDeletionTimestamp())
		}})).Owns(&apps.Deployment{}).Owns(&apps.DaemonSet{}).Owns(&batch.Job{}).Owns(&core.Secret{}).Owns(&core.Service{}).Owns(&core.PersistentVolumeClaim{}).Complete(r); e != nil {
			return e
		}
	}
	_ = manager.AddHealthzCheck("healthz", healthz.Ping)
	_ = manager.AddReadyzCheck("readyz", healthz.Ping)
	return manager.Start(ctx)
}

// Fence deletion against replacement or ownership changes after a cached read.
func (r *Reconciler) deleteOwned(ctx context.Context, o client.Object, options ...client.DeleteOption) error {
	uid := o.GetUID()
	if uid == "" {
		return fmt.Errorf("refusing deletion without a verified Kubernetes UID")
	}
	version := o.GetResourceVersion()
	pre := client.Preconditions{UID: &uid}
	if version != "" {
		pre.ResourceVersion = &version
	}
	options = append(options, pre)
	return r.Client.Delete(ctx, o, options...)
}
