package store

import (
	"github.com/libdns/libdns"
	"os"
	"strings"
	"testing"
)

func TestExposureIdentityAndAtomicApplicationUpdate(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := ApplicationRecord{ID: "app", ServiceName: "svc:app", PublicHostnames: []string{"one.example.com", "two.example.com"}, Enabled: true}
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Exposures()
	if len(es) != 2 || es[0].ID == es[1].ID {
		t.Fatal(es)
	}
	first := es[0]
	_ = s.SetExposureLoadBalancers(first.ID, []string{"edge"})
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Exposure(first.ID)
	if after.ID != first.ID || len(after.LoadBalancerIDs) != 1 {
		t.Fatal(after)
	}
	conflict := ApplicationRecord{ID: "other", ServiceName: "svc:other", PublicHostnames: []string{first.Hostname}}
	if err = s.UpsertApplication(conflict); err == nil {
		t.Fatal("duplicate hostname accepted")
	}
	apps, _ := s.Applications()
	if len(apps) != 1 {
		t.Fatal("application update not atomic", apps)
	}
	a.PublicHostnames = []string{es[1].Hostname}
	_ = s.UpsertApplication(a)
	after, _ = s.Exposure(first.ID)
	if after.Active {
		t.Fatal("removed alias active")
	}
}
func TestExposureMigrationPreservesLedgerAndCredentials(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "secret"}, true)
	_ = s.AddZone("provider", "example.com")
	_ = s.UpsertApplication(ApplicationRecord{ID: "app", ServiceName: "svc:app", PublicHostnames: []string{"app.example.com"}, Enabled: true})
	prior := libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}
	_ = s.ReplaceLedger("provider", "example.com", "ingress:node", []libdns.Record{prior})
	_ = s.ReplaceLedger("provider", "example.com", "loadbalancer:edge", []libdns.Record{libdns.RR{Name: "edge.example.com", Type: "A", Data: "203.0.113.5"}})
	_ = s.SetJSON("node_deployment:node", map[string]any{"loadbalancer_id": "edge", "label_keys": "custom.expose"})
	for _, q := range []string{`DROP TABLE exposures`, `ALTER TABLE applications ADD COLUMN loadbalancer_id TEXT NOT NULL DEFAULT ''`, `DELETE FROM schema_migrations WHERE version='013_exposure_mapping'`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	es, _ := s.Exposures()
	if len(es) != 1 || es[0].LoadBalancerIDs != nil {
		t.Fatal(es)
	}
	ledger, _ := s.Ledger("provider", "example.com", "exposure:"+es[0].ID)
	if len(ledger) != 1 || ledger[0].Record.RR().Data != prior.Data {
		t.Fatal(ledger)
	}
	edge, _ := s.Ledger("provider", "example.com", "loadbalancer:edge")
	if len(edge) != 1 {
		t.Fatal(edge)
	}
	_, credentials, err := s.ProviderConfig("provider")
	if err != nil || credentials["api_token"] != "secret" {
		t.Fatal("credential changed")
	}
	var settings map[string]any
	_ = s.GetJSON("node_deployment:node", &settings)
	if _, ok := settings["loadbalancer_id"]; ok || settings["label_keys"] != "custom.expose" {
		t.Fatal(settings)
	}
}
func TestExposureMigrationRollsBackDuplicateHostnames(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertApplication(ApplicationRecord{ID: "a", ServiceName: "svc:a", PublicHostnames: []string{"app.example.com"}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE exposures`, `ALTER TABLE applications ADD COLUMN loadbalancer_id TEXT NOT NULL DEFAULT ''`, `DELETE FROM schema_migrations WHERE version='013_exposure_mapping'`, `INSERT INTO applications(id,service_name,service_fqdn,service_port,backend_port,public_hostnames,allowed_ingress_nodes,policy_revision,enabled,created_at,updated_at) VALUES('b','svc:b','',80,80,'["app.example.com"]','[]',0,1,0,0)`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.migrateExposures(); err == nil {
		t.Fatal("ambiguous host migration accepted")
	}
	var n int
	_ = s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='exposures'`).Scan(&n)
	if n != 0 {
		t.Fatal("failed migration left tables")
	}
	s.Close()
}

// Opt-in migration rehearsal against a private, disposable copy of deployed data.
func TestLiveExposureMigration(t *testing.T) {
	dir := os.Getenv("PF_MIGRATION_DATA")
	if dir == "" {
		t.Skip("disposable control-data copy required")
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	es, err := s.Exposures()
	if err != nil {
		t.Fatal(err)
	}
	parts, err := s.LedgerPartitions()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if strings.HasPrefix(p.OwnerID, "ingress:") {
			t.Fatal("unmigrated exposure owner", p.OwnerID)
		}
		if strings.HasPrefix(p.OwnerID, "exposure:") {
			if _, err = s.Exposure(strings.TrimPrefix(p.OwnerID, "exposure:")); err != nil {
				t.Fatal("missing exposure owner")
			}
		}
	}
	for _, e := range es {
		if e.LoadBalancerIDs != nil {
			t.Fatal("unexpected initial override")
		}
	}
	providers, err := s.Providers()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range providers {
		if _, credentials, err := s.ProviderConfig(p.ID); err != nil || len(credentials) == 0 {
			t.Fatal("provider credentials unavailable")
		}
	}
	t.Logf("Migration passed: %d exposures, %d ownership partitions, %d providers", len(es), len(parts), len(providers))
}

func TestDeletedExposureHostnameCanBeProvisionedAgainAfterCleanup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := ApplicationRecord{ID: "old", BackendSource: "tailnet", ServiceName: "direct:old", PublicHostnames: []string{"voice.example.com"}}
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Exposures()
	if err = s.DeleteApplication(a.ID); err != nil {
		t.Fatal(err)
	}
	a.ID = "new"
	a.ServiceName = "direct:new"
	if err = s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Exposures()
	if len(after) != 1 || after[0].ID == before[0].ID || after[0].LoadBalancerIDs != nil {
		t.Fatal(after)
	}
}
