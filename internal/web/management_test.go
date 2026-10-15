package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

type fixtureDNS struct {
	records map[string][]libdns.Record
	fail    string
	zones   []libdns.Zone
}

func (f *fixtureDNS) List(_ context.Context, z string) ([]libdns.Record, error) {
	if f.fail == z {
		return nil, fmt.Errorf("zone unavailable")
	}
	return f.records[z], nil
}
func (f *fixtureDNS) Apply(_ context.Context, z string, add, remove []libdns.Record) error {
	if f.fail == z {
		return fmt.Errorf("zone unavailable")
	}
	next := []libdns.Record{}
	for _, r := range f.records[z] {
		drop := false
		for _, d := range remove {
			if r.RR().Name == d.RR().Name && r.RR().Type == d.RR().Type && r.RR().Data == d.RR().Data {
				drop = true
			}
		}
		if !drop {
			next = append(next, r)
		}
	}
	f.records[z] = append(next, add...)
	return nil
}
func (f *fixtureDNS) Capabilities() []string { return []string{"A", "CNAME", "TXT"} }
func (f *fixtureDNS) ListZones(context.Context) ([]libdns.Zone, error) {
	if f.fail == "discovery" {
		return nil, fmt.Errorf("Zone Read denied")
	}
	if f.zones != nil {
		return f.zones, nil
	}
	return []libdns.Zone{{Name: "example.com"}, {Name: "example.net"}, {Name: "example.org"}}, nil
}
func installDNSFixture(t *testing.T, s *store.Store, zones ...string) *fixtureDNS {
	t.Helper()
	f := &fixtureDNS{records: map[string][]libdns.Record{}}
	previous, existed := dns.Registry["fixture"]
	dns.Registry["fixture"] = func(map[string]string) (dns.Provider, error) { return f, nil }
	t.Cleanup(func() {
		if existed {
			dns.Registry["fixture"] = previous
		} else {
			delete(dns.Registry, "fixture")
		}
	})
	if err := s.SaveProvider("fixture", "fixture", map[string]string{"api_token": "secret"}, true); err != nil {
		t.Fatal(err)
	}
	for _, z := range zones {
		if err := s.AddZone("fixture", z); err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func testControl(t *testing.T) (*Control, *store.Store) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	installTestEdge(t, s)
	if err := s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin", "node:owner"}}}); err != nil {
		t.Fatal(err)
	}
	return &Control{Store: s}, s
}
func requestManagement(c *Control, method, path string, body any, role string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r = WithPrincipal(r, Principal{NodeID: "admin", Login: "admin@example.com", TailnetRole: role})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	return w
}
func TestRuleCRUDRevisionAndReadOnly(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("docker-gateway", "docker-gateway", "gateway-host")
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "viewer": {"node:viewer"}, "operator": {"node:operator"}}})
	rule := policy.Rule{Name: "first", HostnameRegexes: []string{`.*[.]example[.]com`}, GatewayIDs: []string{"docker-gateway"}, Enabled: true}
	rev := c.loadExposurePolicy().Revision
	w := requestManagement(c, "POST", "/api/policies/rule", map[string]any{"revision": rev, "rule": rule}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d := c.loadExposurePolicy()
	id := d.Rules[0].ID
	rule.Name = "edited"
	w = requestManagement(c, "PUT", "/api/policies/rules/"+id, map[string]any{"revision": d.Revision, "rule": rule}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d = c.loadExposurePolicy()
	if len(d.Rules) != 1 || d.Rules[0].ID != id || d.Rules[0].Name != "edited" {
		t.Fatal(d)
	}
	if w = requestManagement(c, "PUT", "/api/policies/rules/"+id, map[string]any{"revision": rev, "rule": rule}, "owner"); w.Code != 409 {
		t.Fatal("stale", w.Code)
	}
	if w = requestManagement(c, "POST", "/api/policies/validate", d, "owner"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if c.loadExposurePolicy().Revision != d.Revision {
		t.Fatal("validate mutated policy")
	}
	rule.Enabled = false
	w = requestManagement(c, "PUT", "/api/policies/rules/"+id, map[string]any{"revision": d.Revision, "rule": rule}, "owner")
	if w.Code != 200 || c.loadExposurePolicy().Rules[0].Enabled {
		t.Fatal("disable", w.Code)
	}
	d = c.loadExposurePolicy()
	w = requestManagement(c, "DELETE", fmt.Sprintf("/api/policies/rules/%s?revision=%d", id, d.Revision), nil, "owner")
	if w.Code != 200 || len(c.loadExposurePolicy().Rules) != 0 {
		t.Fatal("delete", w.Code)
	}
	for _, node := range []string{"viewer", "operator"} {
		r := WithPrincipal(httptest.NewRequest("POST", "/api/policies/rule", bytes.NewReader([]byte(`{}`))), Principal{NodeID: node})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(node, w.Code)
		}
		r = WithPrincipal(httptest.NewRequest("GET", "/policies", nil), Principal{NodeID: node})
		w = httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if bytes.Contains(w.Body.Bytes(), []byte(`id="rule-form"`)) {
			t.Fatal("mutating form shown", node)
		}
	}
}
func TestMultiZoneRoutingAndIndependentCleanup(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com", "example.net")
	_ = s.AddZone("fixture", "dev.example.com")
	z, err := c.managedZone("app.dev.example.com")
	if err != nil || z.Zone != "dev.example.com" {
		t.Fatal(z, err)
	}
	if _, err = c.managedZone("badexample.com"); err == nil {
		t.Fatal("domain boundary")
	}
	_ = s.SaveProvider("other", "fixture", map[string]string{"key": "value"}, true)
	if err = s.AddZone("other", "example.com"); err == nil {
		t.Fatal("duplicate assignment accepted")
	}
	desired := map[store.LedgerPartition][]libdns.Record{}
	for _, z := range []string{"example.com", "example.net"} {
		p := store.LedgerPartition{ProviderID: "fixture", Zone: z, OwnerID: "gateway:n"}
		desired[p] = []libdns.Record{libdns.RR{Name: "app." + z, Type: "CNAME", Data: "edge.example.com", TTL: time.Minute}}
	}
	if err = c.reconcilePartitions(context.Background(), "gateway:n", desired); err != nil {
		t.Fatal(err)
	}
	f.records["example.net"] = append(f.records["example.net"], libdns.RR{Name: "unrelated.example.net", Type: "TXT", Data: "keep"})
	f.fail = "example.com"
	if err = c.reconcilePartitions(context.Background(), "gateway:n", nil); err == nil {
		t.Fatal("expected isolated failure")
	}
	netLedger, _ := s.Ledger("fixture", "example.net", "gateway:n")
	comLedger, _ := s.Ledger("fixture", "example.com", "gateway:n")
	if len(netLedger) != 0 || len(comLedger) != 1 || len(f.records["example.net"]) != 1 {
		t.Fatal(netLedger, comLedger, f.records)
	}
	f.fail = ""
	if err = c.reconcilePartitions(context.Background(), "gateway:n", nil); err != nil {
		t.Fatal(err)
	}
}
func TestProviderSecretsDiscoveryAndZoneGuards(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com", "example.net")
	w := requestManagement(c, "PATCH", "/api/providers/fixture", map[string]any{"name": "Fixture provider", "provider": "fixture", "account_id": "account", "config": map[string]string{"api_token": ""}}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	p, config, err := s.ProviderConfig("fixture")
	if err != nil || config["api_token"] != "secret" || p.AccountID != "account" {
		t.Fatal(p, config, err)
	}
	w = requestManagement(c, "GET", "/api/providers/fixture/zones", nil, "owner")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	f.fail = "discovery"
	w = requestManagement(c, "GET", "/api/providers/fixture/zones", nil, "owner")
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
	zones, _ := s.Zones()
	if len(zones) != 2 {
		t.Fatal("discovery changed scope")
	}
	_ = s.ReplaceLedger("fixture", "example.com", "gateway:n", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}})
	if err := s.AddZone("other", "example.com"); err == nil {
		t.Fatal("provider changed with ledger")
	}
	if err := s.DeleteProvider("fixture"); err == nil {
		t.Fatal("deleted used provider")
	}
}
func TestPolicyWithdrawalOverlapAndRetry(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	_ = s.UpsertNodeEnrollment("docker-gateway", "enrollment", "docker-gateway", "node")
	_ = s.SetJSON("node_deployment:docker-gateway", nodeDeployment{})
	pub, priv, _ := report.GenerateKey()
	_ = s.BindNodeIdentity("docker-gateway", encodePublicKey(pub), "tailnode", nil)
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "app", ServiceName: "svc:app", ServiceFQDN: "svc:app", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com"}, AllowedGatewayNodes: []string{"docker-gateway"}, Enabled: true})
	rule := policy.Rule{ID: "one", Name: "First rule", HostnameRegexes: []string{`app[.]example[.]com`}, GatewayIDs: []string{"docker-gateway"}, Enabled: true}
	d := policy.Document{Revision: 1, Rules: []policy.Rule{rule, rule}}
	d.Rules[1].ID = "two"
	_ = s.SetJSON("exposure_policy", d)
	env, _ := report.NewEnvelope("docker-gateway", map[string]any{"exposures": []dns.GatewayExposure{{ApplicationID: "app", PublicHostname: "app.example.com", ServiceName: "svc:app", Port: 8080, Reachable: true}}, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, priv, time.Now())
	raw, _ := json.Marshal(env)
	_ = s.AcceptReport("docker-gateway", env.Nonce, raw, env.PublicKey)
	seedEdgeReport(t, s, "edge-id")
	if err := c.ReconcilePolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.Rules[0].Enabled = false
	if len(c.policyImpact(d)) != 0 {
		t.Fatal("overlap should preserve")
	}
	d.Rules[1].Enabled = false
	if len(c.policyImpact(d)) != 1 {
		t.Fatal("expected impact")
	}
	_ = s.SetJSON("exposure_policy", d)
	f.fail = "example.com"
	if err := c.ReconcilePolicy(context.Background()); err == nil {
		t.Fatal("expected cleanup failure")
	}
	if len(c.authorizedHostnames(d)) != 0 {
		t.Fatal("denied route retained")
	}
	f.fail = ""
	if err := c.ReconcilePolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	ledger, _ := exposureLedger(t, s, "fixture", "example.com", "app.example.com")
	if len(ledger) > 0 {
		t.Fatal("cleanup not retried")
	}
}

func TestProviderAndZoneAdminOnly(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"viewer": {"node:viewer"}, "operator": {"node:operator"}}})
	for _, role := range []string{"viewer", "operator"} {
		for _, route := range []string{"/providers", "/zones"} {
			r := WithPrincipal(httptest.NewRequest("GET", route, nil), Principal{NodeID: role})
			w := httptest.NewRecorder()
			c.Handler().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal(role, route, w.Code)
			}
			for _, control := range []string{"connection-form", "zone-form", "data-zone-toggle", "data-delete="} {
				if bytes.Contains(w.Body.Bytes(), []byte(control)) {
					t.Fatal("mutation control exposed", role, control)
				}
			}
		}
		for _, path := range []string{"/api/providers", "/api/zones", "/api/providers/fixture/test", "/api/providers/fixture/enable", "/api/zones/test"} {
			r := WithPrincipal(httptest.NewRequest("POST", path, bytes.NewReader([]byte(`{}`))), Principal{NodeID: role})
			w := httptest.NewRecorder()
			c.Handler().ServeHTTP(w, r)
			if w.Code != 403 {
				t.Fatal("mutation permitted", role, path, w.Code)
			}
		}
	}
}

