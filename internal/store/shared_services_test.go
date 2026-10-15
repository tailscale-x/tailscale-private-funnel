package store

import (
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"reflect"
	"strings"
	"testing"
)

func TestSharedServiceMigrationPreservesApplicationsAndOwnership(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	app := ApplicationRecord{ID: "first", Name: "First", ServiceName: "svc:shared", ServiceFQDN: "100.70.0.1", ServicePort: 8080, BackendPort: 8080, BackendSource: "kubernetes", AllowedGatewayNodes: []string{"node"}, PublicHostnames: []string{"first.example.com"}, HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/"}}}}, Enabled: true}
	if err = s.UpsertApplication(app); err != nil {
		t.Fatal(err)
	}
	if err = s.SetSecret("credential", "protected"); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "test"}, true); err != nil {
		t.Fatal(err)
	}
	if err = s.AddZone("provider", "example.com"); err != nil {
		t.Fatal(err)
	}
	err = s.ReplaceLedger("provider", "example.com", "exposure:known", []libdns.Record{libdns.RR{Name: "first.example.com", Type: "CNAME", Data: "edge.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.Applications()
	exposures, _ := s.Exposures()
	// Recreate the former UNIQUE constraint, then apply the actual startup migration.
	var schema string
	if err = s.db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='applications'`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	schema = strings.Replace(schema, "service_name TEXT NOT NULL", "service_name TEXT NOT NULL UNIQUE", 1)
	schema = strings.Replace(schema, `CREATE TABLE "applications"`, `CREATE TABLE applications_previous`, 1)
	schema = strings.Replace(schema, `CREATE TABLE applications(`, `CREATE TABLE applications_previous(`, 1)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{schema, `INSERT INTO applications_previous SELECT * FROM applications`, `DROP TABLE applications`, `ALTER TABLE applications_previous RENAME TO applications`, `CREATE INDEX applications_host_idx ON applications(service_name)`, `DELETE FROM schema_migrations WHERE version='015_shared_application_services'`} {
		if _, err = tx.Exec(q); err != nil {
			tx.Rollback()
			t.Fatal(err, q)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after, _ := s.Applications()
	afterExposures, _ := s.Exposures()
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(exposures, afterExposures) {
		t.Fatal("application or exposure identity changed")
	}
	secret, _ := s.GetSecret("credential")
	ledger, _ := s.Ledger("provider", "example.com", "exposure:known")
	if secret != "protected" || len(ledger) != 1 {
		t.Fatal("credentials or ownership changed")
	}
	app.ID = "second"
	app.PublicHostnames = []string{"second.example.com"}
	if err = s.UpsertApplication(app); err != nil {
		t.Fatal("shared backend Service rejected", err)
	}
	apps, _ := s.Applications()
	if len(apps) != 2 || apps[0].ServiceName != apps[1].ServiceName {
		t.Fatal(apps)
	}
}
