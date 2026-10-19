package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func encodePublicKey(key []byte) string {
	return base64.RawStdEncoding.EncodeToString(key)
}

func registrationTestControl(t *testing.T, s *store.Store) *Control {
	installTestEdge(t, s)
	t.Helper()
	for key, value := range map[string]string{"tailscale_services_client_id": "test-id", "tailscale_services_client_secret": "test-secret"} {
		if err := s.SetSecret(key, value); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/keys") {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			create := payload["capabilities"].(map[string]any)["devices"].(map[string]any)["create"].(map[string]any)
			if create["ephemeral"] != false || create["reusable"] != false || create["preauthorized"] != true || payload["expirySeconds"] != float64(900) {
				t.Errorf("invalid bootstrap key settings: %v", payload)
			}
			json.NewEncoder(w).Encode(map[string]string{"key": "tskey-bootstrap-test"})
			return
		}
		if r.Method == "PUT" && strings.Contains(r.URL.Path, "/services/") {
			var service tailscale.Service
			_ = json.NewDecoder(r.Body).Decode(&service)
			service.Addresses = []string{"100.70.1.1"}
			writeJSON(w, service)
			return
		}
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/devices") {
			writeJSON(w, map[string]any{"hosts": []any{}})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return &Control{Store: s, ReleaseCheck: func(context.Context, string) error { return nil }, TailscaleClient: &tailscale.Client{HTTP: server.Client(), ClientID: "test-id", ClientSecret: "test-secret", Tailnet: "example.ts.net", APIBase: server.URL + "/api/v2", OAuthBase: server.URL}}
}
