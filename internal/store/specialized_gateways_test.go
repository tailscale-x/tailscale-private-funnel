package store

import (
	"testing"

	"github.com/libdns/libdns"
)

func TestSpecializedGatewayMigrationPreservesState(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	_ = s.UpsertNode("node", "docker-gateway", "old-host")
	_ = bindTestNode(s, "node", "stable-device", []string{"100.70.0.1"})
	_ = s.SetSecret("provider-test", "credential")
	_ = s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "test"}, true)
	_ = s.AddZone("provider", "example.com")
	_ = s.ReplaceLedger("provider", "example.com", "exposure:known", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "A", Data: "1.1.1.1"}})
	for _, q := range []string{`UPDATE nodes SET mode='ingress' WHERE id='node'`, `ALTER TABLE applications RENAME COLUMN allowed_gateway_nodes TO allowed_ingress_nodes`, `ALTER TABLE applications DROP COLUMN http_config`, `DELETE FROM schema_migrations WHERE version='014_specialized_gateways.sql'`, `INSERT INTO settings(name,value,secret) VALUES('exposure_policy','{"revision":4,"rules":[{"id":"rule","name":"Allowed","hostname_regexes":[".*"],"ingress_ids":["node"],"loadbalancer_ids":["edge"],"enabled":true}]}',0)`} {
		if _, e = s.db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	n, e := s.Node("node")
	if e != nil || n.Mode != "docker-gateway" || n.TailnetID != "stable-device" {
		t.Fatal(n, e)
	}
	secret, e := s.GetSecret("provider-test")
	if e != nil || secret != "credential" {
		t.Fatal(e)
	}
	ledger, e := s.Ledger("provider", "example.com", "exposure:known")
	if e != nil || len(ledger) != 1 {
		t.Fatal(ledger, e)
	}
	var d struct {
		Rules []map[string]any `json:"rules"`
	}
	if e = s.GetJSON("exposure_policy", &d); e != nil || len(d.Rules) != 1 {
		t.Fatal(d, e)
	}
	if _, ok := d.Rules[0]["loadbalancer_ids"]; ok {
		t.Fatal("selector retained")
	}
	if _, ok := d.Rules[0]["gateway_ids"]; !ok {
		t.Fatal("gateway reference lost")
	}
}
