package kube

import (
	"context"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"testing"

	core "k8s.io/api/core/v1"
	networking "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
	v1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func TestGatewayProgrammedTracksAttachedRoutePublication(t *testing.T) {
	objects := fixtureObjects()
	// A second parent has no listener matching this hostname. It must stay pending.
	other := objects[2].(*v1.Gateway).DeepCopy()
	other.Name = "other"
	other.Spec.Listeners[0].Hostname = pointer(v1.Hostname("other.example.com"))
	objects = append(objects, other)
	objects[5].(*v1.HTTPRoute).Spec.ParentRefs = append(objects[5].(*v1.HTTPRoute).Spec.ParentRefs, v1.ParentReference{Name: "other"})
	f := fake.NewClientBuilder().WithScheme(Scheme()).WithStatusSubresource(&v1.HTTPRoute{}, &v1.Gateway{}, &v1.GatewayClass{}, &networking.Ingress{}).WithObjects(objects...).Build()
	published := false
	c := &Controller{Client: f, Options: Options{GatewayClass: "funnel", IngressClass: "funnel"}, Apply: func(_ context.Context, r Result) (Publication, error) {
		if published {
			return Publication{Resources: nodeconfig.KubernetesResourceRevisions(r.Hosts)}, nil
		}
		return Publication{}, nil
	}}
	for _, ready := range []bool{false, true, false} {
		published = ready
		if _, err := c.Reconcile(context.Background(), reconcile.Request{}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"edge", "other"} {
			var gw v1.Gateway
			if err := f.Get(context.Background(), client.ObjectKey{Namespace: "apps", Name: name}, &gw); err != nil {
				t.Fatal(err)
			}
			cond := meta.FindStatusCondition(gw.Status.Conditions, "Programmed")
			want := metav1.ConditionFalse
			if name == "edge" && ready {
				want = metav1.ConditionTrue
			}
			if cond == nil || cond.Status != want {
				t.Fatalf("published=%v gateway=%s: %#v", ready, name, cond)
			}
		}
	}
}

func TestPublicationConflictIsResourceScoped(t *testing.T) {
	objects := fixtureObjects()
	good := objects[5].(*v1.HTTPRoute).DeepCopy()
	good.Name = "unrelated"
	good.Spec.Hostnames = []v1.Hostname{"healthy.example.com"}
	objects = append(objects, good)
	f := fake.NewClientBuilder().WithScheme(Scheme()).WithStatusSubresource(&v1.HTTPRoute{}, &v1.Gateway{}, &v1.GatewayClass{}, &networking.Ingress{}).WithObjects(objects...).Build()
	c := &Controller{Client: f, Options: Options{GatewayClass: "funnel", IngressClass: "funnel"}, Apply: func(ctx context.Context, r Result) (Publication, error) {
		return Publication{Resources: nodeconfig.KubernetesResourceRevisions(r.Hosts), Problems: map[string]string{resourceID(objects[5]): "Hostname conflict"}}, nil
	}}
	if _, e := c.Reconcile(t.Context(), reconcile.Request{}); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"checkout", "unrelated"} {
		var r v1.HTTPRoute
		if e := f.Get(t.Context(), client.ObjectKey{Namespace: "apps", Name: name}, &r); e != nil {
			t.Fatal(e)
		}
		cond := meta.FindStatusCondition(r.Status.Parents[0].Conditions, "Programmed")
		want := metav1.ConditionTrue
		if name == "checkout" {
			want = metav1.ConditionFalse
		}
		if cond == nil || cond.Status != want {
			t.Fatal(name, cond)
		}
	}
}