func TestServiceHostWithdrawalPreservesSharedAuthorizedAssignment(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.UpsertNodeEnrollment("docker-gateway", "e", "docker-gateway", "node")
	_ = s.SetJSON("node_deployment:docker-gateway", nodeDeployment{})
	pub, priv, _ := report.GenerateKey()
	_ = s.BindNodeIdentity("docker-gateway", encodePublicKey(pub), "tailnode", nil)
	app := store.ApplicationRecord{ID: "app", ServiceName: "svc:app", ServiceFQDN: "svc:app", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"one.example.com", "two.example.com"}, AllowedGatewayNodes: []string{"docker-gateway"}, Enabled: true}
	_ = s.UpsertApplication(app)
	d := policy.Document{Revision: 1, Rules: []policy.Rule{{ID: "one", Name: "First rule", HostnameRegexes: []string{`one[.]example[.]com`}, GatewayIDs: []string{"docker-gateway"}, Enabled: true}, {ID: "two", Name: "Second rule", HostnameRegexes: []string{`two[.]example[.]com`}, GatewayIDs: []string{"docker-gateway"}, Enabled: true}}}
	_ = s.SetJSON("exposure_policy", d)
	exposures := []dns.GatewayExposure{}
	for _, h := range app.PublicHostnames {
		exposures = append(exposures, dns.GatewayExposure{ApplicationID: "app", PublicHostname: h, ServiceName: "svc:app", Port: 8080, Reachable: true})
	}
	env, _ := report.NewEnvelope("docker-gateway", map[string]any{"exposures": exposures, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, priv, time.Now())
	raw, _ := json.Marshal(env)
	_ = s.AcceptReport("docker-gateway", env.Nonce, raw, env.PublicKey)
	seedEdgeReport(t, s, "edge-id")
	_ = s.SetJSON("node_config_ack:docker-gateway", map[string]any{"services": []string{"svc:app"}, "at": time.Now().Unix()})
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "loadbalancer_hostname": "edge.example.com"})
	_ = s.SetSecret("tailscale_oauth_client_id", "id")
	_ = s.SetSecret("tailscale_oauth_client_secret", "secret")
	disapproved := 0
	fail := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "oauth/token"):
			writeJSON(w, map[string]any{"access_token": "token"})
		case strings.HasSuffix(r.URL.Path, "/devices"):
			writeJSON(w, map[string]any{"hosts": []map[string]any{{"nodeId": "tailnode", "approvalLevel": "approved:manual"}}})
		case r.URL.Path == "/device/tailnode":
			writeJSON(w, map[string]any{"nodeId": "tailnode", "authorized": true, "tags": []string{"tag:tailscale-private-funnel"}})
		case strings.HasSuffix(r.URL.Path, "/approved"):
			if fail {
				http.Error(w, "permission denied", 403)
				return
			}
			disapproved++
			writeJSON(w, map[string]any{"approved": false})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	client := tailscale.Client{APIBase: api.URL, OAuthBase: api.URL, HTTP: api.Client()}
	c.TailscaleClient = &client
	d.Rules[0].Enabled = false
	_ = s.SetJSON("exposure_policy", d)
	if err := c.ReconcileServiceApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if disapproved != 0 {
		t.Fatal("shared Service was disapproved")
	}
	d.Rules[1].Enabled = false
	_ = s.SetJSON("exposure_policy", d)
	fail = true
	if err := c.ReconcileServiceApprovals(context.Background()); err == nil {
		t.Fatal("approval failure hidden")
	}
	fail = false
	if err := c.ReconcileServiceApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if disapproved != 1 {
		t.Fatal("unneeded Service not disapproved", disapproved)
	}
}

