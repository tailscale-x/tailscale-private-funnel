package store

import "testing"

func TestVerifiedHostnameRefreshPreservesIdentity(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.UpsertNode("node", "loadbalancer", "public-gateway")
	_ = s.BindNodeIdentity("node", "signing-key", "stable-device", nil)
	if err = s.RefreshNodeHostname("node", "spoofed-device", "bad-name"); err == nil {
		t.Fatal("spoofed identity accepted")
	}
	if err = s.RefreshNodeHostname("node", "stable-device", "host-loadbalancer.example.ts.net."); err != nil {
		t.Fatal(err)
	}
	n, _ := s.Node("node")
	if n.Hostname != "host-loadbalancer" || n.Name != n.Hostname || n.TailnetID != "stable-device" || n.PublicKey != "signing-key" {
		t.Fatal(n)
	}
	_ = s.RevokeNode("node")
	if err = s.RefreshNodeHostname("node", "stable-device", "another-name"); err == nil {
		t.Fatal("revoked identity updated")
	}
}

func TestTailnetIdentityCannotBindTwoNodes(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.UpsertNode("first", "docker-gateway", "first")
	_ = s.UpsertNode("second", "tailnet-gateway", "second")
	if err = s.BindNodeIdentity("first", "key", "stable-device", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.BindNodeIdentity("second", "key", "stable-device", nil); err == nil {
		t.Fatal("same verified identity bound to two resources")
	}
	enrollment, code, err := s.CreateEnrollment("tailnet-gateway", "new")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindEnrollment(enrollment.ID, code, "key", "stable-device", "new", nil); err == nil {
		t.Fatal("enrollment reused an existing identity")
	}
	if _, err = s.RedeemEnrollment(enrollment.ID, code); err != nil {
		t.Fatal("failed identity binding consumed the code", err)
	}
}
