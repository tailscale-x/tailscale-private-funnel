package web

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func seedEdgeReport(t *testing.T, s *store.Store, id string) {
	t.Helper()
	secret, err := s.GetSecret("test-edge-key")
	if err != nil {
		t.Fatal(err)
	}
	rawkey, err := base64.RawStdEncoding.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.PrivateKey(rawkey)
	pub := key.Public().(ed25519.PublicKey)
	apps, err := s.Applications()
	if err != nil {
		t.Fatal(err)
	}
	es := []dns.IngressExposure{}
	for _, a := range apps {
		for _, host := range a.PublicHostnames {
			e := dns.IngressExposure{ApplicationID: a.ID, PublicHostname: host, Port: a.BackendPort, Reachable: true}
			if a.BackendSource == "tailnet" {
				e.BackendDeviceID = a.BackendDeviceID
			} else {
				e.ServiceName = a.ServiceName
			}
			es = append(es, e)
		}
	}
	env, err := report.NewEnvelope(id, map[string]any{"exposures": es, "expires_at": time.Now().Add(5 * time.Minute).Unix()}, pub, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env)
	if err = s.AcceptReport(id, env.Nonce, raw, env.PublicKey); err != nil {
		t.Fatal(err)
	}
}
func exposureLedger(t *testing.T, s *store.Store, provider, zone, host string) ([]dns.LedgerEntry, error) {
	t.Helper()
	es, err := s.Exposures()
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		if e.Hostname == host {
			return s.Ledger(provider, zone, "exposure:"+e.ID)
		}
	}
	t.Fatalf("exposure missing: %s", host)
	return nil, nil
}

