package kube

import (
	"context"
	"testing"

	networking "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
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
	c := &Controller{Client: f, Options: Options{GatewayClass: "funnel", IngressClass: "funnel"}, Apply: func(context.Context, Result) (Publication, error) {
		if published {
			return Publication{Hosts: []string{"checkout.example.com"}}, nil
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
		return Publication{Hosts: []string{"checkout.example.com", "healthy.example.com"}, Problems: map[string]string{resourceID(objects[5]): "Hostname conflict"}}, nil
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
