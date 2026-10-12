package store

import (
	"github.com/libdns/libdns"
	"testing"
	"time"
)

func TestFinalizeDirectMigrationPreservesOwnedDNS(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "test"}, true); err != nil {
		t.Fatal(err)
	}
	if err = s.AddZone("provider", "example.com"); err != nil {
		t.Fatal(err)
	}
	record := libdns.RR{Name: "voice.example.com", Type: "CNAME", Data: "edge.example.com", TTL: time.Minute}
	if err = s.ReplaceLedger("provider", "example.com", "loadbalancer:edge", []libdns.Record{record}); err != nil {
		t.Fatal(err)
	}
	app := ApplicationRecord{ID: "voice", Name: "Voice", ServiceName: "svc:old-voice", BackendSource: "tailnet", BackendDeviceID: "peer", BackendPort: 8766, PublicHostnames: []string{"voice.example.com"}, Enabled: true}
	if err = s.UpsertApplication(app); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO service_hosts(service_name,device_id,approved,approval_state,updated_at) VALUES('svc:old-voice','old-ingress',1,'approved',1)`,
		`INSERT INTO backend_assignments(id,application_id,node_id,container_id,backend_ip,backend_port) VALUES('old','voice','old-ingress','','100.70.0.1',8766)`,
		`INSERT INTO settings(name,value,secret) VALUES('direct_previous:voice','{}',0)`,
		`DELETE FROM schema_migrations WHERE version='011_finalize_direct_routes.sql'`,
	} {
		if _, err = s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	apps, err := s.Applications()
	if err != nil || len(apps) != 1 || apps[0].ServiceName != "direct:voice" || apps[0].ServicePort != 0 {
		t.Fatalf("application: %+v %v", apps, err)
	}
	for _, table := range []string{"service_hosts", "backend_assignments"} {
		var count int
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("stale %s: %d %v", table, count, err)
		}
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM settings WHERE name='direct_previous:voice'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cutover state: %d %v", count, err)
	}
	ledger, err := s.Ledger("provider", "example.com", "loadbalancer:edge")
	if err != nil || len(ledger) != 1 || ledger[0].Record.RR().Data != "edge.example.com" {
		t.Fatalf("ledger: %+v %v", ledger, err)
	}
}