func TestExposureFanoutOverridesPolicyAndHealth(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{LoadBalancerHostname: "edge.example.com"})
	key, _ := s.GetSecret("test-edge-key")
	raw, _ := base64.RawStdEncoding.DecodeString(key)
	pub := ed25519.PrivateKey(raw).Public().(ed25519.PublicKey)
	add := func(id, ip string) {
		t.Helper()
		if err := s.UpsertNode(id, "loadbalancer", id); err != nil {
			t.Fatal(err)
		}
		if err := s.BindNodeIdentity(id, encodePublicKey(pub), "tail-"+id, nil); err != nil {
			t.Fatal(err)
		}
		_ = s.SetJSON("node_health:"+id, loadBalancerHealth{ReceivedAt: time.Now().Unix()})
		_ = s.SetJSON("node_endpoints:"+id, map[string]any{"public_ipv4": []string{ip}, "checked_at": time.Now().Unix()})
		seedEdgeReport(t, s, id)
	}
	add("edge-two", "203.0.113.6")
	es, _ := s.Exposures()
	e := es[0]
	records, states := c.exposureDesired(e, c.loadExposurePolicy())
	if len(records) != 2 || len(states) != 2 {
		t.Fatalf("default fanout: %+v %+v", records, states)
	}
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.records["example.com"]) != 2 {
		t.Fatal(f.records)
	}
	_ = s.SetExposureLoadBalancers(e.ID, []string{"edge-two"})
	e, _ = s.Exposure(e.ID)
	records, _ = c.exposureDesired(e, c.loadExposurePolicy())
	if len(records) != 1 || records[0].RR().Data != "203.0.113.6" {
		t.Fatal(records)
	}
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	add("edge-three", "203.0.113.7")
	records, _ = c.exposureDesired(e, c.loadExposurePolicy())
	if len(records) != 1 {
		t.Fatal("override included new edge", records)
	}
	_ = s.SetExposureLoadBalancers(e.ID, nil)
	e, _ = s.Exposure(e.ID)
	records, _ = c.exposureDesired(e, c.loadExposurePolicy())
	if len(records) != 3 {
		t.Fatal("new edge absent from default", records)
	}
	_ = s.SetJSON("node_endpoints:edge-three", map[string]any{"public_ipv4": []string{"203.0.113.6"}, "checked_at": time.Now().Unix()})
	records, _ = c.exposureDesired(e, c.loadExposurePolicy())
	if len(records) != 2 {
		t.Fatal("duplicate IP", records)
	}
	d := c.loadExposurePolicy()
	d.Rules[0].LoadBalancerIDs = []string{"edge-id"}
	records, _ = c.exposureDesired(e, d)
	if len(records) != 1 || records[0].RR().Data != "203.0.113.5" {
		t.Fatal("policy bypass", records)
	}
	_ = s.SetJSON("node_health:edge-id", loadBalancerHealth{ReceivedAt: time.Now().Add(-10 * time.Minute).Unix()})
	records, _ = c.exposureDesired(e, d)
	if len(records) != 0 {
		t.Fatal("stale edge", records)
	}
	_ = s.SetJSON("node_health:edge-id", loadBalancerHealth{ReceivedAt: time.Now().Unix()})
	_ = s.DeleteReport("edge-id")
	records, _ = c.exposureDesired(e, d)
	if len(records) != 0 {
		t.Fatal("expired backend report", records)
	}
}
func TestExposureOverrideAPIAuthorizationAndValidation(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	es, _ := s.Exposures()
	path := "/api/exposures/" + es[0].ID + "/loadbalancers"
	for _, body := range []any{map[string]any{}, map[string]any{"loadbalancer_ids": []string{}}, map[string]any{"loadbalancer_ids": []string{"missing"}}, map[string]any{"loadbalancer_ids": []string{"ingress"}}, map[string]any{"loadbalancer_ids": []string{"edge-id", "edge-id"}}, map[string]any{"loadbalancer_id": "edge-id"}} {
		w := requestManagement(c, "PUT", path, body, "owner")
		if w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	for _, role := range []string{"viewer", "operator"} {
		raw, _ := json.Marshal(map[string]any{"loadbalancer_ids": nil})
		r := WithPrincipal(httptest.NewRequest("PUT", path, bytes.NewReader(raw)), Principal{NodeID: role})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	for _, ids := range [][]string{{"edge-id"}, nil} {
		w := requestManagement(c, "PUT", path, map[string]any{"loadbalancer_ids": ids}, "owner")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		e, _ := s.Exposure(es[0].ID)
		if !reflect.DeepEqual(e.LoadBalancerIDs, ids) {
			t.Fatal(e)
		}
	}
	w := requestManagement(c, "GET", "/enroll/ingress", nil, "owner")
	if strings.Contains(w.Body.String(), `name="loadbalancer_id"`) {
		t.Fatal("ingress still bound to edge")
	}
	w = requestManagement(c, "GET", "/exposures", nil, "owner")
	if !strings.Contains(w.Body.String(), "All load balancers") || !strings.Contains(w.Body.String(), "Selected load balancers") {
		t.Fatal(w.Body.String())
	}
}
func TestOwnedCNAMEConversionConflictAndFailure(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	es, _ := s.Exposures()
	e := es[0]
	owner := "exposure:" + e.ID
	prior := libdns.RR{Name: e.Hostname, Type: "CNAME", Data: "edge.example.com"}
	unrelated := libdns.RR{Name: "keep.example.com", Type: "TXT", Data: "untouched"}
	f.records["example.com"] = []libdns.Record{prior, unrelated}
	_ = s.ReplaceLedger("fixture", "example.com", owner, []libdns.Record{prior})
	f.fail = "example.com"
	if err := c.reconcileExposures(context.Background()); err == nil {
		t.Fatal("provider failure hidden")
	}
	ledger, _ := s.Ledger("fixture", "example.com", owner)
	if len(ledger) != 1 || ledger[0].Record.RR().Type != "CNAME" {
		t.Fatal("failed mutation changed ledger", ledger)
	}
	f.fail = ""
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	ledger, _ = s.Ledger("fixture", "example.com", owner)
	if len(ledger) != 1 || ledger[0].Record.RR().Type != "A" || len(f.records["example.com"]) != 2 {
		t.Fatal(ledger, f.records)
	}
	f.records["example.com"] = append(f.records["example.com"], libdns.RR{Name: e.Hostname, Type: "A", Data: "203.0.113.99"})
	if err := c.reconcileExposures(context.Background()); err == nil {
		t.Fatal("unowned overwrite allowed")
	}
	if len(f.records["example.com"]) != 3 {
		t.Fatal("unowned record modified")
	}
}
func TestAmbiguousEdgeRetainsOnlyItsOwnPriorIP(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	e, _ := s.Node("edge-id")
	_ = s.SetJSON("node_endpoints:edge-id", map[string]any{"public_ipv4": []string{"203.0.113.9", "203.0.113.10"}, "checked_at": time.Now().Unix()})
	ips, degraded := c.edgeIPs(e)
	if len(ips) != 0 || !degraded {
		t.Fatal(ips, degraded)
	}
	_ = s.ReplaceLedger("fixture", "example.com", "loadbalancer:edge-id", []libdns.Record{libdns.RR{Name: "edge.example.com", Type: "A", Data: "203.0.113.5"}})
	ips, degraded = c.edgeIPs(e)
	if len(ips) != 1 || ips[0] != "203.0.113.5" || !degraded {
		t.Fatal(ips, degraded)
	}
}

func TestTwoDirectEdgesReceiveNodeBoundConfigurationWithoutServices(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "direct", HostnameRegexes: []string{`voice[.]example[.]com`}, Enabled: true}}})
	app := store.ApplicationRecord{ID: "voice", BackendSource: "tailnet", BackendDeviceID: "backend", BackendPort: 8766, ServiceName: "direct:voice", PublicHostnames: []string{"voice.example.com"}, Enabled: true}
	if err := s.UpsertApplication(app); err != nil {
		t.Fatal(err)
	}
	secret, _ := s.GetSecret("test-edge-key")
	keyBytes, _ := base64.RawStdEncoding.DecodeString(secret)
	key := ed25519.PrivateKey(keyBytes)
	pub := key.Public().(ed25519.PublicKey)
	_ = s.UpsertNode("edge-two", "loadbalancer", "edge-two")
	_ = s.BindNodeIdentity("edge-two", encodePublicKey(pub), "tail-edge-two", nil)
	for _, id := range []string{"edge-id", "edge-two"} {
		node, _ := s.Node(id)
		_ = s.SetJSON("node_health:"+id, loadBalancerHealth{ReceivedAt: time.Now().Unix()})
		_ = s.SetJSON("node_endpoints:"+id, map[string]any{"public_ipv4": []string{"203.0.113.5"}, "checked_at": time.Now().Unix()})
		_ = s.SetJSON("node_peers:"+id, nodeconfig.PeerSnapshot{ReceivedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), Peers: []nodeconfig.Peer{{ID: "backend", Addresses: []string{"100.70.0.1"}}}})
		env, _ := report.NewEnvelope(id, map[string]any{"exposures": []dns.IngressExposure{{ApplicationID: "voice", BackendDeviceID: "backend", BackendIP: "100.70.0.1", PublicHostname: "voice.example.com", Port: 8766, Reachable: true}}, "expires_at": time.Now().Add(5 * time.Minute).Unix()}, pub, key, time.Now())
		raw, _ := json.Marshal(env)
		r := WithPrincipal(httptest.NewRequest("POST", "/api/reports", bytes.NewReader(raw)), Principal{NodeID: node.TailnetID, Tagged: true, Tags: []string{"tag:tailscale-private-funnel"}})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatal(id, w.Code, w.Body.String())
		}
		r = WithPrincipal(httptest.NewRequest("GET", "/api/node/config", nil), Principal{NodeID: node.TailnetID, Tagged: true, Tags: []string{"tag:tailscale-private-funnel"}})
		w = httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var signed report.Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &signed); err != nil {
			t.Fatal(err)
		}
		controlPub, _, _ := s.ConfigurationKey()
		config, err := nodeconfig.Verify(signed, encodePublicKey(controlPub), id, time.Now())
		if err != nil || len(config.Routes) != 1 || config.Routes[0].BackendDeviceID != "backend" || config.Routes[0].Service != "" {
			t.Fatal(config, err)
		}
	}
	es, _ := s.Exposures()
	_ = s.SetExposureLoadBalancers(es[0].ID, []string{"edge-id"})
	r := WithPrincipal(httptest.NewRequest("GET", "/api/node/config", nil), Principal{NodeID: "tail-edge-two", Tagged: true})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	var env report.Envelope
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	var config nodeconfig.Configuration
	_ = json.Unmarshal(env.Payload, &config)
	if len(config.Routes) != 0 || len(config.Applications) != 0 {
		t.Fatal("override left direct assignment on excluded edge", config)
	}
	services, err := s.ServiceHosts()
	if err != nil || len(services) != 0 {
		t.Fatal("direct exposure created Service hosts", services, err)
	}
}

func TestReportExposureCountDeduplicatesEdgeAndIngress(t *testing.T) {
	reports := map[string]string{"ingress": `{"payload":{"exposures":[{"public_hostname":"app.example.com"}]}}`, "edge-one": `{"payload":{"exposures":[{"public_hostname":"app.example.com"}]}}`, "edge-two": `{"payload":{"exposures":[{"public_hostname":"app.example.com"},{"public_hostname":"alias.example.com"}]}}`}
	if got := reportExposureCount(reports); got != 2 {
		t.Fatal(got)
	}
}