func TestSignedReportReconcilesIndependentZones(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com", "example.net")
	_ = s.UpsertNodeEnrollment("docker-gateway", "e", "docker-gateway", "node")
	_ = s.SetJSON("node_deployment:docker-gateway", nodeDeployment{})
	pub, priv, _ := report.GenerateKey()
	_ = s.BindNodeIdentity("docker-gateway", encodePublicKey(pub), "tailnode", nil)
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{HostnameRegexes: []string{`app[.]example[.](com|net)`}, GatewayIDs: []string{"docker-gateway"}, Enabled: true}}})
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "app", ServiceName: "svc:app", ServiceFQDN: "svc:app", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com", "app.example.net"}, AllowedGatewayNodes: []string{"docker-gateway"}, Enabled: true})
	seedEdgeReport(t, s, "edge-id")
	f.fail = "example.com"
	env, _ := report.NewEnvelope("docker-gateway", map[string]any{"exposures": []dns.GatewayExposure{{ApplicationID: "app", PublicHostname: "app.example.com", ServiceName: "svc:app", Port: 8080, Reachable: true}, {ApplicationID: "app", PublicHostname: "app.example.net", ServiceName: "svc:app", Port: 8080, Reachable: true}}, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, priv, time.Now())
	raw, _ := json.Marshal(env)
	r := httptest.NewRequest("POST", "/api/reports", bytes.NewReader(raw))
	r = WithPrincipal(r, Principal{NodeID: "tailnode"})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	netLedger, _ := exposureLedger(t, s, "fixture", "example.net", "app.example.net")
	if len(netLedger) != 1 {
		t.Fatal("independent zone not reconciled", netLedger)
	}
	f.fail = ""
	if err := c.ReconcilePolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	comLedger, _ := exposureLedger(t, s, "fixture", "example.com", "app.example.com")
	if len(comLedger) != 1 {
		t.Fatal("failed zone not retried", comLedger)
	}
}

