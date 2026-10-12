package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestApplicationAliasSharesApprovedBackend(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	installTestEdge(t, s)
	_ = s.UpsertNode("ingress", "ingress", "host-ingress")
	_ = s.SetJSON("node_deployment:ingress", nodeDeployment{})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "allowed", HostnameRegexes: []string{`^.*[.]example[.]com$`}, Enabled: true}}})
	app := store.ApplicationRecord{ID: "app", ServiceName: "svc:app", ServiceFQDN: "100.64.0.5", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"original.example.com"}, AllowedIngressNodes: []string{"ingress"}, Enabled: true}
	_ = s.UpsertApplication(app)
	for _, role := range []string{"viewer", "operator"} {
		r := WithPrincipal(httptest.NewRequest("POST", "/api/applications/app/aliases", strings.NewReader(`{"hostname":"alias.example.com"}`)), Principal{NodeID: role, TailnetRole: role})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}

	w := requestManagement(c, "POST", "/api/applications/app/aliases", map[string]any{"hostname": "alias.example.com"}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	apps, _ := s.Applications()
	node, _ := s.Node("ingress")
	e := dns.IngressExposure{ApplicationID: "app", ServiceName: "svc:app", PublicHostname: "original.example.com", Port: 8080, Reachable: true}
	expanded := c.expandDockerAliases([]dns.IngressExposure{e}, apps, node, nil)
	if len(expanded) != 2 || expanded[1].ServiceName != e.ServiceName || expanded[1].PublicHostname != "alias.example.com" {
		t.Fatal(expanded)
	}
	if got := c.expandDockerAliases(nil, apps, node, nil); len(got) != 0 {
		t.Fatal("alias became active without backend report")
	}
	e.ServiceName = "svc:spoofed"
	if got := c.expandDockerAliases([]dns.IngressExposure{e}, apps, node, nil); len(got) != 1 {
		t.Fatal("alias trusted mismatched service")
	}
	if w = requestManagement(c, "POST", "/api/applications/app/aliases", map[string]any{"hostname": "alias.example.com"}, "owner"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "original only", HostnameRegexes: []string{`^original[.]example[.]com$`}, Enabled: true}}})
	e.ServiceName = "svc:app"
	if got := c.expandDockerAliases([]dns.IngressExposure{e}, apps, node, nil); len(got) != 1 {
		t.Fatal("denied alias still expanded")
	}
	r := WithPrincipal(httptest.NewRequest("GET", "/applications", nil), Principal{NodeID: "admin", TailnetRole: "owner"})
	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "data-rename") || strings.Contains(rec.Body.String(), ">Rename ") {
		t.Fatal("application rename editor still present")
	}
}
