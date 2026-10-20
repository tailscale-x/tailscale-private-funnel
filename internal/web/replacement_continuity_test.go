package web

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
)

// A replacement device changes runtime authority, not logical DNS ownership.
// Exercise ordinary instance release/re-registration without revocation/reset.
func TestReplacementDevicePreservesOwnedDNSAndValidatedRoutes(t *testing.T) {
	for _, id := range []string{"gateway", "gateway:node0", "edge-id"} {
		t.Run(id, func(t *testing.T) {
			c, s, ctx := retentionFixture(t)
			first, err := c.nodeConfiguration(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var before nodeconfig.Configuration
			if err = json.Unmarshal(first.Payload, &before); err != nil || len(before.Routes) != 1 {
				t.Fatal(err, before)
			}
			ledger, err := exposureLedger(t, s, "fixture", "example.com", "app0.example.com")
			if err != nil {
				t.Fatal(err)
			}
			exposures, err := s.Exposures()
			if err != nil {
				t.Fatal(err)
			}
			reg, err := s.ComponentRegistration(id)
			if err != nil {
				t.Fatal(err)
			}
			oldInstance, oldDevice := reg.InstanceID, reg.DeviceID
			if err = s.ReleaseInstance(id, oldInstance, oldDevice); err != nil {
				t.Fatal(err)
			}
			reg.InstanceID = "replacement-" + id
			reg.DeviceID = "new-device-" + id
			reg.Addresses = []string{"100.64.0.88"}
			if _, err = s.RegisterComponent(reg, time.Now()); err != nil {
				t.Fatal(err)
			}
			if s.CheckInstance(id, oldInstance, oldDevice, time.Now()) == nil {
				t.Fatal("old identity retained runtime authority")
			}
			loseReports(t, s)
			if id == "edge-id" {
				ctx = context.WithValue(ctx, principalKey{}, Principal{NodeID: reg.DeviceID, Tags: []string{identity.Tag("main", identity.LoadBalancer)}})
				ctx = context.WithValue(ctx, componentContext{}, componentContext{ID: id, Instance: reg.InstanceID})
			}
			// A fresh Control object models a replaced control binary/process.
			current := &Control{Store: s, Installation: c.Installation, ReportTTL: c.ReportTTL}
			if err = current.reconcileExposures(t.Context()); err != nil {
				t.Fatal(err)
			}
			after, err := exposureLedger(t, s, "fixture", "example.com", "app0.example.com")
			if err != nil || !reflect.DeepEqual(ledger, after) {
				t.Fatal("ownership/ledger changed", err, ledger, after)
			}
			afterExposures, err := s.Exposures()
			if err != nil || !reflect.DeepEqual(exposures, afterExposures) {
				t.Fatal("exposure identity changed", err)
			}
			got, err := current.nodeConfiguration(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var cfg nodeconfig.Configuration
			if err = json.Unmarshal(got.Payload, &cfg); err != nil || len(cfg.Routes) != 1 || !sameRoute(before.Routes[0], cfg.Routes[0]) {
				t.Fatal("replacement withdrew validated route", err, cfg)
			}
		})
	}
}
