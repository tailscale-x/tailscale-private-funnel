package web

import (
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
	lookupFail := false
	hostApproval := "approved:manual"
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "oauth/token"):
			writeJSON(w, map[string]any{"access_token": "token"})
		case strings.HasSuffix(r.URL.Path, "/devices"):
			writeJSON(w, map[string]any{"hosts": []map[string]any{{"nodeId": "tailnode", "approvalLevel": hostApproval}}})
		case r.URL.Path == "/device/tailnode":
			if lookupFail {
				http.Error(w, "upstream unavailable", 503)
				return
			}
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
	s.SetJSON("node_policy_roles:docker-gateway", []string{"tag:tailscale-private-funnel"})
	lookupFail = true
	if err := c.ReconcileServiceApprovals(context.Background()); err == nil {
		t.Fatal("lookup failure not surfaced")
	}
	if disapproved != 0 {
		t.Fatal("transient lookup failure revoked healthy Service")
	}
	hostApproval = "pending"
	if err := c.ReconcileServiceApprovals(context.Background()); err == nil {
		t.Fatal("lookup failure hidden for pending host")
	}
	if disapproved != 0 {
		t.Fatal("pending host approved without verification")
	}
	hostApproval = "approved:manual"
	lookupFail = false

	d.Rules[0].Enabled = false
	_ = s.SetJSON("exposure_policy", d)
	if err := c.ReconcileServiceApprovals(context.Background()); err != nil {
		t.Fatal(err)
	}
	if disapproved != 0 {
		t.Fatal("shared Service was disapproved")
	}
	d.Rules[1].Enabled = false
	lookupFail = true // Explicit policy denial must still withdraw during lookup failure.
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