func TestDisabledProviderCleansLoadBalancerOwnedPartition(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	node := store.NodeRecord{ID: "lb", Mode: "loadbalancer", TailnetID: "device-lb"}
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "loadbalancer_hostname": "edge.example.com"})
	_ = s.SetSecret("tailscale_oauth_client_id", "id")
	_ = s.SetSecret("tailscale_oauth_client_secret", "secret")
	endpoints := []string{"8.8.8.8:41641", "8.8.8.8:1234", "100.64.0.1:41641"}
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			fmt.Fprint(w, `{"access_token":"test-token"}`)
			return
		}
		if r.URL.Query().Get("fields") != "all" {
			t.Error("missing fields=all")
		}
		if fail {
			http.Error(w, "lookup failed", 503)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"clientConnectivity": map[string]any{"endpoints": endpoints}, "addresses": []string{"9.9.9.9"}})
	}))
	defer server.Close()
	c.TailscaleClient = &tailscale.Client{APIBase: server.URL + "/api/v2", OAuthBase: server.URL, HTTP: server.Client()}
	_ = s.SetJSON("node_health:lb", map[string]any{"public_ipv4": []string{"1.1.1.1"}, "received_at": time.Now().Unix()})
	if err := c.reconcileLoadBalancerNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	endpoints = []string{"8.8.8.8:41641", "9.9.9.9:41641"}
	if err := c.reconcileLoadBalancerNode(context.Background(), node); err == nil {
		t.Fatal("ambiguous endpoint should report error")
	}
	ledger, _ := s.Ledger("fixture", "example.com", "loadbalancer:lb")
	if len(ledger) != 1 {
		t.Fatal("previous A not retained")
	}
	for _, values := range [][]string{nil, {"100.64.0.1:41641"}, {"10.0.0.1:41641"}, {"[2001:db8::1]:41641"}} {
		endpoints = values
		if err := c.reconcileLoadBalancerNode(context.Background(), node); err == nil {
			t.Fatal("missing public endpoint accepted")
		}
		ledger, _ = s.Ledger("fixture", "example.com", "loadbalancer:lb")
		if len(ledger) != 1 || ledger[0].Record.RR().Data != "8.8.8.8" {
			t.Fatal("previous endpoint not retained", ledger)
		}
	}
	fail = true
	if err := c.reconcileLoadBalancerNode(context.Background(), node); err == nil {
		t.Fatal("API failure accepted")
	}
	ledger, _ = s.Ledger("fixture", "example.com", "loadbalancer:lb")
	if len(ledger) != 1 {
		t.Fatal("API failure deleted DNS")
	}
	_ = s.SetProviderEnabled("fixture", false)
	_ = c.reconcileLoadBalancerNode(context.Background(), node)
	ledger, _ = s.Ledger("fixture", "example.com", "loadbalancer:lb")
	if len(ledger) != 0 || len(f.records["example.com"]) != 0 {
		t.Fatal("disabled provider left owned A", ledger, f.records)
	}
}

