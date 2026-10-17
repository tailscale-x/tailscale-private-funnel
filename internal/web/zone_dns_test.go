package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func zoneReportFixture(t *testing.T, c *Control, s *store.Store) {
	t.Helper()
	_ = s.UpsertNode("docker-gateway", "docker-gateway", "worker")
	pub, priv, _ := report.GenerateKey()
	if err := s.BindNodeIdentity("docker-gateway", encodePublicKey(pub), "tail-gateway", []string{"100.64.0.8"}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetJSON("node_deployment:docker-gateway", nodeDeployment{})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Apps", HostnameRegexes: []string{`.*[.]example[.]com`}, Enabled: true}}})
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "app", Name: "App", ServiceName: "svc:app", ServiceFQDN: "svc:app", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com"}, Enabled: true})
	e, err := report.NewEnvelope("docker-gateway", map[string]any{"exposures": []dns.GatewayExposure{{ApplicationID: "app", ServiceName: "svc:app", PublicHostname: "app.example.com", Port: 8080, Reachable: true}}, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, priv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e)
	if err := s.AcceptReport("docker-gateway", e.Nonce, raw, e.PublicKey); err != nil {
		t.Fatal(err)
	}
	seedEdgeReport(t, s, "edge-id")
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{})
}
