package web

import (
	"strings"
	"testing"
)

func TestGatewayEnrollmentRequiresDocker(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net", "management_url": "http://100.64.0.10:8080", "release_version": "v1.0.2", "loadbalancer_hostname": "edge.example.com"})
	c = enrollmentTestControl(t, s)
	c.BuildVersion = "dev-tailnet-test"
	w := requestManagement(c, "POST", "/api/enrollments/docker-gateway", map[string]any{"name": "Remote gateway", "hostname": "remote-gateway", "sources": "tailnet"}, "owner")
	if w.Code != 400 {
		t.Fatalf("tailnet gateway accepted: %d", w.Code)
	}
	w = requestManagement(c, "POST", "/api/enrollments/docker-gateway", map[string]any{"name": "Docker gateway", "hostname": "docker-gateway"}, "owner")
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
