package web

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/libdns/libdns"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestDirectReportWhoIsReplayAndOwnedCleanup(t *testing.T) {
	c, s := testControl(t)
	provider := installDNSFixture(t, s, "example.com")
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	_ = s.UpsertNodeEnrollment("edge", "Edge", "loadbalancer", "edge")
	_ = s.BindNodeIdentity("edge", base64.RawStdEncoding.EncodeToString(pub), "other-edge-tailnet", nil)
	_ = s.SetJSON("node_deployment:edge", nodeDeployment{LoadBalancerHostname: "edge.example.com"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "direct", HostnameRegexes: []string{`voice[.]example[.]com`}, Enabled: true}}})
	_ = s.SetJSON("node_peers:edge", nodeconfig.PeerSnapshot{Sources: []string{"tailnet"}, Peers: []nodeconfig.Peer{{ID: "backend", Addresses: []string{"100.70.0.1"}}}, ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), ReceivedAt: time.Now().Unix()})
	app := store.ApplicationRecord{ID: "voice", Name: "Voice", ServiceName: "direct:voice", BackendSource: "tailnet", BackendDeviceID: "backend", BackendPort: 8766, PublicHostnames: []string{"voice.example.com"}, Enabled: true}
	if err := s.UpsertApplication(app); err != nil {
		t.Fatal(err)
	}
	address := libdns.RR{Name: "edge.example.com", Type: "A", Data: "203.0.113.5", TTL: time.Minute}
	provider.records["example.com"] = []libdns.Record{address}
	if err := s.ReplaceLedger("fixture", "example.com", "loadbalancer:edge", []libdns.Record{address}); err != nil {
		t.Fatal(err)
	}
	send := func(reachable bool, whois string) (*httptest.ResponseRecorder, report.Envelope) {
		exposure := map[string]any{"application_id": "voice", "backend_device_id": "backend", "public_hostname": "voice.example.com", "backend_ip": "100.70.0.1", "port": 8766, "reachable": reachable}
		env, err := report.NewEnvelope("edge", map[string]any{"exposures": []any{exposure}, "expires_at": time.Now().Add(5 * time.Minute).Unix()}, pub, priv, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(env)
		r := WithPrincipal(httptest.NewRequest("POST", "/api/reports", bytes.NewReader(body)), Principal{NodeID: whois, Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w, env
	}
	if w, _ := send(true, "spoof"); w.Code != 403 {
		t.Fatalf("spoof: %d", w.Code)
	}
	_ = s.SetJSON("node_health:edge", loadBalancerHealth{ReceivedAt: time.Now().Unix()})
	w, env := send(true, "other-edge-tailnet")
	if w.Code != 202 {
		t.Fatalf("direct report: %d %s", w.Code, w.Body.String())
	}
	entries, err := exposureLedger(t, s, "fixture", "example.com", "voice.example.com")
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledger: %v %v", entries, err)
	}
	if err := c.ReconcilePolicy(httptest.NewRequest("GET", "/", nil).Context()); err != nil {
		t.Fatal(err)
	}
	entries, _ = exposureLedger(t, s, "fixture", "example.com", "voice.example.com")
	if len(entries) != 1 {
		t.Fatalf("policy deleted load balancer A or CNAME: %v", entries)
	}
	page := requestManagement(c, "GET", "/exposures", nil, "owner")
	if page.Code != 200 || !bytes.Contains(page.Body.Bytes(), []byte("Load balancer → backend machine")) {
		t.Fatalf("direct path missing from exposures page: %d", page.Code)
	}
	encoded, _ := json.Marshal(env)
	req := WithPrincipal(httptest.NewRequest("POST", "/api/reports", bytes.NewReader(encoded)), Principal{NodeID: "other-edge-tailnet", Tagged: true})
	w = httptest.NewRecorder()
	c.Handler().ServeHTTP(w, req)
	if w.Code != 409 {
		t.Fatalf("replay: %d", w.Code)
	}
	if w, _ := send(false, "other-edge-tailnet"); w.Code != 202 {
		t.Fatalf("backend failure: %d %s", w.Code, w.Body.String())
	}
	entries, _ = exposureLedger(t, s, "fixture", "example.com", "voice.example.com")
	if len(entries) != 0 || len(provider.records["example.com"]) != 1 {
		t.Fatalf("owned DNS remained: %v %v", entries, provider.records)
	}
}

func TestLoadBalancerConfigurationSignatureSurvivesHTTPEncoding(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNodeEnrollment("edge", "Edge", "loadbalancer", "edge")
	_ = s.BindNodeIdentity("edge", "unused-signing-key", "other-edge-tailnet", nil)
	req := WithPrincipal(httptest.NewRequest("GET", "/api/node/config", nil), Principal{NodeID: "other-edge-tailnet", Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("config: %d %s", w.Code, w.Body.String())
	}
	var env report.Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	pub, _, err := s.ConfigurationKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := nodeconfig.Verify(env, base64.RawStdEncoding.EncodeToString(pub), "edge", time.Now()); err != nil {
		t.Fatalf("HTTP config signature: %v body=%s", err, w.Body.String())
	}
}
