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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	core.AddToScheme(s)
	batch.AddToScheme(s)
	apps.AddToScheme(s)
	funnel.AddToScheme(s)
	return s
}
func testControl() *funnel.FunnelControl {
	return &funnel.FunnelControl{ObjectMeta: metav1.ObjectMeta{Name: "control", Namespace: "installation", UID: "unique-id", Finalizers: []string{Finalizer}}, Spec: funnel.ControlSpec{ComponentSpec: funnel.ComponentSpec{Image: "ghcr.io/tailscale-x/tailscale-private-funnel:dev-test", Installation: "main", Hostname: "control", KeyIssuerRef: funnel.KeyIssuerReference{Name: "issuer"}}, UsersRef: core.LocalObjectReference{Name: "users"}, ServicesRef: core.LocalObjectReference{Name: "services"}, Tailnet: "example.ts.net"}}
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
					env, _, _ := unstructured.NestedSlice(raw.(map[string]any), "env")
					seen := map[string]bool{}
					for _, item := range env {
						name := item.(map[string]any)["name"].(string)
						if seen[name] {
							t.Fatalf("duplicate workload environment variable %s", name)
						}
						seen[name] = true
					}
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
	o.Spec.KeyIssuerRef = funnel.KeyIssuerReference{Name: "issuer"}
	o.Status.Initialized = true
	s := testScheme()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(o).WithObjects(o).Build()
	r := Reconciler{Client: c, Scheme: s, Kind: "FunnelControl", Namespaces: map[string]bool{o.Namespace: true}}
	if _, e := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(o)}); e != nil {
		t.Fatal(e)
	}
	var got funnel.FunnelControl
	c.Get(t.Context(), client.ObjectKeyFromObject(o), &got)
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "EncryptionMaterialMissing" {
		t.Fatal(got.Status)
	}
	var secret core.Secret
	if e := c.Get(t.Context(), types.NamespacedName{Namespace: o.Namespace, Name: materialName(o)}, &secret); e == nil {
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

func TestLogicalIdentitySurvivesResourceAndPodReplacement(t *testing.T) {
	c := testControl()
	c.Spec.ComponentID = "logical-gateway"
	g := &funnel.FunnelGateway{ObjectMeta: c.ObjectMeta, Spec: funnel.GatewaySpec{ComponentSpec: c.Spec.ComponentSpec, RoutingID: "routing-installation", GatewayClass: "test"}}
	for _, uid := range []types.UID{"first-cr", "replacement-cr"} {
		g.UID = uid
		objects, err := Render(g)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, object := range objects {
			u := object.(*unstructured.Unstructured)
			if u.GetKind() != "Deployment" {
				continue
			}
			var d apps.Deployment
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, &d); err != nil {
				t.Fatal(err)
			}
			vars := map[string]string{}
			for _, e := range d.Spec.Template.Spec.Containers[0].Env {
				vars[e.Name] = e.Value
			}
			if vars["FUNNEL_COMPONENT_ID"] != "logical-gateway" || vars["FUNNEL_ROUTING_ID"] != "routing-installation" {
				t.Fatal("logical routing changed with infrastructure identity", vars)
			}
			found = true
		}
		if !found {
			t.Fatal("no deployment")
		}
	}
}
