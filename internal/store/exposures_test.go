package store

import (
	"testing"
)

func TestExposureMigrationRollsBackDuplicateHostnames(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertApplication(ApplicationRecord{ID: "a", ServiceName: "svc:a", PublicHostnames: []string{"app.example.com"}}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DROP TABLE exposures`, `ALTER TABLE applications ADD COLUMN loadbalancer_id TEXT NOT NULL DEFAULT ''`, `DELETE FROM schema_migrations WHERE version='013_exposure_mapping'`, `INSERT INTO applications(id,service_name,service_fqdn,service_port,backend_port,public_hostnames,allowed_gateway_nodes,policy_revision,enabled,created_at,updated_at) VALUES('b','svc:b','',80,80,'["app.example.com"]','[]',0,1,0,0)`} {
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
