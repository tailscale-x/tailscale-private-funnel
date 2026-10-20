package store

import (
	"testing"
	"time"
)

func TestDatabaseContractExpiryRechecksFreshnessAfterCandidateScan(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "sqlite"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			var err error
			_ = s.UpsertNode("node", "docker-gateway", "node")
			old := time.Now().Add(-time.Hour).Unix()
			cutoff := time.Now().Add(-time.Minute).Unix()
			if err = s.UpsertBackend(BackendAssignment{ID: "still-configured", NodeID: "node", ApplicationID: "app", BackendPort: 8080}); err != nil {
				t.Fatal(err)
			}
			_, err = s.db.Exec(`INSERT INTO reports(node_id,payload,snapshot,received_at) VALUES('node','{}','{}',?)`, old)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = s.db.Exec(`UPDATE nodes SET last_report=? WHERE id='node'`, old)
			ids, err := s.StaleReportIDs(cutoff)
			if err != nil || len(ids) != 1 {
				t.Fatal(ids, err)
			}
			// A heartbeat/report wins after the candidate scan, before cleanup begins.
			_ = s.SaveReport("node", []byte(`{}`))
			_ = s.TouchNodeReport("node")
			for _, expire := range []func(string, int64) (bool, error){s.ExpireReportBefore, s.ExpireNodeBefore} {
				if removed, err := expire("node", cutoff); err != nil || removed {
					t.Fatal("fresh state removed", removed, err)
				}
			}
			reports, err := s.Reports()
			if err != nil || reports["node"] == "" {
				t.Fatal(reports, err)
			}
			_, _ = s.db.Exec(`UPDATE reports SET received_at=? WHERE node_id='node'`, old)
			if removed, err := s.ExpireReportBefore("node", cutoff); err != nil || !removed {
				t.Fatal("stale report retained", removed, err)
			}
			var revoked int
			if err = s.db.QueryRow(`SELECT revoked FROM backend_assignments WHERE id='still-configured'`).Scan(&revoked); err != nil || revoked != 0 {
				t.Fatal("connectivity expiry revoked a configured backend", revoked, err)
			}
			n, err := s.Node("node")
			if err != nil || n.Revoked {
				t.Fatal("expiry revoked persistent identity", n, err)
			}

		})
	}
}
