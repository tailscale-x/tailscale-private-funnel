package web

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"testing"
)

type proxyDNSFixture struct {
	*fixtureDNS
	proxied     bool
	proxyWrites int
	failProxy   bool
}

func (p *proxyDNSFixture) ProxyChanges(_ context.Context, _ string, desired []libdns.Record, enabled bool) ([]dns.ProxyChange, error) {
	if len(desired) > 0 && enabled != p.proxied {
		return []dns.ProxyChange{{Record: desired[0], Proxied: enabled}}, nil
	}
	return nil, nil
}
func (p *proxyDNSFixture) ApplyProxyChanges(_ context.Context, changes []dns.ProxyChange) error {
	if p.failProxy && len(changes) > 0 {
		return errors.New("proxy denied")
	}
	for _, c := range changes {
		p.proxyWrites++
		p.proxied = c.Proxied
	}
	return nil
}
func TestRPCHostnameDNSOwnershipAndReconciliation(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	p := &proxyDNSFixture{fixtureDNS: f}
	prior := dns.Registry["cloudflare"]
	dns.Registry["cloudflare"] = func(map[string]string) (dns.Provider, error) { return p, nil }
	t.Cleanup(func() { dns.Registry["cloudflare"] = prior })
	if err := s.SaveProvider("fixture", "cloudflare", map[string]string{}, true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertApplication(store.ApplicationRecord{ID: "dns-app", ServiceName: "svc:dns", PublicHostnames: []string{"app.example.com"}}); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Exposures()
	e := es[0]
	owner := "exposure:" + e.ID
	desired := []libdns.Record{libdns.RR{Name: e.Hostname, Type: "A", Data: "203.0.113.10"}}
	admin := managementClient(t, c, Principal{NodeID: "admin"})
	viewer := managementClient(t, c, Principal{NodeID: "viewer"})
	input := &v1.HostnameDNSRequest{Id: e.ID, CloudflareProxied: true}
	if _, err := viewer.SaveHostnameDNS(t.Context(), connect.NewRequest(input)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal(err)
	}
	reply, err := admin.SaveHostnameDNS(t.Context(), connect.NewRequest(input))
	if err != nil || reply.Msg.Revision != 1 || !reply.Msg.CloudflareProxied {
		t.Fatal(reply, err)
	}
	if _, err = admin.SaveHostnameDNS(t.Context(), connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale write", err)
	}
	// A proxy choice never authorizes adoption of another owner's identical RR.
	f.records["example.com"] = desired
	if _, err = c.reconcile(t.Context(), "fixture", "example.com", owner, desired, false, false); err == nil {
		t.Fatal("unowned record adopted")
	}
	if p.proxyWrites != 0 {
		t.Fatal("ownership bypassed")
	}
	if err = s.ReplaceLedger("fixture", "example.com", owner, desired); err != nil {
		t.Fatal(err)
	}
	preview, err := c.reconcile(t.Context(), "fixture", "example.com", owner, desired, true, false)
	if err != nil || preview["proxy_updates"] != 1 || p.proxyWrites != 0 {
		t.Fatal(preview, err)
	}
	if _, err = c.reconcile(t.Context(), "fixture", "example.com", owner, desired, false, false); err != nil {
		t.Fatal(err)
	}
	if !p.proxied || p.proxyWrites != 1 {
		t.Fatal("proxy-only update skipped")
	}
	if _, err = c.reconcile(t.Context(), "fixture", "example.com", owner, desired, false, false); err != nil || p.proxyWrites != 1 {
		t.Fatal("not idempotent", err)
	}
	// Failure after creating RR must preserve ownership so the next pass retries.
	input.Revision = 1
	input.CloudflareProxied = false
	if _, err = admin.SaveHostnameDNS(t.Context(), connect.NewRequest(input)); err != nil {
		t.Fatal(err)
	}
	p.failProxy = true
	if _, err = c.reconcile(t.Context(), "fixture", "example.com", owner, desired, false, false); err == nil {
		t.Fatal("proxy failure hidden")
	}
	ledger, err := s.Ledger("fixture", "example.com", owner)
	if err != nil || len(ledger) != 1 {
		t.Fatal("ownership lost", ledger, err)
	}
	p.failProxy = false
	if _, err = c.reconcile(t.Context(), "fixture", "example.com", owner, desired, false, false); err != nil || p.proxied {
		t.Fatal(err)
	}
	list, err := admin.ListExposures(t.Context(), connect.NewRequest(&v1.Empty{}))
	if err != nil || len(list.Msg.Items) != 1 || list.Msg.Items[0].Dns.CloudflareProxied || list.Msg.Items[0].Dns.Revision != 2 || !list.Msg.Items[0].CloudflareProxyAvailable {
		t.Fatal(list, err)
	}
	if err = s.SaveProvider("fixture", "fixture", nil, true); err != nil {
		t.Fatal(err)
	}
	input.Revision = 2
	input.CloudflareProxied = true
	if _, err = admin.SaveHostnameDNS(t.Context(), connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("non-Cloudflare mode accepted", err)
	}
}

func TestHostnameDNSFailedProxyCreationRetainsOwnedRecordForRetry(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	p := &proxyDNSFixture{fixtureDNS: f, failProxy: true}
	prior := dns.Registry["cloudflare"]
	dns.Registry["cloudflare"] = func(map[string]string) (dns.Provider, error) { return p, nil }
	t.Cleanup(func() { dns.Registry["cloudflare"] = prior })
	if err := s.SaveProvider("fixture", "cloudflare", nil, true); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertApplication(store.ApplicationRecord{ID: "created", ServiceName: "svc:created", PublicHostnames: []string{"new.example.com"}}); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Exposures()
	e := es[0]
	if err := s.SaveHostnameDNS(e.Hostname, true, 0); err != nil {
		t.Fatal(err)
	}
	desired := []libdns.Record{libdns.RR{Name: e.Hostname, Type: "A", Data: "203.0.113.10"}}
	owner := "exposure:" + e.ID
	if _, err := c.reconcile(t.Context(), "fixture", "example.com", owner, desired, false, false); err == nil {
		t.Fatal("creation proxy failure hidden")
	}
	owned, err := s.Ledger("fixture", "example.com", owner)
	if err != nil || len(owned) != 1 || len(f.records["example.com"]) != 1 {
		t.Fatal("created record orphaned", owned, err)
	}
	p.failProxy = false
	if _, err = c.reconcile(t.Context(), "fixture", "example.com", owner, desired, false, false); err != nil || !p.proxied || len(f.records["example.com"]) != 1 {
		t.Fatal("retry duplicated DNS or failed", err)
	}
}
