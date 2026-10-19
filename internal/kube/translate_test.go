package kube

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"

	core "k8s.io/api/core/v1"
	discovery "k8s.io/api/discovery/v1"
	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
	v1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func pointer[T any](v T) *T { return &v }
func fixtureObjects() []client.Object {
	return []client.Object{
		&v1.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "funnel"}, Spec: v1.GatewayClassSpec{ControllerName: ControllerName}},
		&networking.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "funnel"}, Spec: networking.IngressClassSpec{Controller: ControllerName}},
		&v1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "edge"}, Spec: v1.GatewaySpec{GatewayClassName: "funnel", Listeners: []v1.Listener{{Name: "http", Protocol: "HTTP", Port: 80}}}},
		&core.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "echo", UID: types.UID("service-uid")}, Spec: core.ServiceSpec{Selector: map[string]string{"app": "echo"}, ClusterIP: "10.43.0.10", Ports: []core.ServicePort{{Name: "http", Port: 8080, TargetPort: intstr.FromInt32(80)}}}},
		&discovery.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "echo", OwnerReferences: []metav1.OwnerReference{{Kind: "Service", Name: "echo", UID: "service-uid", Controller: pointer(true)}}, Labels: map[string]string{discovery.LabelServiceName: "echo"}}, AddressType: discovery.AddressTypeIPv4, Ports: []discovery.EndpointPort{{Name: pointer("http"), Port: pointer(int32(80))}}, Endpoints: []discovery.Endpoint{{Addresses: []string{"10.42.0.2"}, TargetRef: &core.ObjectReference{Kind: "Pod", Namespace: "apps", Name: "echo-pod", UID: "pod-uid"}, Conditions: discovery.EndpointConditions{Ready: pointer(true)}}}},
		&v1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "checkout", UID: "route-uid"}, Spec: v1.HTTPRouteSpec{CommonRouteSpec: v1.CommonRouteSpec{ParentRefs: []v1.ParentReference{{Name: "edge"}}}, Hostnames: []v1.Hostname{"checkout.example.com"}, Rules: []v1.HTTPRouteRule{{Matches: []v1.HTTPRouteMatch{{Path: &v1.HTTPPathMatch{Type: pointer(v1.PathMatchPathPrefix), Value: pointer("/api")}}}, BackendRefs: []v1.HTTPBackendRef{{BackendRef: v1.BackendRef{BackendObjectReference: v1.BackendObjectReference{Name: "echo", Port: pointer(v1.PortNumber(8080))}, Weight: pointer(int32(2))}}}}}}},
		&core.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "echo-pod", UID: "pod-uid", Labels: map[string]string{"app": "echo"}}, Spec: core.PodSpec{NodeName: "node-a", Containers: []core.Container{{Name: "echo", Ports: []core.ContainerPort{{Name: "http", ContainerPort: 80}}}}}, Status: core.PodStatus{Phase: core.PodRunning, PodIP: "10.42.0.2", Conditions: []core.PodCondition{{Type: core.PodReady, Status: core.ConditionTrue}}}},
		&core.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", UID: "node-a-uid"}},
	}
}
func translateFixture(t *testing.T, objects []client.Object) Result {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(Scheme()).WithObjects(objects...).Build()
	r, e := Translate(context.Background(), c, Options{GatewayClass: "funnel", IngressClass: "funnel"})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestHTTPRouteTranslation(t *testing.T) {
	r := translateFixture(t, fixtureObjects())
	if len(r.Hosts) != 1 || len(r.Problems) != 0 {
		t.Fatalf("%+v", r)
	}
	rule := r.Hosts[0].Rules[0]
	if rule.Matches[0].Path != "/api" || rule.Backends[0].Address != "10.43.0.10:8080" || rule.Backends[0].Key != "service-uid:8080" || rule.Backends[0].Weight != 2 {
		t.Fatal(rule)
	}
}
func TestCrossNamespaceReferenceGrant(t *testing.T) {
	objects := fixtureObjects()
	svc := objects[3].(*core.Service)
	svc.Namespace = "private"
	objects[4].(*discovery.EndpointSlice).Namespace = "private"
	objects[4].(*discovery.EndpointSlice).Endpoints[0].TargetRef.Namespace = "private"
	objects[6].(*core.Pod).Namespace = "private"
	route := objects[5].(*v1.HTTPRoute)
	route.Spec.Rules[0].BackendRefs[0].Namespace = pointer(v1.Namespace("private"))
	r := translateFixture(t, objects)
	if len(r.Hosts) != 0 || len(r.Problems) != 1 {
		t.Fatal(r)
	}
	grant := &v1beta1.ReferenceGrant{ObjectMeta: metav1.ObjectMeta{Namespace: "private", Name: "allow"}, Spec: v1beta1.ReferenceGrantSpec{From: []v1beta1.ReferenceGrantFrom{{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Namespace: "apps"}}, To: []v1beta1.ReferenceGrantTo{{Group: "", Kind: "Service", Name: pointer(v1beta1.ObjectName("echo"))}}}}
	r = translateFixture(t, append(objects, grant))
	if len(r.Hosts) != 1 {
		t.Fatal(r.Problems)
	}
}
func TestUnsupportedAndMissingBackends(t *testing.T) {
	for _, kind := range []string{"tls", "regex", "annotation", "external", "class"} {
		t.Run(kind, func(t *testing.T) {
			objects := fixtureObjects()
			route := objects[5].(*v1.HTTPRoute)
			switch kind {
			case "tls":
				objects[2].(*v1.Gateway).Spec.Listeners[0].Protocol = "HTTPS"
			case "regex":
				route.Spec.Rules[0].Matches[0].Path.Type = pointer(v1.PathMatchRegularExpression)
			case "annotation":
				route.Annotations = map[string]string{AnnotationPrefix + "loadbalancer-ids": "forbidden"}
			case "external":
				objects[3].(*core.Service).Spec.Type = core.ServiceTypeExternalName
			case "class":
				objects[0].(*v1.GatewayClass).Spec.ControllerName = "other"
			}
			r := translateFixture(t, objects)
			if len(r.Hosts) != 0 {
				t.Fatal("unsupported route programmed", r)
			}
		})
	}
}
func TestIngressAndTimeoutPrecedence(t *testing.T) {
	objects := fixtureObjects()
	objects[2].(*v1.Gateway).Annotations = map[string]string{AnnotationPrefix + "backend-timeout": "2s"}
	route := objects[5].(*v1.HTTPRoute)
	route.Annotations = map[string]string{AnnotationPrefix + "backend-timeout": "3s"}
	route.Spec.Rules[0].Timeouts = &v1.HTTPRouteTimeouts{BackendRequest: pointer(v1.Duration("4s"))}
	pathType := networking.PathTypeExact
	ing := &networking.Ingress{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "legacy", UID: "ingress-uid"}}
	ing.Spec.IngressClassName = pointer("funnel")
	path := networking.HTTPIngressPath{Path: "/", PathType: &pathType, Backend: networking.IngressBackend{Service: &networking.IngressServiceBackend{Name: "echo", Port: networking.ServiceBackendPort{Name: "http"}}}}
	rule := networking.IngressRule{Host: "other.example.com"}
	rule.HTTP = &networking.HTTPIngressRuleValue{Paths: []networking.HTTPIngressPath{path}}
	ing.Spec.Rules = []networking.IngressRule{rule}
	objects = append(objects, ing)
	r := translateFixture(t, objects)
	if len(r.Hosts) != 2 || r.Hosts[0].Rules[0].BackendRequestTimeout != "4s" || !r.Hosts[1].Rules[0].Matches[0].Exact {
		t.Fatal(r)
	}
}

