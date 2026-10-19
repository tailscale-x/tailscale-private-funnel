package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestTailnetDeletionRetainsOwnershipUntilServiceCleanupSucceeds(t *testing.T) {
	c, s := testControl(t)
	s.SetJSON("tailnet", map[string]string{"name": "test"})
	s.SetSecret("tailscale_services_client_id", "fixture")
	s.SetSecret("tailscale_services_client_secret", "fixture")
	fail := true
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture"})
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
		json.NewEncoder(w).Encode(map[string]any{"hosts": []any{}})
	}))
	defer server.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: server.Client(), ClientID: "fixture", ClientSecret: "fixture", Tailnet: "test", APIBase: server.URL, OAuthBase: server.URL}
	app := store.ApplicationRecord{ID: "app", Name: "App", BackendSource: "tailnet", ServiceName: "svc:tailscale-private-funnel-app-app", Enabled: true, PolicyRevision: 7}
	if e := s.UpsertApplication(app); e != nil {
		t.Fatal(e)
	}
	api := managementClient(t, c, Principal{NodeID: "admin"})
	req := &v1.ResourceRevision{Id: "app", Revision: 7}
	result, e := api.DeleteTailnetExposure(t.Context(), connect.NewRequest(req))
	if e != nil || result.Msg.Ok || deletes != 1 {
		t.Fatal(result, e, deletes)
	}
	apps, _ := s.Applications()
	if len(apps) != 1 || apps[0].Enabled {
		t.Fatal("lost cleanup ownership or kept exposure enabled", apps)
	}
	fail = false
	result, e = api.DeleteTailnetExposure(t.Context(), connect.NewRequest(req))
	if e != nil || !result.Msg.Ok || deletes != 2 {
		t.Fatal(result, e, deletes)
	}
	apps, _ = s.Applications()
	if len(apps) != 0 {
		t.Fatal("cleaned application retained", apps)
	}
}

func TestSharedAndForeignApplicationServicesAreNotDeleted(t *testing.T) {
	c, _ := testControl(t)
	app := store.ApplicationRecord{ID: "app", ServiceName: "svc:tailscale-private-funnel-app-app"}
	other := store.ApplicationRecord{ID: "other", ServiceName: app.ServiceName}
	if e := c.cleanupTailnetApplicationService(context.Background(), app, []store.ApplicationRecord{app, other}); e != nil {
		t.Fatal("shared Service should be retained", e)
	}
	app.ServiceName = "svc:someone-else"
	if e := c.cleanupTailnetApplicationService(context.Background(), app, nil); e == nil {
		t.Fatal("foreign Service accepted")
	}
}
