package web

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestInstallationIssuerCredentialStorageAndRoleSelection(t *testing.T) {
	c, store := testControl(t)
	_ = store.SetJSON("tailnet", map[string]string{"name": "example.ts.net", "management_url": "http://control.example.ts.net:8080"})
	requested := []string{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			r.ParseForm()
			id, secret, ok := r.BasicAuth()
			if !ok {
				id = r.Form.Get("client_id")
				secret = r.Form.Get("client_secret")
			}
			if id != "one-id" || secret != "one-secret" {
				t.Error("wrong issuer")
			}
			writeJSON(w, map[string]string{"access_token": "token"})
			return
		}
		var key struct {
			Capabilities struct {
				Devices struct {
					Create struct {
						Tags []string `json:"tags"`
					} `json:"create"`
				} `json:"devices"`
			} `json:"capabilities"`
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/keys") {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&key); err != nil {
			t.Fatal(err)
		}
		if len(key.Capabilities.Devices.Create.Tags) != 1 {
			t.Error("not one role")
		}
		requested = append(requested, key.Capabilities.Devices.Create.Tags...)
		writeJSON(w, map[string]string{"key": "role-key"})
	}))
	defer fake.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: fake.Client(), ClientID: "one-id", ClientSecret: "one-secret", APIBase: fake.URL, OAuthBase: fake.URL}
	server := rpcServer(t, c, Principal{NodeID: "admin"})
	mgmt := funnelv1connect.NewManagementServiceClient(server.Client(), server.URL)
	if _, err := mgmt.SaveOAuth(t.Context(), connect.NewRequest(&v1.OAuthRequest{Purpose: "issuer", ClientId: "one-id", ClientSecret: "one-secret"})); err != nil {
		t.Fatal(err)
	}
	if _, err := mgmt.SaveOAuth(t.Context(), connect.NewRequest(&v1.OAuthRequest{Purpose: "issuer"})); err != nil {
		t.Fatal("blank credentials not preserved", err)
	}
	if got, err := store.GetSecret("tailscale_issuer_client_secret"); err != nil || got != "one-secret" {
		t.Fatal("shared credential not stored", err)
	}
	if _, err := store.GetSecret("tailscale_issuer_control_client_secret"); err != sql.ErrNoRows {
		t.Fatal("obsolete per-role credential created", err)
	}
	deploy := funnelv1connect.NewDeploymentServiceClient(server.Client(), server.URL)
	for _, mode := range []string{"docker-gateway", "tailnet-gateway", "loadbalancer"} {
		req := &v1.DeploymentRequest{Mode: mode, Target: "native", ResourceName: "fixture", Version: "dev-issuer"}
		if mode == "loadbalancer" {
			req.PublicHostname = "lb.example.com"
			req.CertificateEmail = "qa@example.com"
		}
		if _, err := deploy.Authorize(t.Context(), connect.NewRequest(req)); err != nil {
			t.Fatal(mode, err)
		}
	}
	if len(requested) != 3 {
		t.Fatal("not all native runtime roles issued", requested)
	}
	for i, role := range []string{"docker-gateway", "tailnet-gateway", "loadbalancer"} {
		if requested[i] != "tag:tailscale-private-funnel-main-"+role {
			t.Fatal("client chose issuer tag", requested)
		}
	}
}