func TestExternalAndForgedEndpointBackendsDenied(t *testing.T) {
	for _, kind := range []string{"selectorless", "external-address", "wrong-pod-uid", "wrong-owner", "host-network", "not-ready", "wrong-target-port", "wrong-selector"} {
		t.Run(kind, func(t *testing.T) {
			objects := fixtureObjects()
			svc := objects[3].(*core.Service)
			slice := objects[4].(*discovery.EndpointSlice)
			pod := objects[6].(*core.Pod)
			switch kind {
			case "selectorless":
				svc.Spec.Selector = nil
			case "external-address":
				slice.Endpoints[0].Addresses = []string{"169.254.169.254"}
			case "wrong-pod-uid":
				slice.Endpoints[0].TargetRef.UID = "another-pod"
			case "wrong-owner":
				slice.OwnerReferences[0].UID = "another-service"
			case "host-network":
				pod.Spec.HostNetwork = true
			case "not-ready":
				pod.Status.Conditions[0].Status = core.ConditionFalse
			case "wrong-target-port":
				slice.Ports[0].Port = pointer(int32(22))
			case "wrong-selector":
				pod.Labels = map[string]string{"app": "other"}
			}
			r := translateFixture(t, objects)
			if len(r.Hosts) != 0 {
				t.Fatal("unsafe backend programmed", kind, r)
			}
		})
	}
}

