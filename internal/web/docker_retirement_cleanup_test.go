package web

import (
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDockerRetirementCleanupRetriesAndPreservesSharedServices(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("docker", "docker-gateway", "docker")
	_ = s.BindNodeIdentity("docker", "key", "device", nil)
	a := store.ApplicationRecord{ID: "old", BackendSource: "docker", ServiceName: "svc:tailscale-private-funnel-app-old", ServiceFQDN: "100.70.0.1", BackendPort: 8080, ServicePort: 8080, AllowedGatewayNodes: []string{"docker"}, PublicHostnames: []string{"old.example.com"}, Enabled: true}
	if err := s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptDockerInventory("docker", "key", "removed", store.DockerInventory{Revision: 1}); err != nil {
		t.Fatal(err)
	}
	_ = s.SetJSON("tailnet", map[string]string{"name": "test"})
	_ = s.SetSecret("tailscale_oauth_client_id", "fixture")
	_ = s.SetSecret("tailscale_oauth_client_secret", "fixture")
	fail := true
	deletes := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture"})
			return
		}
		if r.Method == "DELETE" {
			deletes++
			if fail {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"hosts": []any{}})
	}))
	defer api.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL, ClientID: "fixture", ClientSecret: "fixture", Tailnet: "test"}
	shared := a
	shared.ID = "shared"
	shared.BackendSource = "kubernetes"
	shared.PublicHostnames = []string{"other.example.com"}
	shared.AllowedGatewayNodes = []string{"kube"}
	if err := s.UpsertApplication(shared); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconcileDockerRetirements(t.Context()); err != nil || deletes != 0 {
		t.Fatal("shared service touched", err, deletes)
	}
	shared.PublicHostnames = nil
	shared.Enabled = false
	if err := s.UpsertApplication(shared); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconcileDockerRetirements(t.Context()); err == nil || deletes != 1 {
		t.Fatal("cleanup failure not retained", err, deletes)
	}
	pending, _ := s.PendingDockerRetirements()
	if len(pending) != 1 {
		t.Fatal("cleanup proof lost", pending)
	}
	fail = false
	if err := c.ReconcileDockerRetirements(t.Context()); err != nil || deletes != 2 {
		t.Fatal(err, deletes)
	}
	pending, _ = s.PendingDockerRetirements()
	if len(pending) != 0 {
		t.Fatal("cleanup did not finish", pending)
	}
	n, _ := s.Node("docker")
	if n.Revoked {
		t.Fatal("shared node revoked")
	}
}
