package store

import (
	"reflect"
	"testing"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

func TestDatabaseContractDockerRetirementAndReclamation(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "sqlite"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			if err := s.UpsertNode("docker", "docker-gateway", "docker"); err != nil {
				t.Fatal(err)
			}
			if err := bindTestNode(s, "docker", "device", nil); err != nil {
				t.Fatal(err)
			}
			makeApp := func(id, host, path string) ApplicationRecord {
				a := ApplicationRecord{ID: id, BackendSource: "docker", ServiceName: "svc:tailscale-private-funnel-app-" + id, ServiceFQDN: "100.70.0.1", BackendPort: 8080, ServicePort: 8080, AllowedGatewayNodes: []string{"docker"}, PublicHostnames: []string{host}, Enabled: true}
				if path != "" {
					a.HTTPRules = []proxy.HTTPRule{{Matches: []proxy.Match{{Path: path}}}}
				}
				if err := s.UpsertApplication(a); err != nil {
					t.Fatal(err)
				}
				return a
			}
			old := makeApp("old", "app.example.com", "")
			old.PublicHostnames = append(old.PublicHostnames, "alias.example.com")
			if err := s.UpsertApplication(old); err != nil {
				t.Fatal(err)
			}
			makeApp("unrelated", "other.example.com", "")
			makeApp("prefix", "shared.example.com", "/api")
			makeApp("catchall", "shared.example.com", "/")
			exposures, _ := s.Exposures()
			var before ExposureRecord
			for _, e := range exposures {
				if e.Hostname == "app.example.com" {
					before = e
				}
			}
			_ = s.SaveProvider("p", "cloudflare", map[string]string{"api_token": "test"}, true)
			if err := s.AddZone("p", "example.com"); err != nil {
				t.Fatal(err)
			}
			owner := "exposure:" + before.ID
			records := []libdns.Record{libdns.RR{Name: before.Hostname, Type: "A", Data: "203.0.113.5"}}
			if err := s.ReplaceLedger("p", "example.com", owner, records); err != nil {
				t.Fatal(err)
			}
			ledgerBefore, _ := s.Ledger("p", "example.com", owner)
			inv := DockerInventory{Revision: 1, Containers: []DockerContainer{{ID: "stopped", Running: false, Routes: []DockerDeclaration{{Hostname: "app.example.com", Port: 8080}}}, {ID: "other", Running: true, Routes: []DockerDeclaration{{Hostname: "other.example.com", Port: 8080}}}, {ID: "paths", Running: true, Routes: []DockerDeclaration{{Hostname: "shared.example.com", Port: 8080, PathPrefix: "/api"}, {Hostname: "shared.example.com", Port: 8080, PathPrefix: "/"}}}}}
			apply := func(nonce string, want int) {
				t.Helper()
				got, err := s.AcceptDockerInventory("docker", nonce, inv)
				if err != nil || got != want {
					t.Fatal(got, err)
				}
			}
			apply("first", 0) // Stopping is not removal; aliases remain attached.
			inv.Revision++
			inv.Containers[0].ID = "replacement"
			apply("replacement", 0)
			inv.Revision++
			inv.Containers = inv.Containers[1:]
			inv.Containers[1].Routes = inv.Containers[1].Routes[1:]
			apply("removed", 2)
			apps, _ := s.Applications()
			for _, a := range apps {
				switch a.ID {
				case "old", "prefix":
					if a.Enabled || len(a.PublicHostnames) > 0 {
						t.Fatal("not retired", a)
					}
				case "unrelated", "catchall":
					if !a.Enabled || len(a.PublicHostnames) == 0 {
						t.Fatal("unrelated assignment changed", a)
					}
				}
			}
			ledgerAfter, _ := s.Ledger("p", "example.com", owner)
			if !reflect.DeepEqual(ledgerBefore, ledgerAfter) {
				t.Fatal("retirement rewrote ledger")
			}
			if _, err := s.AcceptDockerInventory("docker", "out-of-order", DockerInventory{Revision: 1}); err == nil {
				t.Fatal("old inventory accepted")
			}
			inv.Revision++
			if _, err := s.AcceptDockerInventory("docker", "removed", inv); err == nil {
				t.Fatal("nonce replay accepted")
			}
			if err := s.UpsertApplication(old); err == nil {
				t.Fatal("absent Docker declaration recreated")
			}
			kube := old
			kube.ID = "k8s-new"
			kube.BackendSource = "kubernetes"
			kube.AllowedGatewayNodes = []string{"kube"}
			kube.PublicHostnames = []string{"app.example.com"}
			if err := s.UpsertApplication(kube); err != nil {
				t.Fatal("Kubernetes could not claim released hostname", err)
			}
			e, err := s.Exposure(before.ID)
			if err != nil || e.ApplicationID != kube.ID || !e.Active {
				t.Fatal("exposure identity not reused", e, err)
			}
			competitor := kube
			competitor.ID = "k8s-other"
			competitor.AllowedGatewayNodes = []string{"competitor"}
			if err := s.UpsertApplication(competitor); err == nil {
				t.Fatal("competing owner accepted")
			}
			ledgerAfter, _ = s.Ledger("p", "example.com", owner)
			if !reflect.DeepEqual(ledgerBefore, ledgerAfter) {
				t.Fatal("reclamation changed ownership")
			}
			n, _ := s.Node("docker")
			if n.Revoked || n.TailnetID != "device" {
				t.Fatal("shared identity changed", n)
			}
			retired, err := s.RetiredDockerExposure("docker", "old", "app.example.com", old.ServiceName, 8080)
			if err != nil || !retired {
				t.Fatal("retirement proof missing")
			}
		})
	}
}