func TestAutomaticNodeOwnershipAndRemovedFields(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	_ = s.UpsertNode("node", "docker-gateway", "docker-gateway")
	_ = s.UpsertNode("other", "docker-gateway", "other")
	record := map[string]any{"name": "app.example.com", "type": "CNAME", "data": "edge.example.com", "ttl": 60}
	body := map[string]any{"provider_id": "fixture", "zone_id": testZoneID(t, s, "example.com"), "node_id": "node", "records": []any{record}}
	w := requestManagement(c, "POST", "/api/sync", body, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	ledger, _ := s.Ledger("fixture", "example.com", "gateway:node")
	if len(ledger) != 1 {
		t.Fatal("ownership not derived", ledger)
	}
	for _, field := range []string{"owner_id", "kind"} {
		body[field] = "obsolete"
		w = requestManagement(c, "POST", "/api/sync", body, "owner")
		if w.Code != 400 {
			t.Fatal("accepted removed field", field, w.Code)
		}
		delete(body, field)
	}
	body["node_id"] = "missing"
	if w = requestManagement(c, "POST", "/api/sync", body, "owner"); w.Code != 400 {
		t.Fatal("unknown node accepted")
	}
	body["node_id"] = "other"
	body["dry_run"] = true
	w = requestManagement(c, "POST", "/api/sync", body, "owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Managed by another node") {
		t.Fatal(w.Body.String())
	}
	body["node_id"] = "node"
	_ = s.RevokeNode("node")
	if w = requestManagement(c, "POST", "/api/sync", body, "owner"); w.Code != 400 {
		t.Fatal("revoked desired state accepted")
	}
	body["records"] = []any{}
	body["dry_run"] = false
	w = requestManagement(c, "POST", "/api/sync", body, "owner")
	if w.Code != 200 || len(f.records["example.com"]) != 0 {
		t.Fatal("cleanup failed", w.Body.String())
	}
}
func TestZoneAdditionDuplicateRetryAndNoOwnershipField(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	for _, zone := range []string{"example.net", "example.org"} {
		w := requestManagement(c, "POST", "/api/zones", map[string]any{"provider_id": "fixture", "zone": zone}, "owner")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	w := requestManagement(c, "POST", "/api/zones", map[string]any{"provider_id": "fixture", "zone": "example.net"}, "owner")
	if w.Code != 409 {
		t.Fatal("duplicate accepted", w.Code)
	}
	zones, _ := s.Zones()
	if len(zones) != 3 {
		t.Fatal(zones)
	}
	w = requestManagement(c, "POST", "/api/zones", map[string]any{"provider_id": "fixture", "zone": "new.example.com", "owner_id": "removed"}, "owner")
	if w.Code != 400 {
		t.Fatal("old field accepted")
	}
	w = requestManagement(c, "GET", "/api/zones", nil, "owner")
	if strings.Contains(w.Body.String(), "owner_id") {
		t.Fatal(w.Body.String())
	}
}
func TestDNSManagementUIRolesAndSecretRedaction(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.SaveProvider("cloudflare", "cloudflare", map[string]string{"api_token": "never-render-secret", "account_id": "public-account"}, true)
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "viewer": {"node:viewer"}, "operator": {"node:operator"}}})
	for _, role := range []string{"admin", "viewer", "operator"} {
		for _, path := range []string{"/providers", "/zones", "/operations"} {
			r := WithPrincipal(httptest.NewRequest("GET", path, nil), Principal{NodeID: role})
			w := httptest.NewRecorder()
			c.Handler().ServeHTTP(w, r)
			html := w.Body.String()
			if w.Code != 200 || strings.Contains(html, "never-render-secret") || strings.Contains(html, "Ownership ID") || strings.Contains(html, "Credential connection") {
				t.Fatal(path, w.Code, html)
			}
			for _, mutation := range []string{`data-open="add-provider"`, `data-edit-provider=`, `data-open="add-zone"`, `data-edit-zone=`, `id="apply"`} {
				if role != "admin" && strings.Contains(html, mutation) {
					t.Fatal(role, path, mutation)
				}
			}
		}
	}
}

