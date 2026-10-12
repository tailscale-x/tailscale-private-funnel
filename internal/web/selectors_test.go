package web

import (
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"strings"
	"testing"
)

func TestKnownInfrastructureUsesSelectors(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("ingress-one", "ingress", "Ingress one")
	_ = s.UpsertNode("edge-one", "loadbalancer", "Edge one")
	_ = s.SetJSON("node_deployment:edge-one", nodeDeployment{LoadBalancerHostname: "edge.example.com"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "existing", IngressIDs: []string{"removed-node"}, LoadBalancerIDs: []string{"old.example.com"}, HostnameRegexes: []string{".*"}, Enabled: true}}})
	w := requestManagement(c, "GET", "/policies", nil, "owner")
	for _, required := range []string{`name="ingress_ids" multiple`, `name="loadbalancer_ids" multiple`, `value="ingress-one"`, `value="edge-one"`, `value="removed-node"`, `selectedOptions`, `option.selected=`} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), required) {
			t.Fatalf("missing selector %s: %d", required, w.Code)
		}
	}
	w = requestManagement(c, "GET", "/tailnet-exposures", nil, "owner")
	for _, id := range []string{"remote-node", "remote-peer"} {
		if !strings.Contains(w.Body.String(), `<select class="pf-input" id="`+id+`"`) {
			t.Fatalf("%s is not a select", id)
		}
	}
}

func TestIngressEnrollmentRequiresDocker(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net", "management_url": "http://100.64.0.10:8080", "release_version": "v1.0.2", "loadbalancer_hostname": "edge.example.com"})
	c = enrollmentTestControl(t, s)
	c.BuildVersion = "dev-tailnet-test"
	w := requestManagement(c, "POST", "/api/enrollments/ingress", map[string]any{"name": "Remote ingress", "hostname": "remote-ingress", "sources": "tailnet"}, "owner")
	if w.Code != 400 {
		t.Fatalf("tailnet ingress accepted: %d", w.Code)
	}
	w = requestManagement(c, "POST", "/api/enrollments/ingress", map[string]any{"name": "Docker ingress", "hostname": "docker-ingress"}, "owner")
	if w.Code != 200 {
		t.Fatalf("enrollment failed: %d", w.Code)
	}
	for _, value := range []string{"Docker discovery requires host Docker", "dev-tailnet-test", "FUNNEL_NATIVE_INSTALL"} {
		if !strings.Contains(w.Body.String(), value) {
			t.Fatalf("missing %q", value)
		}
	}
	if strings.Contains(w.Body.String(), "--sources") {
		t.Fatal("obsolete sources flag in generated command")
	}
}
