package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestStaleGatewayPreservesPersistentIdentity(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("persistent", "tailnet-gateway", "persistent")

	_ = bindTestNode(s, "persistent", "stable-device", nil)
	env, _ := report.NewSnapshot("report", "persistent", map[string]any{"expires_at": time.Now().Add(time.Minute).Unix(), "exposures": []any{}}, time.Now())
	raw, _ := json.Marshal(env)
	if err := s.AcceptReport("persistent", env.Nonce, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupStaleNodes(context.Background(), -time.Hour); err != nil {
		t.Fatal(err)
	}
	n, err := s.Node("persistent")
	if err != nil || n.Revoked || n.TailnetID != "stable-device" {
		t.Fatal(n, err)
	}
}

func TestProgrammingWaitsForCurrentRouteAcknowledgment(t *testing.T) {
	c, s := testControl(t)
	a := store.ApplicationRecord{ID: "http", ServiceName: "svc:http", ServiceFQDN: "100.70.0.1", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com"}, Enabled: true, HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/old"}}}}}
	if err := s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	_ = s.SetJSON("node_sent_config:edge-id", map[string]any{"revision": "acked", "routes": []proxy.Route{{Hostname: "app.example.com", Service: a.ServiceFQDN, Port: 8080, Rules: a.HTTPRules}}})
	_ = s.SetJSON("node_config_ack:edge-id", map[string]any{"revision": "acked", "at": time.Now().Unix()})
	if len(c.publishedGatewayHosts()) != 1 {
		t.Fatal("acknowledged route not published")
	}
	a.HTTPRules[0].Matches[0].Path = "/new"
	_ = s.UpsertApplication(a)
	if len(c.publishedGatewayHosts()) != 0 {
		t.Fatal("old Caddy config acknowledged a new route")
	}
}

func TestRevokedGatewayCleanupToleratesRemovedServices(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetSecret("tailscale_services_client_id", "test-id")
	_ = s.SetSecret("tailscale_services_client_secret", "test-secret")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			writeJSON(w, map[string]string{"access_token": "test-token"})
			return
		}
		if strings.Contains(r.URL.Path, "/services/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/devices") {
			writeJSON(w, map[string]any{"devices": []any{}})
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
	_ = s.UpsertNode("gone", "k8s-gateway", "gone")
	_ = s.RevokeNode("gone")
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "gone-app", BackendSource: "kubernetes", ServiceName: "svc:tpf-k8s-removed", ServiceFQDN: "100.70.0.1", ServicePort: 80, BackendPort: 80, AllowedGatewayNodes: []string{"gone"}})
	n, _ := s.Node("gone")
	if err := c.cleanupRevokedNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	var result struct{ Pending bool }
	if err := s.GetJSON("node_cleanup:gone", &result); err != nil || result.Pending {
		t.Fatal(result, err)
	}
}
