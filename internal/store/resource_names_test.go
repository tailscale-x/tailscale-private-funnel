package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func oldNamesFixture(t *testing.T) (string, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.SaveProvider("Cloudflare-dev", "cloudflare", map[string]string{"api_token": "private-token", "account_id": "account"}, true)
	_ = s.AddZone("Cloudflare-dev", "example.com")
	_ = s.ReplaceLedger("Cloudflare-dev", "example.com", "gateway:gateway-id", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}})
	_ = s.UpsertNode("edge-id", "loadbalancer", "edge-host")
	_ = s.UpsertNode("gateway-id", "docker-gateway", "gateway-host")
	_ = s.SetJSON("node_deployment:edge-id", map[string]string{"loadbalancer_hostname": "edge.example.com"})
	_ = s.SetJSON("node_deployment:gateway-id", map[string]string{"loadbalancer_hostname": "edge.example.com"})
	_ = s.SetJSON("exposure_policy", map[string]any{"rules": []any{map[string]any{"name": "Allow", "loadbalancer_hostnames": []string{"edge.example.com"}}}})
	_ = s.SetJSON("provider_test:Cloudflare-dev", "Verified")
	_ = s.SetJSON("pending", map[string]string{"provider_id": "Cloudflare-dev"})

	_ = bindTestNode(s, "gateway-id", "tailnode", nil)
	env, _ := report.NewSnapshot("report", "gateway-id", map[string]any{"exposures": []any{map[string]any{"loadbalancer_hostname": "edge.example.com", "public_hostname": "app.example.com"}}, "expires_at": time.Now().Add(time.Minute).Unix()}, time.Now())
	raw, _ := json.Marshal(env)
	_ = s.AcceptReport("gateway-id", env.Nonce, raw)
	var cipher string
	_ = s.db.QueryRow(`SELECT config FROM providers`).Scan(&cipher)
	for _, q := range []string{`DROP INDEX managed_zones_id`, `ALTER TABLE managed_zones DROP COLUMN id`, `ALTER TABLE providers DROP COLUMN name`, `ALTER TABLE nodes DROP COLUMN name`, `ALTER TABLE applications DROP COLUMN name`, `ALTER TABLE reports DROP COLUMN snapshot`, `ALTER TABLE records DROP COLUMN zone_id`, `DELETE FROM schema_migrations WHERE version='009_resource_names'`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	return dir, raw, cipher
}
func TestResourceNameMigrationPreservesIdentityAndOwnership(t *testing.T) {
	dir, raw, cipher := oldNamesFixture(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	providers, _ := s.Providers()
	if len(providers) != 1 || providers[0].ID == "Cloudflare-dev" || providers[0].Name != "Cloudflare-dev" {
		t.Fatal(providers)
	}
	id := providers[0].ID
	_, cfg, err := s.ProviderConfig(id)
	if err != nil || cfg["api_token"] != "private-token" {
		t.Fatal(cfg, err)
	}
	var after string
	_ = s.db.QueryRow(`SELECT config FROM providers`).Scan(&after)
	if after != cipher {
		t.Fatal("encrypted credentials rewritten")
	}
	zones, _ := s.Zones()
	if len(zones) != 1 || zones[0].ID == "" || zones[0].ProviderID != id {
		t.Fatal(zones)
	}
	ledger, _ := s.Ledger(id, "example.com", "gateway:gateway-id")
	if len(ledger) != 1 || ledger[0].OwnerID != "gateway:gateway-id" {
		t.Fatal(ledger)
	}
	var zid string
	_ = s.db.QueryRow(`SELECT zone_id FROM records`).Scan(&zid)
	if zid != zones[0].ID {
		t.Fatal("ledger zone reference lost")
	}
	var original string
	_ = s.db.QueryRow(`SELECT payload FROM reports`).Scan(&original)
	if original != string(raw) {
		t.Fatal("original signed report changed")
	}
	var envelope report.Snapshot
	_ = json.Unmarshal([]byte(original), &envelope)
	if err = envelope.Validate(time.Now(), time.Minute); err != nil {
		t.Fatal(err)
	}
	reports, _ := s.Reports()
	if !strings.Contains(reports["gateway-id"], `"loadbalancer_id":"edge-id"`) {
		t.Fatal(reports)
	}
	var deployment map[string]string
	_ = s.GetJSON("node_deployment:gateway-id", &deployment)
	if deployment["loadbalancer_id"] != "edge-id" || deployment["loadbalancer_hostname"] != "" {
		t.Fatal(deployment)
	}
	var pending map[string]string
	_ = s.GetJSON("pending", &pending)
	if pending["provider_id"] != id {
		t.Fatal(pending)
	}
	var status string
	if err = s.GetJSON("provider_test:"+id, &status); err != nil || status != "Verified" {
		t.Fatal(status, err)
	}
	if err = s.RenameResource("provider", id, "  家庭 DNS  "); err != nil {
		t.Fatal(err)
	}
	if err = s.RenameResource("node", "gateway-id", "Gateway alias"); err != nil {
		t.Fatal(err)
	}
	node, _ := s.Node("gateway-id")
	if node.Hostname != "gateway-host" || node.TailnetID != "tailnode" || node.Name != "Gateway alias" {
		t.Fatal(node)
	}
	s.Close()
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	p, _, _ := reopened.ProviderConfig(id)
	if p.Name != "家庭 DNS" {
		t.Fatal(p)
	}
}
func TestResourceNameMigrationRollsBack(t *testing.T) {
	dir, _, _ := oldNamesFixture(t)
	db, err := sql.Open("sqlite", filepath.Join(dir, "funnel.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE settings SET value='{"rules":[{"loadbalancer_hostnames":["missing.example.com"]}]}' WHERE name='exposure_policy'`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(dir); err == nil {
		s.Close()
		t.Fatal("unresolved reference migration accepted")
	}
	db, err = sql.Open("sqlite", filepath.Join(dir, "funnel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var id string
	_ = db.QueryRow(`SELECT id FROM providers`).Scan(&id)
	if id != "Cloudflare-dev" {
		t.Fatal("provider ID changed despite rollback")
	}
	var n int
	_ = db.QueryRow(`SELECT count(*) FROM pragma_table_info('providers') WHERE name='name'`).Scan(&n)
	if n != 0 {
		t.Fatal("schema partially migrated")
	}
}

func TestGatewayNameMigrationPreservesIdentity(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertNode("gateway-id", "docker-gateway", "host-gateway"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE nodes SET name='Old alias',tailnet_id='stable-device' WHERE id='gateway-id'; DELETE FROM schema_migrations WHERE version='012_gateway_hostname_names'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.Node("gateway-id")
	if err != nil || n.Name != "Old alias" || n.Hostname != "host-gateway" || n.TailnetID != "stable-device" {
		t.Fatal(n, err)
	}
}
