package kube

import (
	"context"
	"encoding/json"
	"github.com/go-logr/logr"
	"k8s.io/client-go/util/retry"
	"log/slog"
	"reflect"
	"sort"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	networking "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
	v1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

type Publication struct {
	Hosts     []string
	Addresses []string
}
type Apply func(context.Context, Result) (Publication, error)
type Controller struct {
	client.Client
	Reader   client.Reader
	Options  Options
	Apply    Apply
	Recorder record.EventRecorder
	mu       sync.Mutex
}

func Scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = core.AddToScheme(s)
	_ = apps.AddToScheme(s)
	_ = coordination.AddToScheme(s)
	_ = networking.AddToScheme(s)
	_ = discovery.AddToScheme(s)
	_ = v1.Install(s)
	_ = v1beta1.Install(s)
	return s
}
func Run(ctx context.Context, kubeconfig, probe string, options Options, apply Apply) error {
	ctrl.SetLogger(logr.FromSlogHandler(slog.Default().Handler()))
	cfg, err := ctrl.GetConfig()
	if kubeconfig != "" {
		cfg, err = loadKubeconfig(kubeconfig)
	}
	if err != nil {
		return err
	}
	cacheOptions := cache.Options{}
	if len(options.Namespaces) > 0 {
		cacheOptions.DefaultNamespaces = map[string]cache.Config{}
		for _, ns := range options.Namespaces {
			cacheOptions.DefaultNamespaces[ns] = cache.Config{}
		}
	}
	manager, err := ctrl.NewManager(cfg, ctrl.Options{Cache: cacheOptions, Scheme: Scheme(), Metrics: metrics.Options{BindAddress: "0"}, HealthProbeBindAddress: probe})
	if err != nil {
		return err
	}
	controller := &Controller{Client: manager.GetClient(), Reader: manager.GetAPIReader(), Options: options, Apply: apply, Recorder: manager.GetEventRecorderFor(options.controllerName())}
	enqueue := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: "all-routes"}}}
	})
	changes := predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
		if pod, ok := e.ObjectNew.(*core.Pod); ok {
			old := e.ObjectOld.(*core.Pod)
			return !reflect.DeepEqual(old.Status, pod.Status) || !reflect.DeepEqual(old.Labels, pod.Labels)
		}
		if _, ok := e.ObjectNew.(*discovery.EndpointSlice); ok {
			return true
		}
		return e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() || !reflect.DeepEqual(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations()) || !reflect.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels())
	}}
	builder := ctrl.NewControllerManagedBy(manager).Named("private-funnel-http").WithEventFilter(changes).For(&v1.HTTPRoute{})
	for _, object := range []client.Object{&v1.Gateway{}, &v1.GatewayClass{}, &v1beta1.ReferenceGrant{}, &networking.Ingress{}, &networking.IngressClass{}, &core.Service{}, &core.Pod{}, &discovery.EndpointSlice{}, &core.Namespace{}, &core.Node{}} {
		builder = builder.Watches(object, enqueue)
	}
	if err = builder.Complete(controller); err != nil {
		return err
	}
	_ = manager.AddHealthzCheck("healthz", healthz.Ping)
	_ = manager.AddReadyzCheck("readyz", healthz.Ping)
	return manager.Start(ctx)
}
func (c *Controller) Reconcile(ctx context.Context, _ reconcile.Request) (ctrl.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, err := Translate(ctx, c.Client, c.Options)
	if err != nil {
		return ctrl.Result{}, err
	}
	published, applyErr := c.Apply(ctx, result)
	set := map[string]bool{}
	for _, h := range published.Hosts {
		set[h] = true
	}
	for _, object := range result.Objects {
		id := resourceID(object)
		problem := result.Problems[id]
		if applyErr != nil && problem == "" {
			problem = applyErr.Error()
		}
		programmed := problem == ""
		seen := false
		for _, host := range result.Hosts {
			for _, ref := range host.ResourceIDs {
				if ref == id {
					seen = true
					programmed = programmed && set[host.Hostname]
				}
			}
		}
		if !seen {
			programmed = false
		}
		if problem == "" && !programmed {
			problem = "Awaiting authorized backend, Tailscale Service approval, and load-balancer acknowledgment"
		}
		accepted := result.Problems[id] == ""
		conditions := []metav1.Condition{{Type: "Accepted", Status: boolStatus(accepted), Reason: boolReason(accepted, "Accepted", "UnsupportedValue"), Message: message(result.Problems[id], "Supported HTTP configuration"), ObservedGeneration: object.GetGeneration(), LastTransitionTime: metav1.Now()}, {Type: "ResolvedRefs", Status: boolStatus(accepted), Reason: boolReason(accepted, "ResolvedRefs", "RefNotPermitted"), Message: message(result.Problems[id], "Backend references resolved"), ObservedGeneration: object.GetGeneration(), LastTransitionTime: metav1.Now()}, {Type: "Programmed", Status: boolStatus(programmed), Reason: boolReason(programmed, "Programmed", "Pending"), Message: message(problem, "Public load balancer acknowledged configuration"), ObservedGeneration: object.GetGeneration(), LastTransitionTime: metav1.Now()}}
		switch o := object.(type) {
		case *v1.HTTPRoute:
			owned := []v1.RouteParentStatus{}
			for _, p := range o.Status.Parents {
				if p.ControllerName != v1.GatewayController(c.Options.controllerName()) {
					owned = append(owned, p)
				}
			}
			for _, parent := range result.Parents[id] {
				p := v1.RouteParentStatus{ParentRef: parent, ControllerName: v1.GatewayController(c.Options.controllerName())}
				for _, old := range o.Status.Parents {
					if old.ControllerName == p.ControllerName && reflect.DeepEqual(old.ParentRef, parent) {
						p.Conditions = old.Conditions
					}
				}
				for _, cond := range conditions {
					meta.SetStatusCondition(&p.Conditions, cond)
				}
				owned = append(owned, p)
			}
			o.Status.Parents = owned
		case *v1.Gateway:
			o.Status.Addresses = nil
			for _, ip := range published.Addresses {
				kind := v1.IPAddressType
				o.Status.Addresses = append(o.Status.Addresses, v1.GatewayStatusAddress{Type: &kind, Value: ip})
			}
			// Listener validation is independent from whether a route currently exists.
			valid := true
			for _, l := range o.Spec.Listeners {
				valid = valid && l.Protocol == "HTTP" && l.Port == 80 && l.TLS == nil
			}
			conditions[0].Status = boolStatus(valid)
			conditions[0].Reason = boolReason(valid, "Accepted", "UnsupportedProtocol")
			for _, cond := range []metav1.Condition{conditions[0], conditions[2]} {
				meta.SetStatusCondition(&o.Status.Conditions, cond)
			}
			previousListeners := o.Status.Listeners
			o.Status.Listeners = nil
			for _, l := range o.Spec.Listeners {
				count := int32(0)
				for _, r := range result.Objects {
					if route, ok := r.(*v1.HTTPRoute); ok && result.Problems[resourceID(route)] == "" {
						for _, p := range result.Parents[resourceID(route)] {
							ns := route.Namespace
							if p.Namespace != nil {
								ns = string(*p.Namespace)
							}
							if ns == o.Namespace && p.Name == v1.ObjectName(o.Name) && (p.SectionName == nil || *p.SectionName == l.Name) && (p.Port == nil || *p.Port == l.Port) {
								count++
							}
						}
					}
				}
				listenerConditions := []metav1.Condition{}
				for _, old := range previousListeners {
					if old.Name == l.Name {
						listenerConditions = old.Conditions
					}
				}
				for _, condition := range conditions {
					meta.SetStatusCondition(&listenerConditions, condition)
				}
				o.Status.Listeners = append(o.Status.Listeners, v1.ListenerStatus{Name: l.Name, SupportedKinds: []v1.RouteGroupKind{{Kind: "HTTPRoute"}}, AttachedRoutes: count, Conditions: listenerConditions})
			}
		case *v1.GatewayClass:
			meta.SetStatusCondition(&o.Status.Conditions, metav1.Condition{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted", Message: "Private Funnel HTTP subset; no conformance claim", ObservedGeneration: o.Generation, LastTransitionTime: metav1.Now()})
		case *networking.Ingress:
			// Ingress has no standard conditions; Events carry rejection/readiness.
			o.Status.LoadBalancer.Ingress = nil
			for _, ip := range published.Addresses {
				o.Status.LoadBalancer.Ingress = append(o.Status.LoadBalancer.Ingress, networking.IngressLoadBalancerIngress{IP: ip})
			}
		}
		if err := c.patchStatus(ctx, object); err != nil {
			return ctrl.Result{}, err
		}
		// Cluster-scoped objects have conditions; do not emit Events into an unwatched default namespace.
		if c.Recorder != nil && !programmed && object.GetNamespace() != "" {
			c.Recorder.Event(object, core.EventTypeNormal, "Pending", problem)
		}
	}
	if applyErr != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}
func boolStatus(v bool) metav1.ConditionStatus {
	if v {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}
func boolReason(v bool, a, b string) string {
	if v {
		return a
	}
	return b
}
func message(v, otherwise string) string {
	if v != "" {
		if len(v) > 1000 {
			return v[:1000]
		}
		return v
	}
	return otherwise
}

// Merge against the API server's latest status, with a resource-version guard.
// Independent controllers may update the same HTTPRoute while Apply does I/O.
func (c *Controller) patchStatus(ctx context.Context, desired client.Object) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current := desired.DeepCopyObject().(client.Object)
		reader := c.Reader
		if reader == nil {
			reader = c.Client
		}
		if e := reader.Get(ctx, client.ObjectKeyFromObject(desired), current); e != nil {
			return e
		}
		before := current.DeepCopyObject().(client.Object)
		switch out := current.(type) {
		case *v1.HTTPRoute:
			want := desired.(*v1.HTTPRoute)
			owned := v1.GatewayController(c.Options.controllerName())
			merged := []v1.RouteParentStatus{}
			for _, p := range out.Status.Parents {
				if p.ControllerName != owned {
					merged = append(merged, p)
				}
			}
			for _, p := range want.Status.Parents {
				if p.ControllerName == owned {
					merged = append(merged, p)
				}
			}
			sort.Slice(merged, func(i, j int) bool {
				a, _ := json.Marshal(merged[i].ParentRef)
				b, _ := json.Marshal(merged[j].ParentRef)
				return string(merged[i].ControllerName)+string(a) < string(merged[j].ControllerName)+string(b)
			})
			out.Status.Parents = merged
		case *v1.Gateway:
			out.Status = desired.(*v1.Gateway).Status
		case *v1.GatewayClass:
			out.Status = desired.(*v1.GatewayClass).Status
		case *networking.Ingress:
			out.Status = desired.(*networking.Ingress).Status
		}
		return c.Status().Patch(ctx, current, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}
