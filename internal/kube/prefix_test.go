package kube

import (
	"testing"

	networking "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestHTTPRoutePrefixRewrite(t *testing.T) {
	objects := fixtureObjects()
	route := objects[5].(*v1.HTTPRoute)
	route.Spec.Rules[0].Filters = []v1.HTTPRouteFilter{{Type: v1.HTTPRouteFilterURLRewrite, URLRewrite: &v1.HTTPURLRewriteFilter{Path: &v1.HTTPPathModifier{Type: v1.PrefixMatchHTTPPathModifier, ReplacePrefixMatch: pointer("/internal")}}}}
	r := translateFixture(t, objects)
	if len(r.Problems) != 0 || len(r.Hosts) != 1 || r.Hosts[0].Rules[0].Rewrite.ReplacePrefix != "/internal" {
		t.Fatalf("%+v", r)
	}
	for _, kind := range []string{"full", "host", "exact", "multiple", "redirect", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			copy := route.DeepCopy()
			objects[5] = copy
			f := copy.Spec.Rules[0].Filters[0].URLRewrite
			switch kind {
			case "full":
				f.Path.Type = v1.FullPathHTTPPathModifier
				f.Path.ReplaceFullPath = pointer("/x")
			case "host":
				f.Hostname = pointer(v1.PreciseHostname("other.example.com"))
			case "exact":
				copy.Spec.Rules[0].Matches[0].Path.Type = pointer(v1.PathMatchExact)
			case "multiple":
				copy.Spec.Rules[0].Matches = append(copy.Spec.Rules[0].Matches, copy.Spec.Rules[0].Matches[0])
			case "redirect":
				copy.Spec.Rules[0].Filters = append(copy.Spec.Rules[0].Filters, v1.HTTPRouteFilter{Type: v1.HTTPRouteFilterRequestRedirect, RequestRedirect: &v1.HTTPRequestRedirectFilter{Scheme: pointer("https")}})
			case "invalid":
				f.Path.ReplacePrefixMatch = pointer("/{uri}")
			}
			r := translateFixture(t, objects)
			if len(r.Problems) == 0 || len(r.Hosts) != 0 {
				t.Fatalf("unsupported rewrite accepted: %+v", r)
			}
		})
	}
}
func TestIngressPrefixRewrite(t *testing.T) {
	for _, kind := range []networking.PathType{networking.PathTypePrefix, networking.PathTypeExact, networking.PathTypeImplementationSpecific} {
		objects := fixtureObjects()
		ing := &networking.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "path", Namespace: "apps", Annotations: map[string]string{AnnotationPrefix + "replace-prefix": "/"}}, Spec: networking.IngressSpec{IngressClassName: pointer("funnel"), Rules: []networking.IngressRule{{Host: "ingress.example.com", IngressRuleValue: networking.IngressRuleValue{HTTP: &networking.HTTPIngressRuleValue{Paths: []networking.HTTPIngressPath{{Path: "/web", PathType: pointer(kind), Backend: networking.IngressBackend{Service: &networking.IngressServiceBackend{Name: "echo", Port: networking.ServiceBackendPort{Number: 8080}}}}}}}}}}}
		r := translateFixture(t, append(objects, ing))
		if kind != networking.PathTypePrefix {
			if len(r.Problems) == 0 {
				t.Fatal("non-Prefix rewrite accepted")
			}
			continue
		}
		if len(r.Problems) != 0 || len(r.Hosts) != 2 {
			t.Fatalf("%+v", r)
		}
		for _, h := range r.Hosts {
			if h.Hostname == "ingress.example.com" && (h.Rules[0].Rewrite == nil || h.Rules[0].Rewrite.ReplacePrefix != "/") {
				t.Fatal(h)
			}
		}
	}
}
