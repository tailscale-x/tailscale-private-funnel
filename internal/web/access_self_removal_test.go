package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestExplicitSelfRemovalRequiresVerifiedRemainingOwner(t *testing.T) {
	for _, tc := range []struct {
		name, status          string
		confirm, selector, ok bool
	}{
		{"confirmation required", "active", false, true, false},
		{"owner required", "active", true, false, false},
		{"suspended owner rejected", "suspended", true, true, false},
		{"verified owner retained", "active", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, api, _ := rpcFixture(t, Principal{NodeID: "admin", Tagged: true})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/oauth/token") {
					json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture"})
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"users": []tailscale.User{{Role: "owner", Type: "member", Status: tc.status, LoginName: "owner@example.com"}}})
			}))
			defer server.Close()
			c.TailscaleClient = &tailscale.Client{HTTP: server.Client(), ClientID: "id", ClientSecret: "secret", Tailnet: "test", APIBase: server.URL, OAuthBase: server.URL}
			c.Store.SetJSON("tailnet", map[string]string{"name": "test"})
			c.Store.SetSecret("tailscale_users_client_id", "fixture")
			c.Store.SetSecret("tailscale_users_client_secret", "fixture")
			selectors := []string{"node:someone-else"}
			if tc.selector {
				selectors = []string{"autogroup:owner"}
			}
			_, e := api.SaveAccess(t.Context(), connect.NewRequest(&v1.AccessSettings{Roles: map[string]*v1.Selectors{"admin": {Selectors: selectors}}, ConfirmSelfRemoval: tc.confirm}))
			if (e == nil) != tc.ok {
				t.Fatalf("save error=%v", e)
			}
			saved, _ := c.loadAccessSettings()
			if tc.ok {
				if saved.permissions(Principal{NodeID: "admin", Tagged: true}).Admin {
					t.Fatal("QA access retained")
				}
				if !saved.permissions(Principal{TailnetRole: "owner"}).Admin {
					t.Fatal("Owner lost access")
				}
			} else if !saved.permissions(Principal{NodeID: "admin", Tagged: true}).Admin {
				t.Fatal("failed save modified roles")
			}
		})
	}
}