func TestDNSApplyRequiresAdministratorAndExposuresDeriveMode(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.UpsertNode("edge", "loadbalancer", "edge")
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "operator": {"node:operator"}}})
	r := WithPrincipal(httptest.NewRequest("POST", "/api/sync", strings.NewReader(fmt.Sprintf(`{"provider_id":"fixture","zone_id":%q,"node_id":"edge","records":[],"dry_run":false}`, testZoneID(t, s, "example.com")))), Principal{NodeID: "operator"})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("operator applied DNS", w.Code)
	}
	body := map[string]any{"provider_id": "fixture", "zone_id": testZoneID(t, s, "example.com"), "node_id": "edge", "dry_run": true, "cleanup": true}
	w = requestManagement(c, "POST", "/api/exposures/sync", body, "owner")
	if w.Code != 200 || strings.Contains(w.Body.String(), "owner_id") {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, removed := range []string{"owner_id", "kind"} {
		body[removed] = "removed"
		w = requestManagement(c, "POST", "/api/exposures/sync", body, "owner")
		if w.Code != 400 {
			t.Fatal("accepted removed field", removed, w.Code)
		}
		delete(body, removed)
	}
}
func TestProviderCreationDoesNotOverwriteExistingCredentials(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	w := requestManagement(c, "POST", "/api/providers", map[string]any{"id": "fixture", "provider": "fixture", "config": map[string]string{"api_token": "replacement"}, "create_only": true}, "owner")
	if w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	_, cfg, _ := s.ProviderConfig("fixture")
	if cfg["api_token"] != "secret" {
		t.Fatal("overwritten credentials")
	}
}

