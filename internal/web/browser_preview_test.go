package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	core "k8s.io/api/core/v1"
)

// Opt-in local fixture used for browser QA; it is never linked into the binary.
func TestBrowserPreview(t *testing.T) {
	if os.Getenv("PF_UI_PREVIEW") != "1" {
		t.Skip("local browser fixture")
	}
	c, s := testControl(t)
	c.BuildVersion = "dev-operator-qa"
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "management_url": "http://control.example.ts.net:8080", "release_version": "v1.0.2", "loadbalancer_hostname": "edge.example.com"})
	f := installDNSFixture(t, s, "example.com")
	previous := dns.Registry["cloudflare"]
	proxyFixture := &proxyDNSFixture{fixtureDNS: f}
	dns.Registry["cloudflare"] = func(map[string]string) (dns.Provider, error) { return proxyFixture, nil }
	t.Cleanup(func() { dns.Registry["cloudflare"] = previous })
	_ = s.DeleteZone("fixture", "example.com")
	_ = s.DeleteProvider("fixture")
	_ = s.SaveProvider("cloudflare-dev", "cloudflare", map[string]string{"api_token": "synthetic-secret", "account_id": "synthetic-account"}, true)
	_ = s.AddZone("cloudflare-dev", "example.com")
	_ = s.UpsertNode("gateway-dev", "docker-gateway", "development-gateway")
	_ = s.UpsertNode("edge-dev", "loadbalancer", "public-edge")
	_ = s.SetJSON("node_deployment:edge-dev", nodeDeployment{LoadBalancerHostname: "edge.example.com", CertEmail: "admin@example.com", DeploymentTarget: "native", ResourceName: "public-edge", Version: "dev-operator-qa"})
	_ = s.SetJSON("node_deployment:gateway-dev", nodeDeployment{DeploymentTarget: "native", ResourceName: "development-gateway", Version: "dev-operator-qa"})
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:preview"}, "viewer": {"node:viewer"}, "operator": {"node:operator"}}})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Development apps", HostnameRegexes: []string{`[^.]+[.]example[.]com`}, GatewayIDs: []string{"gateway-dev"}, Enabled: true}}})
	_ = s.SetSecret("tailscale_services_client_id", "synthetic")
	_ = s.SetSecret("tailscale_services_client_secret", "synthetic")
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

	_ = bindTestNode(s, "gateway-dev", "synthetic-gateway-device", []string{"100.70.0.9"})
	_ = s.RenameResource("node", "gateway-dev", "Development gateway")
	_ = s.RenameResource("node", "edge-dev", "Public edge")
	_ = s.RenameResource("provider", "cloudflare-dev", "Development DNS")
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "demo-app", Name: "Checkout demo", ServiceName: "svc:demo-checkout", ServiceFQDN: "svc:demo-checkout", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"checkout.example.com"}, AllowedGatewayNodes: []string{"gateway-dev"}, Enabled: true})
	record := libdns.RR{Name: "checkout.example.com", Type: "CNAME", Data: "edge.example.com"}
	f.records["example.com"] = []libdns.Record{record}
	exposures, _ := s.Exposures()
	for _, exposure := range exposures {
		if exposure.Hostname == "checkout.example.com" {
			_ = s.ReplaceLedger("cloudflare-dev", "example.com", "exposure:"+exposure.ID, []libdns.Record{record})
		}
	}
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{LoadBalancerHostname: "edge.example.com", AddressSource: "kubernetes", KubernetesNamespace: "example-system", PublicService: "example-public"})

	_ = s.SetJSON("zone_sync:cloudflare-dev:example.com", "Reconciled")
	zones, _ := s.Zones()
	for _, z := range zones {
		_ = s.SetJSON("zone_last_sync:"+z.ID, time.Now().Unix())
	}
	seedPeers := func() {
		seedEdgeReport(t, s, "edge-id")
		_ = s.SetJSON("node_health:edge-id", loadBalancerHealth{ReceivedAt: time.Now().Unix()})
		_ = s.SetJSON("node_public_endpoint:edge-id", publicEndpoint{Namespace: "example-system", Service: "example-public", UID: "synthetic-service", ObservedAt: time.Now().Unix(), Ingress: []core.LoadBalancerIngress{{IP: "203.0.113.5"}}})
		_ = s.SetJSON("node_endpoints:edge-id", map[string]any{"public_ipv4": []string{"203.0.113.5"}, "checked_at": time.Now().Unix()})
		envelope, _ := report.NewSnapshot("report", "gateway-dev", map[string]any{"exposures": []dns.GatewayExposure{{ApplicationID: "demo-app", ServiceName: "svc:demo-checkout", PublicHostname: "checkout.example.com", Port: 8080, BackendHostname: "checkout", Networks: []string{"demo-network"}, Reachable: true}}, "expires_at": time.Now().Add(5 * time.Minute).Unix()}, time.Now())
		raw, _ := json.Marshal(envelope)
		_ = s.AcceptReport("gateway-dev", envelope.Nonce, raw)

		var probes []nodeconfig.Probe
		_ = s.GetJSON("node_probes:gateway-dev", &probes)
		_ = s.UpsertNode("tailnet-dev", "tailnet-gateway", "tailnet-dev")
		_ = s.SetJSON("node_peers:tailnet-dev", nodeconfig.PeerSnapshot{Sources: []string{"tailnet"}, Tags: []string{"tag:tailscale-private-funnel"}, ReceivedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), Peers: []nodeconfig.Peer{{ID: "test-backend", Hostname: "test-backend", Addresses: []string{"100.70.0.1"}, Online: true}}})
		snapshot := nodeconfig.PeerSnapshot{Sources: []string{"docker", "tailnet"}, Tags: []string{"tag:tailscale-private-funnel", "tag:development"}, Roles: []string{"node:gateway-dev"}, ReceivedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), Peers: []nodeconfig.Peer{{ID: "test-backend", Hostname: "test-backend", DNSName: "test-backend.example.ts.net", Addresses: []string{"100.70.0.1"}, Online: true}}}
		for _, p := range probes {
			if p.ExpiresAt > time.Now().Unix() {
				snapshot.Results = append(snapshot.Results, nodeconfig.ProbeResult{ID: p.ID, DeviceID: p.DeviceID, Port: p.Port, Reachable: true, CheckedAt: time.Now().Unix()})
			}
		}
		_ = s.SetJSON("node_peers:gateway-dev", snapshot)
	}
	for i, path := range []string{"/api", "/admin"} {
		id := fmt.Sprintf("path-%d", i)
		_ = s.UpsertApplication(store.ApplicationRecord{ID: id, BackendSource: "docker", ServiceName: "svc:" + id, ServiceFQDN: id + ".example.ts.net", ServicePort: 8080, BackendPort: uint16(8080 + i), PublicHostnames: []string{"paths.example.com"}, AllowedGatewayNodes: []string{"gateway-dev"}, Enabled: true, HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: path}}, Rewrite: &proxy.PrefixRewrite{ReplacePrefix: "/"}}}})
	}
	_ = s.Audit("node.enroll:gateway-dev", "preview@example.com")
	_ = s.Audit("policy.save:development", "preview@example.com")
	_ = s.Audit("dns.apply:cloudflare-dev:example.com", "preview@example.com")
	seedPeers()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			seedPeers()
		}
	}()
	address := os.Getenv("PF_UI_PREVIEW_ADDR")
	if address == "" {
		address = "127.0.0.1:8097"
	}
	t.Log("Synthetic preview http://" + address)
	h := c.controlHandler()
	if err := http.ListenAndServe(address, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		node := "preview"
		if cookie, err := r.Cookie("preview-role"); err == nil && (cookie.Value == "viewer" || cookie.Value == "operator") {
			node = cookie.Value
		}
		h.ServeHTTP(w, WithPrincipal(r, Principal{NodeID: node, Login: "preview@example.com"}))
	})); err != nil {
		t.Fatal(err)
	}
}
