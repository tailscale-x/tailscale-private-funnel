package kube

import (
	"context"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
	"testing"
)

func TestStatusPreservesOtherControllersIncludingSameParent(t *testing.T) {
	objects := fixtureObjects()
	f := fake.NewClientBuilder().WithScheme(Scheme()).WithStatusSubresource(&v1.HTTPRoute{}, &v1.Gateway{}, &v1.GatewayClass{}, &networking.Ingress{}).WithObjects(objects...).Build()
	var route v1.HTTPRoute
	key := client.ObjectKey{Namespace: "apps", Name: "checkout"}
	_ = f.Get(context.Background(), key, &route)
	route.Status.Parents = []v1.RouteParentStatus{{ControllerName: "old.example/controller", ParentRef: v1.ParentReference{Name: "edge"}}}
	if e := f.Status().Update(context.Background(), &route); e != nil {
		t.Fatal(e)
	}
	c := &Controller{Client: f, Options: Options{GatewayClass: "funnel", IngressClass: "funnel"}}
	c.Apply = func(ctx context.Context, result Result) (Publication, error) {
		var fresh v1.HTTPRoute
		_ = f.Get(ctx, key, &fresh)
		fresh.Status.Parents = append(fresh.Status.Parents, v1.RouteParentStatus{ControllerName: "other.example/controller", ParentRef: v1.ParentReference{Name: "other-edge"}, Conditions: []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "Accepted", LastTransitionTime: metav1.Now()}}})
		if e := f.Status().Update(ctx, &fresh); e != nil {
			t.Fatal(e)
		}
		return Publication{Hosts: []string{"checkout.example.com"}}, nil
	}
	if _, e := c.Reconcile(context.Background(), reconcile.Request{}); e != nil {
		t.Fatal(e)
	}
	if e := f.Get(context.Background(), key, &route); e != nil {
		t.Fatal(e)
	}
	if len(route.Status.Parents) != 3 {
		t.Fatal(route.Status.Parents)
	}
	have := map[string]bool{}
	for _, p := range route.Status.Parents {
		have[string(p.ControllerName)] = true
	}
	if !have[ControllerName] || !have["other.example/controller"] || !have["old.example/controller"] {
		t.Fatal(have)
	}
}