func TestKubernetesEndpointsIncludeVerifiedNodeIdentity(t *testing.T) {
	result := translateFixture(t, fixtureObjects())
	if len(result.Hosts) != 1 || len(result.Hosts[0].Backends) == 0 {
		t.Fatal(result)
	}
	endpoints := result.Hosts[0].Backends[0].Endpoints
	if len(endpoints) != 1 || endpoints[0].NodeUID != "node-a-uid" || endpoints[0].NodeName != "node-a" || endpoints[0].PodUID != "pod-uid" || endpoints[0].Address != "10.42.0.2:80" {
		t.Fatal(endpoints)
	}
}

func TestUnreadyEndpointsKeepApplicationServiceIntent(t *testing.T) {
	objects := fixtureObjects()
	objects[4].(*discovery.EndpointSlice).Endpoints[0].Conditions.Ready = pointer(false)
	result := translateFixture(t, objects)
	if len(result.Hosts) != 1 || len(result.Hosts[0].Backends) != 1 || len(result.Hosts[0].Backends[0].Endpoints) != 0 {
		t.Fatal(result)
	}
	if result.Hosts[0].Backends[0].Key != "service-uid:8080" {
		t.Fatal("backend identity changed")
	}
}

func TestEndpointPortCannotEscapeResolvedServiceTarget(t *testing.T) {
	objects := fixtureObjects()
	slice := objects[4].(*discovery.EndpointSlice)
	slice.Ports = append(slice.Ports, discovery.EndpointPort{Name: pointer("http"), Port: pointer(int32(8443)), Protocol: pointer(core.ProtocolUDP)})
	result := translateFixture(t, objects)
	endpoints := result.Hosts[0].Backends[0].Endpoints
	if len(endpoints) != 1 || endpoints[0].Address != "10.42.0.2:80" {
		t.Fatal(endpoints)
	}
}

func TestAlternativeMatchesUseIndependentPrecedence(t *testing.T) {
	objects := fixtureObjects()
	a := objects[5].(*v1.HTTPRoute)
	a.Spec.Rules[0].Matches = []v1.HTTPRouteMatch{
		{Path: &v1.HTTPPathMatch{Type: pointer(v1.PathMatchPathPrefix), Value: pointer("/very-specific")}},
		{Path: &v1.HTTPPathMatch{Type: pointer(v1.PathMatchPathPrefix), Value: pointer("/")}},
	}
	b := a.DeepCopy()
	b.Name, b.UID = "second", "second-uid"
	b.Spec.Rules[0].Matches = []v1.HTTPRouteMatch{{Path: &v1.HTTPPathMatch{Type: pointer(v1.PathMatchPathPrefix), Value: pointer("/api")}}}
	result := translateFixture(t, append(objects, b))
	if len(result.Problems) != 0 || len(result.Hosts) != 1 || len(result.Hosts[0].Rules) != 3 {
		t.Fatal(result)
	}
	for i, want := range []string{"/very-specific", "/api", "/"} {
		if got := result.Hosts[0].Rules[i].Matches; len(got) != 1 || got[0].Path != want {
			t.Fatal(got, want)
		}
	}
}

func TestMatchPrecedenceDoesNotUseAdditiveWeights(t *testing.T) {
	a := proxy.Match{Path: "/longer"}
	b := proxy.Match{Path: "/", Method: "GET", Headers: map[string]string{"A": "b"}}
	if !matchPrecedes(a, b) || matchPrecedes(b, a) {
		t.Fatal("method overrode longer prefix")
	}
	if !matchPrecedes(proxy.Match{Path: "/", Exact: true}, proxy.Match{Path: "/" + strings.Repeat("x", 1023)}) {
		t.Fatal("long prefix overrode exact match")
	}
}

func TestSameSourceMatchTiesKeepOlderRouteFirst(t *testing.T) {
	objects := fixtureObjects()
	first := objects[5].(*v1.HTTPRoute)
	first.CreationTimestamp = metav1.NewTime(time.Unix(1, 0))
	first.Annotations = map[string]string{AnnotationPrefix + "request-timeout": "2s"}
	second := first.DeepCopy()
	second.Name, second.UID = "newer", "newer-uid"
	second.CreationTimestamp = metav1.NewTime(time.Unix(2, 0))
	second.Annotations = map[string]string{AnnotationPrefix + "request-timeout": "3s"}
	result := translateFixture(t, append(objects, second))
	if len(result.Problems) != 0 || len(result.Hosts) != 1 || len(result.Hosts[0].Rules) != 2 || result.Hosts[0].Rules[0].RequestTimeout != "2s" {
		t.Fatal(result)
	}
}
