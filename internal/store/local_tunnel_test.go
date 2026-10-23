package store

import (
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
	"testing"
	"time"
)

func TestDatabaseContractLocalTunnelRetirementAndFreshness(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			now := time.Now()
			r := Registration{Runtime: tunnel.Runtime, ComponentID: "local", InstanceID: "process", DeviceID: "host-device", Installation: "main", Mode: "tailnet-gateway", Name: "Local", Connection: "host"}
			if _, err := s.RegisterComponent(r, now); err != nil {
				t.Fatal(err)
			}
			_ = s.SaveProvider("dns", "cloudflare", map[string]string{"api_token": "encrypted"}, true)
			_ = s.AddZone("dns", "example.com")
			app := ApplicationRecord{ID: "app", ServiceName: "svc:local-test", BackendSource: "local", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com"}, AllowedGatewayNodes: []string{"local"}, Enabled: true}
			if err := s.UpsertApplication(app); err != nil {
				t.Fatal(err)
			}
			other := ApplicationRecord{ID: "keep", ServiceName: "svc:other", BackendSource: "docker", PublicHostnames: []string{"keep.example.com"}, Enabled: true}
			if err := s.UpsertApplication(other); err != nil {
				t.Fatal(err)
			}
			exposures, _ := s.Exposures()
			var id string
			for _, e := range exposures {
				if e.Hostname == "app.example.com" {
					id = e.ID
				}
			}
			_ = s.ReplaceLedger("dns", "example.com", "exposure:"+id, []libdns.Record{libdns.RR{Name: "app.example.com", Type: "A", Data: "203.0.113.5"}})
			if retired, err := s.RetireLocalTunnel("local", now.Add(-5*time.Minute).Unix()); err != nil || retired {
				t.Fatal("fresh runtime retired", retired, err)
			}
			if retired, err := s.RetireLocalTunnel("local", 0); err != nil || !retired {
				t.Fatal(retired, err)
			}
			ledger, err := s.Ledger("dns", "example.com", "exposure:"+id)
			if err != nil || len(ledger) != 1 {
				t.Fatal("retirement bypassed DNS reconciliation", ledger, err)
			}
			for _, e := range mustExposures(t, s) {
				if e.Hostname == "app.example.com" && (e.Active || e.ID != id) {
					t.Fatal(e)
				}
				if e.Hostname == "keep.example.com" && !e.Active {
					t.Fatal("unrelated application retired")
				}
			}
			if err = s.RenewInstance("local", "process", "host-device", time.Now()); err == nil {
				t.Fatal("withdrawn instance resumed")
			}
			// Restart restores the same exposure/owner, with a new active session.
			r.InstanceID = "replacement"
			if _, err = s.RegisterComponent(r, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err = s.UpsertApplication(app); err != nil {
				t.Fatal(err)
			}
			for _, e := range mustExposures(t, s) {
				if e.Hostname == "app.example.com" && (!e.Active || e.ID != id) {
					t.Fatal("restart changed exposure identity", e)
				}
			}
			// An expired report alone cannot retire an actively leased process.
			if _, err = s.db.Exec(`UPDATE nodes SET last_report=? WHERE id='local'`, now.Add(-10*time.Minute).Unix()); err != nil {
				t.Fatal(err)
			}
			if retired, err := s.RetireLocalTunnel("local", now.Add(-5*time.Minute).Unix()); err != nil || retired {
				t.Fatal("active lease retired", retired, err)
			}
			if _, err = s.db.Exec(`UPDATE component_instances SET lease_until=? WHERE component_id='local'`, now.Add(-time.Minute).Unix()); err != nil {
				t.Fatal(err)
			}
			if retired, err := s.RetireLocalTunnel("local", now.Add(-5*time.Minute).Unix()); err != nil || !retired {
				t.Fatal("expired process retained", retired, err)
			}
			if _, err = s.RegisterComponent(Registration{Runtime: tunnel.Runtime, ComponentID: "local", InstanceID: "third", DeviceID: "host-device", Installation: "main", Mode: "tailnet-gateway", Name: "Local", Connection: "host"}, time.Now()); err != nil {
				t.Fatal(err)
			}
			// A stale scan must not retire the replacement's fresh registration.
			if retired, err := s.RetireLocalTunnel("local", now.Add(-time.Minute).Unix()); err != nil || retired {
				t.Fatal(retired, err)
			}
		})
	}
}
func mustExposures(t *testing.T, s *Store) []ExposureRecord {
	t.Helper()
	es, err := s.Exposures()
	if err != nil {
		t.Fatal(err)
	}
	return es
}