func TestTemporaryRoutePublicationIsPerResourceRevision(t *testing.T) {
	objects := fixtureObjects()
	harbor := objects[5].(*v1.HTTPRoute)
	harbor.Spec.Rules[0].Matches[0].Path.Value = pointer("/")
	solver := harbor.DeepCopy()
	solver.Name = "solver"
	solver.Namespace = "challenge"
	solver.UID = "solver-uid"
	solver.Generation = 1
	solver.Spec.ParentRefs = []v1.ParentReference{{Name: "edge", Namespace: pointer(v1.Namespace("apps"))}}
	solver.Spec.Rules[0].Matches[0].Path = &v1.HTTPPathMatch{Type: pointer(v1.PathMatchExact), Value: pointer("/.well-known/acme-challenge/token")}
	// Use the same verified backend here; forwarding readiness is covered by the
	// control lifecycle test. This isolates resource ownership after sorting.
	solver.Spec.Rules[0].BackendRefs[0].Namespace = pointer(v1.Namespace("apps"))
	grant := &v1beta1.ReferenceGrant{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "solver"}, Spec: v1beta1.ReferenceGrantSpec{From: []v1beta1.ReferenceGrantFrom{{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "challenge"}}, To: []v1beta1.ReferenceGrantTo{{Group: "", Kind: "Service"}}}}
	objects = append(objects, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "challenge"}}, solver, grant)
	// fixture Gateway normally accepts only its own namespace.
	objects[2].(*v1.Gateway).Spec.Listeners[0].AllowedRoutes = &v1.AllowedRoutes{Namespaces: &v1.RouteNamespaces{From: pointer(v1.NamespacesFromAll)}}
	f := fake.NewClientBuilder().WithScheme(Scheme()).WithStatusSubresource(&v1.HTTPRoute{}, &v1.Gateway{}, &v1.GatewayClass{}).WithObjects(objects...).Build()
	options := Options{GatewayClass: "funnel", IngressClass: "funnel"}
	result, e := Translate(t.Context(), f, options)
	if e != nil {
		t.Fatal(e)
	}
	if len(result.Problems) != 0 || len(result.Hosts) != 1 || len(result.Hosts[0].Rules) != 2 {
		t.Fatalf("translation failed: %+v", result)
	}
	if result.Hosts[0].ResourceIDs[0] != resourceID(solver) || result.Hosts[0].ResourceIDs[1] != resourceID(harbor) {
		t.Fatal("sorting detached rule ownership", result.Hosts[0])
	}
	all := nodeconfig.KubernetesResourceRevisions(result.Hosts)
	publication := map[string]string{resourceID(harbor): all[resourceID(harbor)]}
	c := &Controller{Client: f, Options: options, Apply: func(context.Context, Result) (Publication, error) { return Publication{Resources: publication}, nil }}
	verify := func(name, ns string, want metav1.ConditionStatus) {
		t.Helper()
		var r v1.HTTPRoute
		if e := f.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: name}, &r); e != nil {
			t.Fatal(e)
		}
		cond := meta.FindStatusCondition(r.Status.Parents[0].Conditions, "Programmed")
		if cond == nil || cond.Status != want || cond.ObservedGeneration != r.Generation {
			t.Fatalf("%s: %+v", name, cond)
		}
	}
	if _, e = c.Reconcile(t.Context(), reconcile.Request{}); e != nil {
		t.Fatal(e)
	}
	verify(harbor.Name, harbor.Namespace, metav1.ConditionTrue)
	verify(solver.Name, solver.Namespace, metav1.ConditionFalse)
	publication = all
	if _, e = c.Reconcile(t.Context(), reconcile.Request{}); e != nil {
		t.Fatal(e)
	}
	verify(solver.Name, solver.Namespace, metav1.ConditionTrue)
	var current v1.HTTPRoute
	key := client.ObjectKeyFromObject(solver)
	if e = f.Get(t.Context(), key, &current); e != nil {
		t.Fatal(e)
	}
	current.Generation++
	current.Spec.Rules[0].Matches[0].Path.Value = pointer("/.well-known/acme-challenge/replacement")
	if e = f.Update(t.Context(), &current); e != nil {
		t.Fatal(e)
	}
	// Acknowledgment of the same hostname/UID's old revision is insufficient.
	if _, e = c.Reconcile(t.Context(), reconcile.Request{}); e != nil {
		t.Fatal(e)
	}
	verify(harbor.Name, harbor.Namespace, metav1.ConditionTrue)
	verify(solver.Name, solver.Namespace, metav1.ConditionFalse)
	if e = f.Delete(t.Context(), &current); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Reconcile(t.Context(), reconcile.Request{}); e != nil {
		t.Fatal(e)
	}
	verify(harbor.Name, harbor.Namespace, metav1.ConditionTrue)
}
