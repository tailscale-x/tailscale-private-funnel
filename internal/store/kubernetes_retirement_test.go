package store

import (
	"github.com/libdns/libdns"
	"testing"
)

func TestKubernetesRetirementContract(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "sqlite"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			if e := s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "fixture"}, true); e != nil {
				t.Fatal(e)
			}
			if e := s.AddZone("provider", "example.com"); e != nil {
				t.Fatal(e)
			}
			_ = s.UpsertNode("gateway", "k8s-gateway", "gateway")
			apps := []ApplicationRecord{{ID: "one", BackendSource: "kubernetes", AllowedGatewayNodes: []string{"gateway"}, PublicHostnames: []string{"one.example.com"}}, {ID: "two", BackendSource: "kubernetes", AllowedGatewayNodes: []string{"gateway"}, PublicHostnames: []string{"two.example.com"}}}
			for _, a := range apps {
				if e := s.UpsertApplication(a); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.RetireKubernetesApplications("gateway", apps); e == nil {
				t.Fatal("unrevoked gateway retired")
			}
			_ = s.RevokeNode("gateway")
			exposures, e := s.Exposures()
			if e != nil {
				t.Fatal(e)
			}
			owner := ""
			for _, e := range exposures {
				if e.ApplicationID == "two" {
					owner = "exposure:" + e.ID
				}
			}
			if owner == "" {
				t.Fatal("missing exposure")
			}
			if e := s.ReplaceLedger("provider", "example.com", owner, []libdns.Record{libdns.RR{Name: "two.example.com", Type: "A", Data: "192.0.2.1"}}); e != nil {
				t.Fatal(e)
			}
			if e := s.RetireKubernetesApplications("gateway", apps); e == nil {
				t.Fatal("released claim while owned DNS remains")
			}
			got, _ := s.Applications()
			for _, a := range got {
				if len(a.PublicHostnames) != 1 {
					t.Fatal("partial retirement committed", got)
				}
			}
			var history any
			if e := s.GetJSON("kubernetes_retirement:one", &history); e == nil {
				t.Fatal("history committed on rollback")
			}
			_ = s.ReplaceLedger("provider", "example.com", owner, nil)
			if e := s.RetireKubernetesApplications("gateway", apps); e != nil {
				t.Fatal(e)
			}
			got, _ = s.Applications()
			for _, a := range got {
				if len(a.PublicHostnames) != 0 || a.Enabled {
					t.Fatal("retirement failed", a)
				}
			}
			if e := s.GetJSON("kubernetes_retirement:one", &history); e != nil {
				t.Fatal(e)
			}
			if e := s.RetireKubernetesApplications("gateway", got); e != nil {
				t.Fatal("not idempotent", e)
			}
			after, _ := s.Exposures()
			if len(after) != len(exposures) {
				t.Fatal("lost exposure identity")
			}
		})
	}
}
