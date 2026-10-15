package store

import (
	"testing"
	"time"

	"github.com/libdns/libdns"
)

func TestReplaceGatewayRequiresRevocationAndOwnedCleanup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.UpsertNode("old", "docker-gateway", "old-gateway")
	_ = s.UpsertNode("new", "docker-gateway", "host-gateway")
	_ = s.BindNodeIdentity("new", "key", "new-device", nil)
	_ = s.UpsertApplication(ApplicationRecord{ID: "app", ServiceName: "svc:app", BackendPort: 8081, PublicHostnames: []string{"app.example.com"}, AllowedGatewayNodes: []string{"old", "other"}, Enabled: true})
	if s.ReplaceGateway("old", "new") == nil {
		t.Fatal("active node replaced")
	}
	_ = s.RevokeNode("old")
	if err = s.SaveProvider("provider", "cloudflare", map[string]string{}, true); err != nil {
		t.Fatal(err)
	}
	if err = s.AddZone("provider", "example.com"); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceLedger("provider", "example.com", "gateway:old", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com", TTL: time.Minute}}); err != nil {
		t.Fatal(err)
	}
	if s.ReplaceGateway("old", "new") == nil {
		t.Fatal("DNS cleanup guard bypassed")
	}
	_ = s.ReplaceLedger("provider", "example.com", "gateway:old", nil)
	if err = s.ReplaceGateway("old", "new"); err != nil {
		t.Fatal(err)
	}
	apps, _ := s.Applications()
	a := apps[0]
	if len(a.AllowedGatewayNodes) != 2 || a.AllowedGatewayNodes[0] != "new" || a.AllowedGatewayNodes[1] != "other" || a.ServiceName != "svc:app" || a.PublicHostnames[0] != "app.example.com" {
		t.Fatal(a)
	}
}
