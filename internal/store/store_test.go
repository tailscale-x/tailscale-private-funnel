package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func TestProviderConfigIsEncryptedAndRoundTrips(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SaveProvider("dev", "cloudflare", map[string]string{"api_token": "secret"}, true); err != nil {
		t.Fatal(err)
	}
	p, config, err := s.ProviderConfig("dev")
	if err != nil {
		t.Fatal(err)
	}
	if p.Provider != "cloudflare" || config["api_token"] != "secret" {
		t.Fatalf("provider=%+v config=%v", p, config)
	}
	_ = s.AddZone("dev", "example.com")
	if err := s.ReplaceLedger("dev", "example.com", "owner", []libdns.Record{libdns.RR{Name: "app", Type: "A", Data: "100.64.0.2", TTL: time.Minute}}); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Ledger("dev", "example.com", "owner")
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}

func TestAcceptReportRequiresEnrolledSigningKey(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub, priv, err := report.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	e, err := report.NewEnvelope("gateway-1", map[string]any{"exposures": []any{}}, pub, priv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(e)
	if err := s.AcceptReport(e.NodeID, e.Nonce, b, e.PublicKey); err == nil {
		t.Fatal("unknown node report was accepted")
	}
	if err := s.UpsertNode("gateway-1", "docker-gateway", "gateway-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindNodeKey("gateway-1", e.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptReport(e.NodeID, e.Nonce, b, e.PublicKey); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptReport(e.NodeID, e.Nonce, b, e.PublicKey); err == nil {
		t.Fatal("replayed report was accepted")
	}
}

func TestEnrollmentIsReusableUntilRevoked(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, code, err := s.CreateEnrollment("docker-gateway", "gateway-node")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemEnrollment(e.ID, code); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemEnrollment(e.ID, code); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeEnrollment(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemEnrollment(e.ID, code); err == nil {
		t.Fatal("revoked enrollment was accepted")
	}
}

func TestBackendSnapshotsReconcileAndExpire(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpsertBackend(BackendAssignment{ID: "backend-a", NodeID: "gateway-1", ApplicationID: "app", BackendPort: 8080}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertBackend(BackendAssignment{ID: "backend-b", NodeID: "gateway-1", ApplicationID: "app", BackendPort: 8081}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileBackends("gateway-1", map[string]bool{"backend-a": true}); err != nil {
		t.Fatal(err)
	}
	var revoked int
	if err := s.db.QueryRow(`SELECT revoked FROM backend_assignments WHERE id=?`, "backend-b").Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("stale backend revoked=%d err=%v", revoked, err)
	}
	if err := s.RevokeBackends("gateway-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT revoked FROM backend_assignments WHERE id=?`, "backend-a").Scan(&revoked); err != nil || revoked != 1 {
		t.Fatalf("active backend not revoked on expiry: revoked=%d err=%v", revoked, err)
	}
}

func TestProviderZoneMigrationPreservesCredentialsAndLedger(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveProvider("old", "cloudflare", map[string]string{"api_token": "preserved", "account_id": "account"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`ALTER TABLE providers ADD COLUMN zone TEXT NOT NULL DEFAULT ''; UPDATE providers SET zone='example.com' WHERE id='old'`); err != nil {
		t.Fatal(err)
	}
	_ = s.AddZone("old", "example.com")
	_ = s.ReplaceLedger("old", "example.com", "gateway:old", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}})
	_ = s.SaveReport("old", []byte(`{"preserved":true}`))
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p, cfg, err := s.ProviderConfig("old")
	if err != nil || cfg["api_token"] != "preserved" || p.AccountID != "account" {
		t.Fatal(p, cfg, err)
	}
	zones, _ := s.Zones()
	if len(zones) != 1 || zones[0].Zone != "example.com" {
		t.Fatal(zones)
	}
	ledger, _ := s.Ledger("old", "example.com", "gateway:old")
	if len(ledger) != 1 {
		t.Fatal(ledger)
	}
	reports, _ := s.Reports()
	if reports["old"] == "" {
		t.Fatal("report lost")
	}
	if err = s.SaveProvider("second", "cloudflare", map[string]string{"api_token": "new"}, true); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveZoneOwnershipColumnPreservesLedger(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SaveProvider("cf", "cloudflare", map[string]string{"api_token": "secret"}, true)
	_ = s.AddZone("cf", "example.com")
	_ = s.ReplaceLedger("cf", "example.com", "gateway:original", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}})
	if _, err = s.db.Exec(`ALTER TABLE managed_zones ADD COLUMN owner_id TEXT NOT NULL DEFAULT 'historical-owner'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err = s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('managed_zones') WHERE name='owner_id'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	ledger, _ := s.Ledger("cf", "example.com", "gateway:original")
	if len(ledger) != 1 {
		t.Fatal(ledger)
	}
	_, cfg, _ := s.ProviderConfig("cf")
	if cfg["api_token"] != "secret" {
		t.Fatal("credential lost")
	}
	zones, _ := s.Zones()
	if len(zones) != 1 || zones[0].Zone != "example.com" {
		t.Fatal(zones)
	}
}

func TestConcurrentSettingsAndAuditWrites(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	failures := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				if err := s.SetJSON("roles", map[string]string{"admin": "configured"}); err != nil {
					failures <- err
					return
				}
				var roles map[string]string
				if err := s.GetJSON("roles", &roles); err != nil {
					failures <- err
					return
				}
				if err := s.Audit("concurrent", "test"); err != nil {
					failures <- err
					return
				}
			}
			failures <- nil
		}()
	}
	for i := 0; i < 32; i++ {
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
	}
	events, err := s.AuditEvents(500)
	if err != nil || len(events) != 320 {
		t.Fatal("lost writes", len(events), err)
	}
}

func TestZoneMigrationRefusesDisabledAssignments(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SaveProvider("cf", "cloudflare", map[string]string{"api_token": "keep"}, true)
	_ = s.AddZone("cf", "example.com")
	if _, err = s.db.Exec("ALTER TABLE managed_zones ADD COLUMN enabled INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if reopened, err := Open(dir); err == nil {
		reopened.Close()
		t.Fatal("disabled assignment silently activated")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "funnel.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("UPDATE managed_zones SET enabled=1"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	zones, _ := s.Zones()
	_, cfg, _ := s.ProviderConfig("cf")
	if len(zones) != 1 || cfg["api_token"] != "keep" {
		t.Fatal("migration lost data")
	}
	var count int
	_ = s.db.QueryRow("SELECT count(*) FROM pragma_table_info('managed_zones') WHERE name='enabled'").Scan(&count)
	if count != 0 {
		t.Fatal("enabled column retained")
	}
}

func TestHostnamePatternMigrationPreservesRules(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"revision":7,"rules":[{"id":"stable","name":"Test","hostname_regex":"app[0-9]{1,3}[.]example[.]com","roles":["tag:allowed"],"gateway_ids":[],"loadbalancer_hostnames":["edge.example.com"],"enabled":true}]}`
	if _, err = s.db.Exec("INSERT INTO settings(name,value,secret) VALUES('exposure_policy',?,0)", raw); err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec("DELETE FROM schema_migrations WHERE version='005_policy_hostname_patterns.sql'")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err = s.GetJSON("exposure_policy", &d); err != nil {
		t.Fatal(err)
	}
	rules := d["rules"].([]any)
	rule := rules[0].(map[string]any)
	if d["revision"] != float64(8) || rule["id"] != "stable" || rule["hostname_regexes"].([]any)[0] != "app[0-9]{1,3}[.]example[.]com" || rule["enabled"] != true {
		t.Fatal(d)
	}
	if _, old := rule["hostname_regex"]; old {
		t.Fatal("scalar field retained")
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var after map[string]any
	_ = s.GetJSON("exposure_policy", &after)
	if after["revision"] != float64(8) {
		t.Fatal("migration reran", after)
	}
}

func TestNodeDeploymentMigrationPreservesSettingsAndCredentials(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.UpsertNode("edge", "loadbalancer", "edge")
	_ = s.UpsertNode("docker-gateway", "in"+"gress", "docker-gateway")
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "loadbalancer_hostname": "edge.example.com", "loadbalancer_public_ipv4": "8.8.8.8", "cert_email": "admin@example.com", "label_keys": "vendor.expose", "deny_networks": "private-*"})
	_ = s.SetJSON("node_health:edge", map[string]any{"public_ipv4": []string{"1.1.1.1"}, "received_at": 123})
	_ = s.SetSecrets(map[string]string{"tailscale_oauth_client_id": "id", "tailscale_oauth_client_secret": "secret"})
	_, err = s.db.Exec("DELETE FROM schema_migrations WHERE version IN ('006_node_deployment_settings.sql','007_tailscale_endpoint_discovery.sql')")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var edge, gateway, settings map[string]any
	if err = s.GetJSON("node_deployment:edge", &edge); err != nil {
		t.Fatal(err)
	}
	_ = s.GetJSON("node_deployment:docker-gateway", &gateway)
	_ = s.GetJSON("tailnet", &settings)
	if edge["cert_email"] != "admin@example.com" || gateway["deny_networks"] != "private-*" {
		t.Fatal(edge, gateway)
	}
	if _, exists := edge["public_ipv4"]; exists {
		t.Fatal("removed override retained")
	}
	if _, exists := settings["loadbalancer_public_ipv4"]; exists {
		t.Fatal("global override retained")
	}
	secret, err := s.GetSecret("tailscale_oauth_client_secret")
	if err != nil || secret != "secret" {
		t.Fatal("lost credential", err)
	}
	var health map[string]any
	_ = s.GetJSON("node_health:edge", &health)
	if _, exists := health["public_ipv4"]; exists || health["received_at"] != float64(123) {
		t.Fatal("health migration changed freshness or retained IP", health)
	}
	var encrypted string
	_ = s.db.QueryRow("SELECT value FROM settings WHERE name='tailscale_oauth_client_secret'").Scan(&encrypted)
	if encrypted == secret {
		t.Fatal("plaintext stored")
	}
	// A failure midway through a credential-pair update must leave both unchanged.
	_, err = s.db.Exec(`CREATE TRIGGER block_credentials BEFORE UPDATE ON settings WHEN NEW.name='tailscale_oauth_client_secret' BEGIN SELECT RAISE(ABORT,'fixture'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetSecrets(map[string]string{"tailscale_oauth_client_id": "changed", "tailscale_oauth_client_secret": "changed"}); err == nil {
		t.Fatal("expected failure")
	}
	id, _ := s.GetSecret("tailscale_oauth_client_id")
	secret, _ = s.GetSecret("tailscale_oauth_client_secret")
	if id != "id" || secret != "secret" {
		t.Fatal("partial credential update")
	}
}
