package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestProviderDisplayNamesAndImmutableReferences(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	create := func() store.ProviderRecord {
		w := requestManagement(c, "POST", "/api/providers", map[string]any{"name": "  家庭 DNS  ", "provider": "fixture", "config": map[string]string{"api_token": "private"}}, "owner")
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		var p store.ProviderRecord
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.Name != "家庭 DNS" || p.ID == "家庭 DNS" || p.ID == "" || strings.Contains(w.Body.String(), "private") {
			t.Fatal(p)
		}
		return p
	}
	first, second := create(), create()
	if first.ID == second.ID {
		t.Fatal("duplicate names shared ID")
	}
	w := requestManagement(c, "PATCH", "/api/providers/fixture", map[string]any{"name": "New alias"}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, cfg, _ := s.ProviderConfig("fixture")
	if p.Name != "New alias" || cfg["api_token"] != "secret" {
		t.Fatal(p, cfg)
	}
	zones, _ := s.Zones()
	if len(zones) != 1 || zones[0].ProviderID != "fixture" {
		t.Fatal("rename changed assignments")
	}
	if w = requestManagement(c, "PATCH", "/api/providers/not-real", map[string]any{"name": "New alias"}, "owner"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = requestManagement(c, "POST", "/api/providers", map[string]any{"id": "chosen", "name": "Bad", "provider": "fixture", "config": map[string]string{"api_token": "private"}}, "owner"); w.Code != 400 {
		t.Fatal("client ID accepted", w.Code)
	}
	if w = requestManagement(c, "PATCH", "/api/providers/fixture", map[string]any{"id": "replacement", "name": "Bad"}, "owner"); w.Code != 400 {
		t.Fatal("immutable ID changed", w.Code)
	}
	for _, name := range []string{" ", strings.Repeat("é", 129)} {
		if w = requestManagement(c, "POST", "/api/providers", map[string]any{"name": name, "provider": "fixture", "config": map[string]string{"api_token": "private"}}, "owner"); w.Code != 400 {
			t.Fatal("invalid name accepted", w.Code)
		}
	}
}
func TestResourceRenamesPreserveEnrollmentAndService(t *testing.T) {
	c, s := testControl(t)
	e, code, err := s.CreateEnrollment("loadbalancer", "actual-host")
	if err != nil {
		t.Fatal(err)
	}
	w := requestManagement(c, "PATCH", "/api/enrollments/"+e.ID+"/name", map[string]any{"name": "Friendly ingress"}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err = s.BindEnrollment(e.ID, code, "signing-key", "tailnet-device", "", nil); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Node(e.ID)
	if n.Name != "Friendly ingress" || n.Hostname != "actual-host" {
		t.Fatal(n)
	}
	app := store.ApplicationRecord{ID: "app-id", Name: "First name", ServiceName: "svc:actual-service", ServiceFQDN: "actual.example.ts.net", BackendPort: 8080, ServicePort: 8080, Enabled: true}
	_ = s.UpsertApplication(app)
	w = requestManagement(c, "PATCH", "/api/applications/app-id/name", map[string]any{"name": "Another name"}, "owner")
	if w.Code != 404 && w.Code != 405 {
		t.Fatal(w.Code, w.Body.String())
	}
	apps, _ := s.Applications()
	if len(apps) != 1 || apps[0].ID != app.ID || apps[0].ServiceName != app.ServiceName || apps[0].Name != app.ServiceName {
		t.Fatal(apps)
	}
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"viewer": {"node:admin"}}})
	if w = requestManagement(c, "PATCH", "/api/nodes/"+e.ID+"/name", map[string]any{"name": "Denied"}, "viewer"); w.Code != 403 {
		t.Fatal("viewer renamed node", w.Code)
	}
}
func TestZoneReferencesUseAssignmentIDs(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	id := testZoneID(t, s, "example.com")
	w := requestManagement(c, "POST", "/api/zones/"+id+"/test", nil, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = requestManagement(c, "DELETE", "/api/zones/"+id, nil, "owner"); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = requestManagement(c, "POST", "/api/zones/"+id+"/test", nil, "owner"); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
