package web

import (
	"encoding/json"
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
)

func zoneReportFixture(t *testing.T, c *Control, s *store.Store) {
	t.Helper()
	_ = s.UpsertNode("ingress", "ingress", "worker")
	pub, priv, _ := report.GenerateKey()
	if err := s.BindNodeIdentity("ingress", encodePublicKey(pub), "tail-ingress", []string{"100.64.0.8"}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetJSON("node_deployment:ingress", nodeDeployment{})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Apps", HostnameRegexes: []string{`.*[.]example[.]com`}, Enabled: true}}})
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "app", Name: "App", ServiceName: "svc:app", ServiceFQDN: "svc:app", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com"}, Enabled: true})
	e, err := report.NewEnvelope("ingress", map[string]any{"exposures": []dns.IngressExposure{{ApplicationID: "app", ServiceName: "svc:app", PublicHostname: "app.example.com", Port: 8080, Reachable: true}}, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, priv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	if err := s.AcceptReport("ingress", e.Nonce, raw, e.PublicKey); err != nil {
		t.Fatal(err)
	}
	seedEdgeReport(t, s, "edge-id")
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{})
}
func TestZoneDNSPreviewApplyAndExpiry(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com", "example.net")
	zoneReportFixture(t, c, s)
	unrelated := libdns.RR{Name: "existing.example.com", Type: "TXT", Data: "keep"}
	f.records["example.com"] = []libdns.Record{unrelated}
	f.records["example.net"] = []libdns.Record{libdns.RR{Name: "other.example.net", Type: "TXT", Data: "keep"}}
	path := "/api/zones/" + testZoneID(t, s, "example.com") + "/sync"
	w := requestManagement(c, "POST", path, map[string]any{"dry_run": true}, "owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"add": 1`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(f.records["example.com"]) != 1 {
		t.Fatal("preview changed provider")
	}
	w = requestManagement(c, "POST", path, map[string]any{"dry_run": false}, "owner")
	if w.Code != 200 || len(f.records["example.com"]) != 2 {
		t.Fatal(w.Code, w.Body.String(), f.records)
	}
	var d policy.Document
	_ = s.GetJSON("exposure_policy", &d)
	d.Rules = nil
	_ = s.SetJSON("exposure_policy", d)
	w = requestManagement(c, "POST", path, map[string]any{"dry_run": false}, "owner")
	if w.Code != 200 || len(f.records["example.com"]) != 1 || len(f.records["example.net"]) != 1 {
		t.Fatal("cleanup affected unrelated records", w.Body.String(), f.records)
	}
}
func TestZoneDNSExplicitApprovalAndStalePreview(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	f.records["example.com"] = []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "unmanaged.example.net."}}
	path := "/api/zones/" + testZoneID(t, s, "example.com") + "/sync"
	w := requestManagement(c, "POST", path, map[string]any{"dry_run": false}, "owner")
	if !strings.Contains(w.Body.String(), `"pending": true`) || f.records["example.com"][0].RR().Data != "unmanaged.example.net." {
		t.Fatal(w.Body.String())
	}
	w = requestManagement(c, "POST", path, map[string]any{"dry_run": true}, "owner")
	var reply struct {
		Results []struct {
			NodeID string `json:"node_id"`
			Token  string `json:"approval_token"`
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &reply)
	if len(reply.Results) != 1 || reply.Results[0].Token == "" {
		t.Fatal(w.Body.String())
	}
	token := reply.Results[0].Token
	f.records["example.com"][0] = libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "changed.example.net."}
	w = requestManagement(c, "POST", path, map[string]any{"approvals": map[string]string{reply.Results[0].NodeID: token}}, "owner")
	if !strings.Contains(w.Body.String(), `"pending": true`) || f.records["example.com"][0].RR().Data != "changed.example.net." {
		t.Fatal("stale preview permitted overwrite", w.Body.String())
	}
	w = requestManagement(c, "POST", path, map[string]any{"dry_run": true}, "owner")
	_ = json.Unmarshal(w.Body.Bytes(), &reply)
	w = requestManagement(c, "POST", path, map[string]any{"approvals": map[string]string{reply.Results[0].NodeID: reply.Results[0].Token}}, "owner")
	if strings.Contains(w.Body.String(), `"pending": true`) {
		t.Fatal(w.Body.String())
	}
}
func TestZoneDNSRetainsAOnEndpointFailure(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	record := libdns.RR{Name: "edge.example.com", Type: "A", Data: "203.0.113.5"}
	f.records["example.com"] = []libdns.Record{record}
	_ = s.ReplaceLedger("fixture", "example.com", "loadbalancer:edge-id", []libdns.Record{record})
	_ = s.SetJSON("node_health:edge-id", loadBalancerHealth{ReceivedAt: time.Now().Unix()})
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{LoadBalancerHostname: "edge.example.com"})
	path := "/api/zones/" + testZoneID(t, s, "example.com") + "/sync"
	w := requestManagement(c, "POST", path, map[string]any{}, "owner")
	if !strings.Contains(w.Body.String(), "retained") || len(f.records["example.com"]) != 2 {
		t.Fatal(w.Body.String())
	}
}
func TestZoneDNSRolesAndRemovedEditor(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "viewer": {"node:viewer"}, "operator": {"node:operator"}}})
	id := testZoneID(t, s, "example.com")
	call := func(method, path, body, role string) *httptest.ResponseRecorder {
		r := WithPrincipal(httptest.NewRequest(method, path, strings.NewReader(body)), Principal{NodeID: role})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	for _, role := range []string{"viewer", "operator"} {
		w := call("GET", "/zones", "", role)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "DNS details") || strings.Contains(w.Body.String(), `id="zone-dns-retry"`) {
			t.Fatal(role, w.Body.String())
		}
		if w := call("POST", "/api/zones/"+id+"/sync", `{"dry_run":false}`, role); w.Code != http.StatusForbidden {
			t.Fatal(role, w.Code)
		}
	}
	if w := call("POST", "/api/zones/"+id+"/sync", `{"dry_run":true}`, "operator"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/api/zones/"+id+"/sync", `{"records":[]}`, "admin"); w.Code != 400 {
		t.Fatal("raw record contract accepted", w.Code)
	}
	if w := call("GET", "/api/zones/missing/dns", "", "admin"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	w := call("GET", "/operations", "", "admin")
	if !strings.Contains(w.Body.String(), "Audit history") || strings.Contains(w.Body.String(), "Desired records") || strings.Contains(w.Body.String(), "DNS operations") {
		t.Fatal(w.Body.String())
	}
}

