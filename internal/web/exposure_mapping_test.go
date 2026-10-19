package web

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func seedEdgeReport(t *testing.T, s *store.Store, id string) {
	t.Helper()
	apps, err := s.Applications()
	if err != nil {
		t.Fatal(err)
	}
	es := []dns.GatewayExposure{}
	for _, a := range apps {
		for _, host := range a.PublicHostnames {
			e := dns.GatewayExposure{ApplicationID: a.ID, PublicHostname: host, Port: a.BackendPort, Reachable: true}
			if a.BackendSource == "tailnet" {
				e.BackendDeviceID = a.BackendDeviceID
			} else {
				e.ServiceName = a.ServiceName
			}
			es = append(es, e)
		}
	}
	env, err := report.NewSnapshot("report", id, map[string]any{"exposures": es, "expires_at": time.Now().Add(5 * time.Minute).Unix()}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(env)
	if err = s.AcceptReport(id, env.Nonce, raw); err != nil {
		t.Fatal(err)
	}
}
func exposureLedger(t *testing.T, s *store.Store, provider, zone, host string) ([]dns.LedgerEntry, error) {
	t.Helper()
	es, err := s.Exposures()
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		if e.Hostname == host {
			return s.Ledger(provider, zone, "exposure:"+e.ID)
		}
	}
	t.Fatalf("exposure missing: %s", host)
	return nil, nil
}

func TestOwnedCNAMEConversionConflictAndFailure(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	es, _ := s.Exposures()
	e := es[0]
	owner := "exposure:" + e.ID
	prior := libdns.RR{Name: e.Hostname, Type: "CNAME", Data: "edge.example.com"}
	unrelated := libdns.RR{Name: "keep.example.com", Type: "TXT", Data: "untouched"}
	f.records["example.com"] = []libdns.Record{prior, unrelated}
	_ = s.ReplaceLedger("fixture", "example.com", owner, []libdns.Record{prior})
	f.fail = "example.com"
	if err := c.reconcileExposures(context.Background()); err == nil {
		t.Fatal("provider failure hidden")
	}
	ledger, _ := s.Ledger("fixture", "example.com", owner)
	if len(ledger) != 1 || ledger[0].Record.RR().Type != "CNAME" {
		t.Fatal("failed mutation changed ledger", ledger)
	}
	f.fail = ""
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	ledger, _ = s.Ledger("fixture", "example.com", owner)
	if len(ledger) != 1 || ledger[0].Record.RR().Type != "A" || len(f.records["example.com"]) != 2 {
		t.Fatal(ledger, f.records)
	}
	f.records["example.com"] = append(f.records["example.com"], libdns.RR{Name: e.Hostname, Type: "A", Data: "203.0.113.99"})
	if err := c.reconcileExposures(context.Background()); err == nil {
		t.Fatal("unowned overwrite allowed")
	}
	if len(f.records["example.com"]) != 3 {
		t.Fatal("unowned record modified")
	}
}
func TestAmbiguousEdgeRetainsOnlyItsOwnPriorIP(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	e, _ := s.Node("edge-id")
	_ = s.SetJSON("node_endpoints:edge-id", map[string]any{"public_ipv4": []string{"203.0.113.9", "203.0.113.10"}, "checked_at": time.Now().Unix()})
	ips, degraded := c.edgeIPs(e)
	if len(ips) != 0 || !degraded {
		t.Fatal(ips, degraded)
	}
	_ = s.ReplaceLedger("fixture", "example.com", "loadbalancer:edge-id", []libdns.Record{libdns.RR{Name: "edge.example.com", Type: "A", Data: "203.0.113.5"}})
	ips, degraded = c.edgeIPs(e)
	if len(ips) != 1 || ips[0] != "203.0.113.5" || !degraded {
		t.Fatal(ips, degraded)
	}
}
