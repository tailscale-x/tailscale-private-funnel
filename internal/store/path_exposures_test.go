package store

import (
	"testing"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

func TestDatabaseContractSharedPathOwnership(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "sqlite"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			if err := s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "test"}, true); err != nil {
				t.Fatal(err)
			}
			if err := s.AddZone("provider", "example.com"); err != nil {
				t.Fatal(err)
			}
			app := func(id, path string) ApplicationRecord {
				return ApplicationRecord{ID: id, ServiceName: "svc:" + id, ServicePort: 8080, BackendPort: 8080, BackendSource: "docker", PublicHostnames: []string{"app.example.com"}, AllowedGatewayNodes: []string{"docker"}, Enabled: true, HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: path}}}}}
			}
			a, b := app("a", "/api"), app("b", "/admin")
			if err := s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			before, _ := s.Exposures()
			if len(before) != 1 {
				t.Fatal(before)
			}
			id := before[0].ID
			if err := s.ReplaceLedger("provider", "example.com", "exposure:"+id, []libdns.Record{libdns.RR{Name: "app.example.com", Type: "A", Data: "203.0.113.1"}}); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertApplication(b); err != nil {
				t.Fatal(err)
			}
			a.Enabled = false
			if err := s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			after, _ := s.Exposures()
			if len(after) != 1 || after[0].ID != id || !after[0].Active {
				t.Fatal("shared hostname lost ownership/activity", after)
			}
			ledger, err := s.Ledger("provider", "example.com", "exposure:"+id)
			if err != nil || len(ledger) != 1 {
				t.Fatal("ownership ledger changed", ledger, err)
			}
			foreign := app("foreign", "/other")
			foreign.AllowedGatewayNodes = []string{"another-gateway"}
			if err := s.UpsertApplication(foreign); err == nil {
				t.Fatal("foreign gateway share accepted")
			}
			a.HTTPRules = b.HTTPRules
			if err := s.UpsertApplication(a); err == nil {
				t.Fatal("duplicate path update accepted")
			}
			apps, _ := s.Applications()
			for _, stored := range apps {
				if stored.ID == "a" && stored.HTTPRules[0].Matches[0].Path != "/api" {
					t.Fatal("failed update wasn't rolled back")
				}
			}
			b.Enabled = false
			if err := s.UpsertApplication(b); err != nil {
				t.Fatal(err)
			}
			a.PublicHostnames = nil
			b.PublicHostnames = nil
			a.HTTPRules = nil
			if err := s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertApplication(b); err != nil {
				t.Fatal(err)
			}
			after, _ = s.Exposures()
			if after[0].Active || after[0].ID != id {
				t.Fatal("empty hostname cleanup state", after)
			}
		})
	}
}
