package web

import (
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstallationCleanupOwnershipAndActiveNodeGuard(t *testing.T) {
	c, s := testControl(t)
	s.UpsertNode("active", "tailnet-gateway", "active")
	if e := c.CleanupOwnedInstallation(t.Context()); e == nil {
		t.Fatal("deleted active installation")
	}
	s.RevokeNode("active")
	s.SetJSON("tailnet", map[string]string{"name": "test"})
	s.SetSecret("tailscale_oauth_client_id", "test")
	s.SetSecret("tailscale_oauth_client_secret", "test")
	s.UpsertApplication(store.ApplicationRecord{ID: "app", ServiceName: "svc:tailscale-private-funnel-app-app", ServicePort: 8080, BackendPort: 8080})
	deleted := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			writeJSON(w, map[string]string{"access_token": "test"})
			return
		}
		if r.Method == "DELETE" {
			deleted = true
			w.WriteHeader(204)
			return
		}
		writeJSON(w, map[string]any{"hosts": []map[string]string{{"nodeId": "foreign-device", "approvalLevel": "approved:manual"}}})
	}))
	defer api.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
	if e := c.CleanupOwnedInstallation(t.Context()); e == nil || deleted {
		t.Fatal("deleted Service with unknown host", e)
	}
}