func TestAuthorizationSettingsFailureDoesNotBootstrap(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("access_roles", "corrupt settings")
	r := WithPrincipal(httptest.NewRequest("GET", "/providers", nil), Principal{NodeID: "new-node", Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal("storage failure allowed setup", w.Code, w.Body.String())
	}
	var original string
	_ = s.GetJSON("access_roles", &original)
	if original != "corrupt settings" {
		t.Fatal("access configuration overwritten")
	}
}

func TestUnreadablePolicyPreservesRulesAndOwnedDNS(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	record := libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}
	f.records["example.com"] = []libdns.Record{record}
	_ = s.ReplaceLedger("fixture", "example.com", "gateway:node", []libdns.Record{record})
	_ = s.SetJSON("exposure_policy", "unreadable policy")
	_ = c.loadExposurePolicy()
	var original string
	if err := s.GetJSON("exposure_policy", &original); err != nil || original != "unreadable policy" {
		t.Fatal("policy overwritten", original, err)
	}
	if err := c.ReconcilePolicy(context.Background()); err == nil {
		t.Fatal("unreadable policy accepted")
	}
	ledger, _ := s.Ledger("fixture", "example.com", "gateway:node")
	if len(ledger) != 1 || len(f.records["example.com"]) != 1 {
		t.Fatal("DNS withdrawn on read failure")
	}
	w := requestManagement(c, "POST", "/api/reports", map[string]any{}, "owner")
	if w.Code != 503 {
		t.Fatal("report cleanup allowed", w.Code)
	}
}

func TestZoneSelectionFreshDiscoveryAndRemovedFields(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	for _, field := range []string{"enabled", "create_only", "previous_provider_id", "owner_id"} {
		body := map[string]any{"provider_id": "fixture", "zone": "example.net", field: true}
		if w := requestManagement(c, "POST", "/api/zones", body, "owner"); w.Code != 400 {
			t.Fatal(field, w.Code, w.Body.String())
		}
	}
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "viewer": {"node:viewer"}, "operator": {"node:operator"}}})
	for _, role := range []string{"viewer", "operator"} {
		r := WithPrincipal(httptest.NewRequest("POST", "/api/zones", strings.NewReader(`{"provider_id":"fixture","zone":"example.net"}`)), Principal{NodeID: role})
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	body := map[string]string{"provider_id": "fixture", "zone": "unknown.example"}
	if w := requestManagement(c, "POST", "/api/zones", body, "owner"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	body["zone"] = "example.net"
	f.fail = "discovery"
	if w := requestManagement(c, "POST", "/api/zones", body, "owner"); w.Code != 502 {
		t.Fatal(w.Code)
	}
	f.fail = "example.net"
	if w := requestManagement(c, "POST", "/api/zones", body, "owner"); w.Code != 502 {
		t.Fatal(w.Code)
	}
	zones, _ := s.Zones()
	if len(zones) != 1 {
		t.Fatal("failed selection mutated scope")
	}
	f.fail = ""
	if w := requestManagement(c, "POST", "/api/zones", body, "owner"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = s.ReplaceLedger("fixture", "example.net", "gateway:node", []libdns.Record{libdns.RR{Name: "app.example.net", Type: "A", Data: "192.0.2.1"}})
	if err := s.DeleteZone("fixture", "example.net"); err == nil {
		t.Fatal("removed zone with ledger")
	}
	w := requestManagement(c, "GET", "/zones", nil, "owner")
	for _, obsolete := range []string{"manual-domain", "edit-zone", "data-zone-action=\"enable\""} {
		if strings.Contains(w.Body.String(), obsolete) {
			t.Fatal(obsolete)
		}
	}
}

func TestZoneSelectionRejectsAmbiguousDiscovery(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	f.zones = []libdns.Zone{{Name: "example.net."}, {Name: "EXAMPLE.NET"}}
	w := requestManagement(c, "POST", "/api/zones", map[string]string{"provider_id": "fixture", "zone": "example.net"}, "owner")
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	zones, _ := s.Zones()
	if len(zones) != 1 {
		t.Fatal("ambiguous domain assigned")
	}
}

func TestUnrestrictedExposureRuleAndPrettyJSON(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}}})
	rule := policy.Rule{Name: "Hostname only", HostnameRegexes: []string{`app[.]example[.]com`}, Enabled: true}
	w := requestManagement(c, "POST", "/api/policies/rule", map[string]any{"revision": c.loadExposurePolicy().Revision, "rule": rule}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if ok, _ := c.loadExposurePolicy().Allows("app.example.com", []string{"any-enrolled-node"}, nil); !ok {
		t.Fatal("optional selectors rejected")
	}
	w = requestManagement(c, "GET", "/api/policies", nil, "owner")
	if !strings.Contains(w.Body.String(), "\n  \"revision\"") {
		t.Fatal("JSON response not formatted", w.Body.String())
	}
	w = requestManagement(c, "GET", "/policies", nil, "owner")
	for _, text := range []string{"All enrolled gateway nodes", "All enrolled gateway nodes", "optional, comma-separated", "Format JSON", "JSON.stringify(d,null,2)"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatal("missing policy guidance or formatting", text)
		}
	}
}

