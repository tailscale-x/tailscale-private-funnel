package web

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestForwarderBindingAndReportAggregation(t *testing.T) {
	c, s := testControl(t)
	if e := s.UpsertNode("parent", "k8s-gateway", "controller"); e != nil {
		t.Fatal(e)
	}

	a, e := reserveTestForwarder(s, "parent", "node-a", "a")
	if e != nil {
		t.Fatal(e)
	}
	b, e := reserveTestForwarder(s, "parent", "node-b", "b")
	if e != nil {
		t.Fatal(e)
	}
	if e = bindTestNode(s, a.ID, "tail-a", nil); e != nil {
		t.Fatal(e)
	}
	if e = bindTestNode(s, b.ID, "tail-b", nil); e != nil {
		t.Fatal(e)
	}
	a, _ = s.Node(a.ID)
	app := store.ApplicationRecord{ID: "app", BackendSource: "kubernetes", AllowedGatewayNodes: []string{"parent"}, Enabled: true}
	if !applicationAllows(app, a) {
		t.Fatal("child did not inherit its installation")
	}
	app.AllowedGatewayNodes = []string{"other"}
	if applicationAllows(app, a) {
		t.Fatal("cross-installation assignment allowed")
	}
	send := func(id string, exposures []dns.GatewayExposure) {
		t.Helper()
		env, e := report.NewSnapshot("report", id, map[string]any{"exposures": exposures, "expires_at": time.Now().Add(time.Minute).Unix()}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(env)
		if e = s.AcceptReport(id, env.Nonce, raw); e != nil {
			t.Fatal(e)
		}
	}
	send(a.ID, []dns.GatewayExposure{{ApplicationID: "app", PublicHostname: "app.example.com", Reachable: true}})
	send(b.ID, nil)
	parent, _ := s.Node("parent")
	if got := c.freshReport(parent); len(got) != 1 || !got[0].Reachable {
		t.Fatal("empty child erased healthy sibling", got)
	}
	if e = s.RevokeNode(b.ID); e != nil {
		t.Fatal(e)
	}
	if len(c.freshReport(parent)) != 1 {
		t.Fatal("revoking sibling removed healthy report")
	}
	if e = s.RevokeNode(a.ID); e != nil {
		t.Fatal(e)
	}
	if len(c.freshReport(parent)) != 0 {
		t.Fatal("revoked child still authorized")
	}
}
func TestForwarderReservationRejectsRebinding(t *testing.T) {
	_, s := testControl(t)
	_ = s.UpsertNode("p", "k8s-gateway", "p")
	_ = s.UpsertNode("wrong", "docker-gateway", "w")
	n, e := reserveTestForwarder(s, "p", "uid", "node")
	if e != nil {
		t.Fatal(e)
	}
	again, e := reserveTestForwarder(s, "p", "uid", "node")
	if e != nil || again.ID != n.ID {
		t.Fatal("reservation not idempotent", e)
	}
	_ = s.RevokeNode(n.ID)
	if _, e = reserveTestForwarder(s, "p", "uid", "node"); e == nil {
		t.Fatal("revoked child reenrolled")
	}
}

func TestAuthorizedReportsUseLogicalInstallationAndHealthySiblings(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.UpsertNode("parent", "k8s-gateway", "controller")
	_ = s.UpsertNode("other", "k8s-gateway", "other")

	a, _ := reserveTestForwarder(s, "parent", "node-a", "a")
	b, _ := reserveTestForwarder(s, "parent", "node-b", "b")
	for _, n := range []store.NodeRecord{a, b} {
		if err := bindTestNode(s, n.ID, "tail-"+n.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	app := store.ApplicationRecord{ID: "app", BackendSource: "kubernetes", ServiceName: "svc:app", BackendPort: 80, ServicePort: 80, ServiceFQDN: "100.100.1.1", PublicHostnames: []string{"app.example.com"}, AllowedGatewayNodes: []string{"parent"}, Enabled: true}
	if err := s.UpsertApplication(app); err != nil {
		t.Fatal(err)
	}
	send := func(id string, es []dns.GatewayExposure) {
		t.Helper()
		env, _ := report.NewSnapshot("report", id, map[string]any{"exposures": es, "expires_at": time.Now().Add(time.Minute).Unix()}, time.Now())
		raw, _ := json.Marshal(env)
		if err := s.AcceptReport(id, env.Nonce, raw); err != nil {
			t.Fatal(err)
		}
	}
	send(a.ID, []dns.GatewayExposure{{ApplicationID: "app", ServiceName: "svc:app", PublicHostname: "app.example.com", Port: 80, Reachable: true}})
	send(b.ID, nil)
	d := policy.Document{Rules: []policy.Rule{{Name: "installation", HostnameRegexes: []string{`^app[.]example[.]com$`}, GatewayIDs: []string{"parent"}, Enabled: true}}}
	if got := c.authorizedReports(d); len(got["parent"]) != 1 || len(got["other"]) != 0 || len(got[a.ID]) != 0 {
		t.Fatal("report ownership escaped logical installation", got)
	}
	_ = s.RevokeNode(b.ID)
	if len(c.authorizedReports(d)["parent"]) != 1 {
		t.Fatal("revoked empty sibling erased healthy publication")
	}
	_ = s.RevokeNode(a.ID)
	if len(c.authorizedReports(d)["parent"]) != 0 {
		t.Fatal("revoked hosts still publish")
	}
}

func TestRevokedInstallationDisablesOnlyItsApplications(t *testing.T) {
	c, s := testControl(t)
	for _, id := range []string{"parent", "other"} {
		_ = s.UpsertNode(id, "k8s-gateway", id)
	}
	for _, id := range []string{"parent", "other"} {
		if err := s.UpsertApplication(store.ApplicationRecord{ID: id + "-app", BackendSource: "kubernetes", ServiceName: "svc:" + id, ServiceFQDN: "100.100.1.1", BackendPort: 80, ServicePort: 80, PublicHostnames: []string{id + ".example.com"}, AllowedGatewayNodes: []string{id}, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.RevokeNode("parent")
	n, _ := s.Node("parent")
	_ = c.cleanupRevokedNode(context.Background(), n) // No external provider configured in this storage test.
	apps, _ := s.Applications()
	for _, a := range apps {
		if a.ID == "parent-app" && a.Enabled {
			t.Fatal("revoked installation still owns enabled intent")
		}
		if a.ID == "other-app" && !a.Enabled {
			t.Fatal("other installation disabled")
		}
	}
}
