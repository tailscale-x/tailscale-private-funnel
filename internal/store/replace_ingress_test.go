package store

import (
	"github.com/libdns/libdns"
	"testing"
	"time"
)

func TestReplaceIngressRequiresRevocationAndOwnedCleanup(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.UpsertNode("old", "ingress", "old-ingress")
	_ = s.UpsertNode("new", "ingress", "host-ingress")
	_ = s.BindNodeIdentity("new", "key", "new-device", nil)
	_ = s.UpsertApplication(ApplicationRecord{ID: "app", ServiceName: "svc:app", BackendPort: 8081, PublicHostnames: []string{"app.example.com"}, AllowedIngressNodes: []string{"old", "other"}, Enabled: true})
	if s.ReplaceIngress("old", "new") == nil {
		t.Fatal("active node replaced")
	}
	_ = s.RevokeNode("old")
	if err = s.SaveProvider("provider", "cloudflare", map[string]string{}, true); err != nil {
		t.Fatal(err)
	}
	if err = s.AddZone("provider", "example.com"); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceLedger("provider", "example.com", "ingress:old", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com", TTL: time.Minute}}); err != nil {
		t.Fatal(err)
	}
	if s.ReplaceIngress("old", "new") == nil {
		t.Fatal("DNS cleanup guard bypassed")
	}
	_ = s.ReplaceLedger("provider", "example.com", "ingress:old", nil)
	if err = s.ReplaceIngress("old", "new"); err != nil {
		t.Fatal(err)
	}
	apps, _ := s.Applications()
	a := apps[0]
	if len(a.AllowedIngressNodes) != 2 || a.AllowedIngressNodes[0] != "new" || a.AllowedIngressNodes[1] != "other" || a.ServiceName != "svc:app" || a.PublicHostnames[0] != "app.example.com" {
		t.Fatal(a)
	}
}
