package web

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func retentionFixture(t *testing.T) (*Control, *store.Store, context.Context) {
	t.Helper()
	c, ctx := configurationFanoutFixture(t, 1)
	apps, err := c.Store.Applications()
	if err != nil {
		t.Fatal(err)
	}
	a := apps[0]
	a.AllowedGatewayNodes = []string{"gateway"}
	if err = c.Store.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	// The load balancer owns its address independently of exposure ownership.
	if err = c.Store.ReplaceLedger("fixture", "example.com", "loadbalancer:edge-id", []libdns.Record{libdns.RR{Name: "edge.example.com", Type: "A", Data: "203.0.113.5"}}); err != nil {
		t.Fatal(err)
	}
	if err = c.reconcileExposures(t.Context()); err != nil {
		t.Fatal(err)
	}
	return c, c.Store, ctx
}
func loseReports(t *testing.T, s *store.Store) {
	t.Helper()
	reports, err := s.Reports()
	if err != nil {
		t.Fatal(err)
	}
	for id := range reports {
		if err = s.DeleteReport(id); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SetJSON("node_health:edge-id", loadBalancerHealth{ReceivedAt: time.Now().Add(-10 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetJSON("node_endpoints:edge-id", map[string]any{"checked_at": time.Now().Add(-10 * time.Minute).Unix()}); err != nil {
		t.Fatal(err)
	}
}
func TestReportLossPreservesOwnedDNSAndPreviouslyReadyRoutes(t *testing.T) {
	c, s, ctx := retentionFixture(t)
	before, err := exposureLedger(t, s, "fixture", "example.com", "app0.example.com")
	if err != nil || len(before) != 1 {
		t.Fatal(before, err)
	}
	// Establish previously ready routes before losing connectivity.
	if _, err = c.nodeConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	loseReports(t, s)
	if err = c.reconcileExposures(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := exposureLedger(t, s, "fixture", "example.com", "app0.example.com")
	if err != nil || len(after) != 1 || before[0].Record.RR() != after[0].Record.RR() {
		t.Fatal("transient outage changed DNS", after, err)
	}
	env, err := c.nodeConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Routes []any `json:"routes"`
	}
	if err = json.Unmarshal(env.Payload, &cfg); err != nil || len(cfg.Routes) != 1 {
		t.Fatal("connectivity loss withdrew a working route", cfg, err)
	}
	// Retention is not permission to create new names from unverified readiness.
	if err = s.UpsertApplication(store.ApplicationRecord{ID: "new", ServiceName: "svc:new", BackendSource: "kubernetes", PublicHostnames: []string{"new.example.com"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = c.reconcileExposures(t.Context()); err != nil {
		t.Fatal(err)
	}
	newRecords, err := exposureLedger(t, s, "fixture", "example.com", "new.example.com")
	if err != nil || len(newRecords) != 0 {
		t.Fatal("unready name was published", newRecords, err)
	}
	edge, err := s.Node("edge-id")
	if err != nil {
		t.Fatal(err)
	}
	if err = c.reconcileLoadBalancerNode(t.Context(), edge); err == nil {
		t.Fatal("missing health diagnostic")
	}
	owned, err := s.Ledger("fixture", "example.com", "loadbalancer:edge-id")
	if err != nil || len(owned) != 1 {
		t.Fatal("lost the load balancer's owned address", owned, err)
	}
}
func TestDNSRetentionDoesNotDefeatExplicitWithdrawal(t *testing.T) {
	for _, action := range []string{"policy", "gateway revocation", "loadbalancer revocation", "application disable", "hostname removal"} {
		t.Run(action, func(t *testing.T) {
			c, s, _ := retentionFixture(t)
			loseReports(t, s)
			switch action {
			case "policy":
				if err := s.SetJSON("exposure_policy", policy.Document{Revision: 2, Rules: []policy.Rule{}}); err != nil {
					t.Fatal(err)
				}
			case "gateway revocation":
				if err := s.RevokeNode("gateway"); err != nil {
					t.Fatal(err)
				}
			case "loadbalancer revocation":
				if err := s.RevokeNode("edge-id"); err != nil {
					t.Fatal(err)
				}
			default:
				apps, err := s.Applications()
				if err != nil {
					t.Fatal(err)
				}
				a := apps[0]
				if action == "application disable" {
					a.Enabled = false
				} else {
					a.PublicHostnames = nil
				}
				if err = s.UpsertApplication(a); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.reconcileExposures(t.Context()); err != nil {
				t.Fatal(err)
			}
			records, err := exposureLedger(t, s, "fixture", "example.com", "app0.example.com")
			if err != nil || len(records) != 0 {
				t.Fatal("explicit withdrawal retained DNS", records, err)
			}
		})
	}
}