func TestMultiplePatternRuleAPI(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}}})
	rule := policy.Rule{Name: "Two domains", HostnameRegexes: []string{`api[.]example[.]com`, `app[0-9]{1,3}[.]example[.]net`}, Enabled: true}
	w := requestManagement(c, "POST", "/api/policies/rule", map[string]any{"revision": c.loadExposurePolicy().Revision, "rule": rule}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d := c.loadExposurePolicy()
	if len(d.Rules) != 1 || len(d.Rules[0].HostnameRegexes) != 2 {
		t.Fatal(d)
	}
	rule.HostnameRegexes = []string{`api[.]example[.]com`}
	w = requestManagement(c, "PUT", "/api/policies/rules/"+d.Rules[0].ID, map[string]any{"revision": d.Revision, "rule": rule}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d = c.loadExposurePolicy()
	if len(d.Rules) != 1 || len(d.Rules[0].HostnameRegexes) != 1 {
		t.Fatal("edit duplicated or retained pattern", d)
	}
	if ok, _ := d.Allows("app12.example.net", nil, nil); ok {
		t.Fatal("removed pattern still authorized")
	}
	for _, value := range []any{"scalar", []string{"["}, []string{}} {
		bad := map[string]any{"revision": d.Revision, "rule": map[string]any{"hostname_regexes": value, "enabled": true}}
		w = requestManagement(c, "POST", "/api/policies/rule", bad, "owner")
		if w.Code != 400 {
			t.Fatal("invalid patterns accepted", value, w.Code)
		}
	}
	w = requestManagement(c, "POST", "/api/policies/rule", map[string]any{"revision": d.Revision, "rule": map[string]any{"hostname_regex": "old", "enabled": true}}, "owner")
	if w.Code != 400 {
		t.Fatal("removed field accepted")
	}
}

func installTestEdge(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.UpsertNode("edge-id", "loadbalancer", "public-edge"); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := report.GenerateKey()
	n, _ := s.Node("edge-id")
	if n.TailnetID == "" {
		_ = s.SetSecret("test-edge-key", encodePublicKey(key))
		if err := s.BindNodeIdentity("edge-id", encodePublicKey(pub), "edge-tailnet", nil); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.SetJSON("node_health:edge-id", loadBalancerHealth{ReceivedAt: time.Now().Unix()})
	_ = s.SetJSON("node_endpoints:edge-id", map[string]any{"public_ipv4": []string{"203.0.113.5"}, "checked_at": time.Now().Unix()})
	if err := s.SetJSON("node_deployment:edge-id", nodeDeployment{LoadBalancerHostname: "edge.example.com"}); err != nil {
		t.Fatal(err)
	}
}
func testZoneID(t *testing.T, s *store.Store, domain string) string {
	t.Helper()
	zones, err := s.Zones()
	if err != nil {
		t.Fatal(err)
	}
	for _, z := range zones {
		if z.Zone == domain {
			return z.ID
		}
	}
	t.Fatalf("zone %s missing", domain)
	return ""
}
