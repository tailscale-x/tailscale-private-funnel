package operator

import (
	"context"
	"strings"
	"testing"

	funnel "github.com/tailscale-x/tailscale-private-funnel/api/kubernetes/v1alpha1"
	apps "k8s.io/api/apps/v1"
	batch "k8s.io/api/batch/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubescheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	core.AddToScheme(s)
	apps.AddToScheme(s)
	funnel.AddToScheme(s)
	return s
}
func testControl() *funnel.FunnelControl {
	return &funnel.FunnelControl{ObjectMeta: metav1.ObjectMeta{Name: "control", Namespace: "installation", UID: "unique-id", Finalizers: []string{Finalizer}}, Spec: funnel.ControlSpec{ComponentSpec: funnel.ComponentSpec{Image: "ghcr.io/tailscale-x/tailscale-private-funnel:dev-test", Installation: "main", Hostname: "control"}, Tailnet: "example.ts.net"}}
}
func TestRenderComponentContracts(t *testing.T) {
	c := testControl()
	for _, o := range []client.Object{c, &funnel.FunnelLoadBalancer{ObjectMeta: c.ObjectMeta, Spec: funnel.LoadBalancerSpec{ComponentSpec: c.Spec.ComponentSpec}}, &funnel.FunnelGateway{ObjectMeta: c.ObjectMeta, Spec: funnel.GatewaySpec{ComponentSpec: c.Spec.ComponentSpec, GatewayClass: "funnel-test", IngressClass: "funnel-test"}}} {
		objects, e := Render(o)
		if e != nil {
			t.Fatal(e)
		}
		deployment, ds := 0, 0
		for _, v := range objects {
			u := v.(*unstructured.Unstructured)
			if u.GetLabels()[OwnerLabel] != "unique-id" {
				t.Fatal("missing exact ownership")
			}
			switch u.GetKind() {
			case "Deployment":
				deployment++
				var parsed apps.Deployment
				if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &parsed); err != nil {
					t.Fatal("invalid workload", err)
				}
				strategy, _, _ := unstructured.NestedString(u.Object, "spec", "strategy", "type")
				if strategy != "Recreate" {
					t.Fatal("identity could overlap")
				}
				containers, _, _ := unstructured.NestedSlice(u.Object, "spec", "template", "spec", "containers")
				for _, raw := range containers {
					args := raw.(map[string]any)["args"].([]any)
					if len(args) < 4 || args[0] != "k8s" || args[1] != "runtime" {
						t.Fatal("old runtime", args)
					}
				}
			case "DaemonSet":
				ds++
			case "GatewayClass":
				ref, _, _ := unstructured.NestedString(u.Object, "spec", "parametersRef", "name")
				if ref != o.GetName() {
					t.Fatal("missing infrastructure reference")
				}
			}
		}
		if deployment != 1 {
			t.Fatal("missing singleton")
		}
		if _, ok := o.(*funnel.FunnelGateway); ok && ds != 1 {
			t.Fatal("missing forwarder")
		}
	}
}
func TestForeignResourceNeverAdopted(t *testing.T) {
	o := testControl()
	s := testScheme()
	existing := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "control-identity", Namespace: o.Namespace}, Data: map[string][]byte{"private": []byte("preserve")}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()
	r := Reconciler{Client: c, Scheme: s}
	if e := r.ensure(t.Context(), o, &core.Secret{ObjectMeta: existing.ObjectMeta}); e == nil || !strings.Contains(e.Error(), "not owned") {
		t.Fatal(e)
	}
}
func TestReservedSecretPreservesRuntimeData(t *testing.T) {
	o := testControl()
	s := testScheme()
	secret := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "control-identity", Namespace: o.Namespace, Labels: labels(o)}, Data: map[string][]byte{"private": []byte("preserve")}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
	r := Reconciler{Client: c, Scheme: s}
	if e := r.ensure(t.Context(), o, &core.Secret{ObjectMeta: secret.ObjectMeta}); e != nil {
		t.Fatal(e)
	}
	var got core.Secret
	c.Get(t.Context(), client.ObjectKeyFromObject(secret), &got)
	if string(got.Data["private"]) != "preserve" {
		t.Fatal("runtime state overwritten")
	}
}
func TestMissingIdentityFailsClosed(t *testing.T) {
	o := testControl()
	o.Status.Initialized = true
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(o).WithObjects(o).Build()
	r := Reconciler{Client: c, Scheme: s, Kind: "FunnelControl", Namespaces: map[string]bool{o.Namespace: true}}
	if _, e := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(o)}); e != nil {
		t.Fatal(e)
	}
	var got funnel.FunnelControl
	c.Get(t.Context(), client.ObjectKeyFromObject(o), &got)
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "IdentityMissing" {
		t.Fatal(got.Status)
	}
	var secret core.Secret
	if e := c.Get(t.Context(), types.NamespacedName{Namespace: o.Namespace, Name: identityName(o)}, &secret); e == nil {
		t.Fatal("identity recreated")
	}
}
func TestUnmanagedNamespaceDoesNothing(t *testing.T) {
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).Build()
	r := Reconciler{Client: c, Scheme: s, Kind: "FunnelControl", Namespaces: map[string]bool{"allowed": true}}
	if _, e := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "other", Name: "control"}}); e != nil {
		t.Fatal(e)
	}
}

func TestCleanupRetainsMissingInitializedIdentity(t *testing.T) {
	o := testControl()
	o.Status.Initialized = true
	now := metav1.Now()
	o.DeletionTimestamp = &now
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(o).WithObjects(o).Build()
	r := Reconciler{Client: c, Scheme: s, Kind: "FunnelControl", Namespaces: map[string]bool{o.Namespace: true}}
	if _, e := r.cleanup(t.Context(), o); e != nil {
		t.Fatal(e)
	}
	var got funnel.FunnelControl
	c.Get(t.Context(), client.ObjectKeyFromObject(o), &got)
	if len(got.Finalizers) == 0 || got.Status.Conditions[0].Reason != "CleanupBlocked" {
		t.Fatal("lost finalizer", got.Status)
	}
}

func TestFailedGatewayBootstrapHasActionableCondition(t *testing.T) {
	s := testScheme()
	if err := kubescheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	base := testControl()
	o := &funnel.FunnelGateway{ObjectMeta: base.ObjectMeta, Spec: funnel.GatewaySpec{ComponentSpec: base.Spec.ComponentSpec, GatewayClass: "bootstrap-test", IngressClass: "bootstrap-test"}}
	o.Spec.BootstrapSecretRef = &core.LocalObjectReference{Name: "bootstrap"}
	o.Generation = 1
	secret := &core.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap", Namespace: o.Namespace}}
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(o, &batch.Job{}).WithObjects(o, secret).Build()
	r := Reconciler{Client: c, Scheme: s, Kind: "FunnelGateway", Namespaces: map[string]bool{o.Namespace: true}}
	key := client.ObjectKeyFromObject(o)
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	var jobs batch.JobList
	if err := c.List(t.Context(), &jobs, client.InNamespace(o.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs=%d", len(jobs.Items))
	}
	job := &jobs.Items[0]
	job.Status.Conditions = []batch.JobCondition{{Type: batch.JobFailed, Status: core.ConditionTrue, Reason: "BackoffLimitExceeded"}}
	if err := c.Status().Update(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	var got funnel.FunnelGateway
	if err := c.Get(t.Context(), key, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Status.Conditions) != 1 || got.Status.Conditions[0].Reason != "BootstrapFailed" || !strings.Contains(got.Status.Conditions[0].Message, "Inspect its logs") {
		t.Fatalf("status=%+v", got.Status)
	}
}