func TestZoneDNSExpiredReportCleanup(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	path := "/api/zones/" + testZoneID(t, s, "example.com") + "/sync"
	w := requestManagement(c, "POST", path, map[string]any{}, "owner")
	if w.Code != 200 || len(f.records["example.com"]) != 1 {
		t.Fatal(w.Body.String())
	}
	reports, _ := s.Reports()
	var envelope report.Envelope
	_ = json.Unmarshal([]byte(reports["ingress"]), &envelope)
	var payload map[string]any
	_ = json.Unmarshal(envelope.Payload, &payload)
	payload["expires_at"] = time.Now().Add(-time.Minute).Unix()
	envelope.Payload, _ = json.Marshal(payload)
	raw, _ := json.Marshal(envelope)
	_ = s.SaveReport("ingress", raw)
	w = requestManagement(c, "POST", path, map[string]any{}, "owner")
	if w.Code != 200 || len(f.records["example.com"]) != 0 {
		t.Fatal("expired report records retained", w.Body.String())
	}
}

func TestEnrollmentBackLinks(t *testing.T) {
	c, _ := testControl(t)
	for mode, path := range map[string]string{"ingress": "/ingresses", "loadbalancer": "/loadbalancers"} {
		w := requestManagement(c, "GET", "/enroll/"+mode, nil, "owner")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `href="`+path+`">Back to`) || strings.Contains(w.Body.String(), "/ingresss") {
			t.Fatal(mode, w.Body.String())
		}
	}
}
