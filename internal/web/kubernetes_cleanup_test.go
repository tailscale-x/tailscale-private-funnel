package web

import (
	"connectrpc.com/connect"
	"encoding/json"
	"github.com/libdns/libdns"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRevokedKubernetesGatewayReleasesOnlyOwnedClaimsAfterCleanup(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]string{"name": "test"})
	_ = s.SetSecret("tailscale_services_client_id", "fixture")
	_ = s.SetSecret("tailscale_services_client_secret", "fixture")
	fail := true
	foreignHost := false
	deleted := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/devices") {
			hosts := []tailscale.ServiceHost{}
			if foreignHost {
				hosts = append(hosts, tailscale.ServiceHost{NodeID: "foreign-device"})
			}
			json.NewEncoder(w).Encode(map[string]any{"hosts": hosts})
			return
		}
		if r.Method == "DELETE" {
			deleted = append(deleted, r.URL.Path)
			if fail {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: server.Client(), ClientID: "fixture", ClientSecret: "fixture", Tailnet: "test", APIBase: server.URL, OAuthBase: server.URL}
	_ = s.UpsertNode("removed", "k8s-gateway", "removed")
	_ = s.UpsertNode("other", "k8s-gateway", "other")
	own := store.ApplicationRecord{ID: "old-app", BackendSource: "kubernetes", Enabled: true, PublicHostnames: []string{"voice.example.com"}, AllowedGatewayNodes: []string{"removed"}, HTTPBackends: []proxy.Backend{{ServiceName: "svc:tpf-k8s-owned"}}}
	shared := store.ApplicationRecord{ID: "shared", BackendSource: "kubernetes", Enabled: true, PublicHostnames: []string{"shared.example.com"}, AllowedGatewayNodes: []string{"removed", "other"}, HTTPBackends: []proxy.Backend{{ServiceName: "svc:tpf-k8s-shared"}}}
	_ = s.UpsertApplication(own)
	_ = s.UpsertApplication(shared)
	if e := c.retireKubernetesGateway(t.Context(), store.NodeRecord{ID: "removed", Mode: "k8s-gateway"}); e == nil {
		t.Fatal("active gateway cleanup accepted")
	}
	api := managementClient(t, c, Principal{NodeID: "admin"})
	r, e := api.DisableNode(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "removed"}))
	if e != nil || r.Msg.Ok {
		t.Fatal(r, e)
	}
	apps, _ := s.Applications()
	for _, a := range apps {
		if a.ID == own.ID && (a.Enabled || len(a.PublicHostnames) != 1) {
			t.Fatal("failed cleanup released ownership", a)
		}
	}
	foreignHost = true
	fail = false
	r, e = api.CleanupNode(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "removed"}))
	if e != nil || r.Msg.Ok {
		t.Fatal("foreign Service host was not protected", r, e)
	}
	foreignHost = false
	r, e = api.CleanupNode(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "removed"}))
	if e != nil || !r.Msg.Ok {
		t.Fatal(r, e)
	}
	deletedBeforeRetry := len(deleted)
	for i := 0; i < 2; i++ {
		r, e = api.CleanupNode(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "removed"}))
		if e != nil || !r.Msg.Ok {
			t.Fatal("cleanup retry failed", r, e)
		}
	}
	if len(deleted) != deletedBeforeRetry {
		t.Fatal("completed cleanup repeated external deletion", deleted)
	}
	apps, _ = s.Applications()
	for _, a := range apps {
		if a.ID == own.ID && (a.Enabled || len(a.PublicHostnames) != 0) {
			t.Fatal("claim not released", a)
		}
		if a.ID == shared.ID && (!a.Enabled || len(a.PublicHostnames) != 1) {
			t.Fatal("shared app changed", a)
		}
	}
	var history map[string]any
	if e := s.GetJSON("kubernetes_retirement:"+own.ID, &history); e != nil {
		t.Fatal("lost retirement history", e)
	}
	for _, path := range deleted {
		if strings.Contains(path, "shared") {
			t.Fatal("shared Service deleted", deleted)
		}
	}
	viewer := managementClient(t, c, Principal{NodeID: "viewer"})
	if _, e := viewer.CleanupNode(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "removed"})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("viewer authorized cleanup", e)
	}
}

func TestKubernetesCleanupDoesNotVisitUnrelatedZones(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com", "example.net")
	_ = s.UpsertNode("removed", "k8s-gateway", "removed")
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "old", BackendSource: "kubernetes", AllowedGatewayNodes: []string{"removed"}, PublicHostnames: []string{"old.example.com"}})
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "unrelated", BackendSource: "kubernetes", AllowedGatewayNodes: []string{"other"}, PublicHostnames: []string{"keep.example.net"}, Enabled: true})
	es, _ := s.Exposures()
	owner := ""
	for _, e := range es {
		if e.ApplicationID == "old" {
			owner = "exposure:" + e.ID
		}
	}
	if owner == "" {
		t.Fatal("missing owned exposure")
	}
	owned := libdns.RR{Name: "old.example.com", Type: "A", Data: "192.0.2.1"}
	unmanaged := libdns.RR{Name: "unmanaged.example.com", Type: "A", Data: "192.0.2.2"}
	f.records["example.com"] = []libdns.Record{owned, unmanaged}
	if e := s.ReplaceLedger("fixture", "example.com", owner, []libdns.Record{owned}); e != nil {
		t.Fatal(e)
	}
	f.fail = "example.net"
	_ = s.RevokeNode("removed")
	n, _ := s.Node("removed")
	if e := c.cleanupRevokedNode(t.Context(), n); e != nil {
		t.Fatal("unrelated zone blocked cleanup", e)
	}
	if len(f.records["example.com"]) != 1 || f.records["example.com"][0].RR().Name != unmanaged.Name {
		t.Fatal("unmanaged record changed", f.records)
	}
	apps, _ := s.Applications()
	for _, a := range apps {
		if a.ID == "unrelated" && (!a.Enabled || len(a.PublicHostnames) != 1) {
			t.Fatal("unrelated application changed")
		}
	}
}
