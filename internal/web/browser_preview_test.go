package web

import (
	"context"
	"encoding/json"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Opt-in local fixture used for browser QA; it is never linked into the binary.
func TestBrowserPreview(t *testing.T) {
	if os.Getenv("PF_UI_PREVIEW") != "1" {
		t.Skip("local browser fixture")
	}
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "management_url": "http://control.example.ts.net:8080", "release_version": "v1.0.2", "loadbalancer_hostname": "edge.example.com"})
	f := installDNSFixture(t, s, "example.com")
	previous := dns.Registry["cloudflare"]
	dns.Registry["cloudflare"] = func(map[string]string) (dns.Provider, error) { return f, nil }
	t.Cleanup(func() { dns.Registry["cloudflare"] = previous })
	_ = s.DeleteZone("fixture", "example.com")
	_ = s.DeleteProvider("fixture")
	_ = s.SaveProvider("cloudflare-dev", "cloudflare", map[string]string{"api_token": "synthetic-secret", "account_id": "synthetic-account"}, true)
	_ = s.AddZone("cloudflare-dev", "example.com")
	_ = s.UpsertNode("ingress-dev", "ingress", "development-ingress")
	_ = s.UpsertNode("edge-dev", "loadbalancer", "public-edge")
	_ = s.SetJSON("node_deployment:edge-dev", nodeDeployment{LoadBalancerHostname: "edge.example.com"})
	_ = s.SetJSON("node_deployment:ingress-dev", nodeDeployment{})
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:preview"}, "viewer": {"node:viewer"}, "operator": {"node:operator"}}})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Development apps", HostnameRegexes: []string{`[^.]+[.]example[.]com`}, IngressIDs: []string{"ingress-dev"}, LoadBalancerIDs: []string{"edge-dev"}, Enabled: true}}})
	_ = s.SetSecret("tailscale_oauth_client_id", "synthetic")
	_ = s.SetSecret("tailscale_oauth_client_secret", "synthetic")
	serviceAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "oauth/token") {
			fmt := map[string]string{"access_token": "synthetic"}
			_ = json.NewEncoder(w).Encode(fmt)
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/keys") {
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "tskey-auth-SYNTHETIC-PREVIEW"})
			return
		}
		if r.Method == "PUT" {
			var v tailscale.Service
			_ = json.NewDecoder(r.Body).Decode(&v)
			v.Addresses = []string{"100.70.1.1"}
			_ = json.NewEncoder(w).Encode(v)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"devices": []any{}})
	}))
	defer serviceAPI.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: serviceAPI.Client(), APIBase: serviceAPI.URL, OAuthBase: serviceAPI.URL, Tailnet: "example.ts.net"}
	c.ReleaseCheck = func(context.Context, string) error { return nil }
	pub, priv, _ := report.GenerateKey()
	_ = s.BindNodeIdentity("ingress-dev", encodePublicKey(pub), "synthetic-ingress-device", []string{"100.70.0.9"})
	_ = s.RenameResource("node", "ingress-dev", "Development ingress")
	_ = s.RenameResource("node", "edge-dev", "Public edge")
	_ = s.RenameResource("provider", "cloudflare-dev", "Development DNS")
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "demo-app", Name: "Checkout demo", ServiceName: "svc:demo-checkout", ServiceFQDN: "svc:demo-checkout", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"checkout.example.com"}, AllowedIngressNodes: []string{"ingress-dev"}, Enabled: true})
	record := libdns.RR{Name: "checkout.example.com", Type: "CNAME", Data: "edge.example.com"}
	f.records["example.com"] = []libdns.Record{record}
	_ = s.ReplaceLedger("cloudflare-dev", "example.com", "ingress:ingress-dev", []libdns.Record{record})
	_ = s.SetJSON("zone_sync:cloudflare-dev:example.com", "Reconciled")
	zones, _ := s.Zones()
	for _, z := range zones {
		_ = s.SetJSON("zone_last_sync:"+z.ID, time.Now().Unix())
	}
	seedPeers := func() {
		envelope, _ := report.NewEnvelope("ingress-dev", map[string]any{"exposures": []dns.IngressExposure{{ApplicationID: "demo-app", ServiceName: "svc:demo-checkout", PublicHostname: "checkout.example.com", Port: 8080, BackendHostname: "checkout", Networks: []string{"demo-network"}, Reachable: true}}, "expires_at": time.Now().Add(5 * time.Minute).Unix()}, pub, priv, time.Now())
		raw, _ := json.Marshal(envelope)
		_ = s.AcceptReport("ingress-dev", envelope.Nonce, raw, envelope.PublicKey)

		var probes []nodeconfig.Probe
		_ = s.GetJSON("node_probes:ingress-dev", &probes)
		snapshot := nodeconfig.PeerSnapshot{Sources: []string{"docker", "tailnet"}, Tags: []string{"tag:tailscale-private-funnel", "tag:development"}, Roles: []string{"node:ingress-dev"}, ReceivedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), Peers: []nodeconfig.Peer{{ID: "test-backend", Hostname: "test-backend", DNSName: "test-backend.example.ts.net", Addresses: []string{"100.70.0.1"}, Online: true}}}
		for _, p := range probes {
			if p.ExpiresAt > time.Now().Unix() {
				snapshot.Results = append(snapshot.Results, nodeconfig.ProbeResult{ID: p.ID, DeviceID: p.DeviceID, Port: p.Port, Reachable: true, CheckedAt: time.Now().Unix()})
			}
		}
		_ = s.SetJSON("node_peers:ingress-dev", snapshot)
	}
	seedPeers()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			seedPeers()
		}
	}()
	t.Log("Synthetic preview http://127.0.0.1:8097")
	h := c.Handler()
	if err := http.ListenAndServe("127.0.0.1:8097", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node := "preview"
		if cookie, err := r.Cookie("preview-role"); err == nil && (cookie.Value == "viewer" || cookie.Value == "operator") {
			node = cookie.Value
		}
		h.ServeHTTP(w, WithPrincipal(r, Principal{NodeID: node, Login: "preview@example.com"}))
	})); err != nil {
		t.Fatal(err)
	}
}
