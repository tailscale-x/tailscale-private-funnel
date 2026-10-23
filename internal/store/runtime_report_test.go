package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDatabaseContractAtomicRuntimeReport(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			for _, id := range []string{"one", "other"} {
				if e := s.UpsertNode(id, "docker-gateway", id); e != nil {
					t.Fatal(e)
				}
			}
			old := BackendAssignment{ID: "old", ApplicationID: "app", NodeID: "one", BackendIP: "192.0.2.1", BackendPort: 80, Reachable: true}
			other := BackendAssignment{ID: "other", ApplicationID: "app", NodeID: "other", BackendPort: 80}
			for _, b := range []BackendAssignment{old, other} {
				if e := s.UpsertRegisteredBackend(b); e != nil {
					t.Fatal(e)
				}
			}
			if e := s.AcceptRuntimeReport("one", "initial", []byte(`{"prior":true}`), []string{"prior"}, []BackendAssignment{old}); e != nil {
				t.Fatal(e)
			}
			good := old
			good.ID = "new"
			bad := good
			bad.ID = "invalid"
			bad.NodeID = "other"
			if e := s.AcceptRuntimeReport("one", "attempt", []byte(`{"changed":true}`), []string{"changed"}, []BackendAssignment{good, bad}); e == nil {
				t.Fatal("foreign backend accepted")
			}
			reports, e := s.Reports()
			if e != nil || reports["one"] != `{"prior":true}` {
				t.Fatal("report partially committed", reports, e)
			}
			var n int
			if e = s.db.QueryRow(`SELECT count(*) FROM report_nonces WHERE node_id=? AND nonce=?`, "one", "attempt").Scan(&n); e != nil || n != 0 {
				t.Fatal("nonce consumed on failure", n, e)
			}
			if e = s.db.QueryRow(`SELECT count(*) FROM backend_assignments WHERE id=?`, "new").Scan(&n); e != nil || n != 0 {
				t.Fatal("backend partially committed", n, e)
			}
			var roles []string
			if e = s.GetJSON("node_policy_roles:one", &roles); e != nil || len(roles) != 1 || roles[0] != "prior" {
				t.Fatal("roles partially committed", roles, e)
			}
			if e = s.AcceptRuntimeReport("one", "attempt", []byte(`{"changed":true}`), []string{"changed"}, []BackendAssignment{good}); e != nil {
				t.Fatal(e)
			}
			if e = s.AcceptRuntimeReport("one", "attempt", []byte(`{"replayed":true}`), nil, nil); e == nil {
				t.Fatal("replay accepted")
			}
			for id, want := range map[string]int{"old": 1, "new": 0, "other": 0} {
				if e = s.db.QueryRow(`SELECT revoked FROM backend_assignments WHERE id=?`, id).Scan(&n); e != nil || n != want {
					t.Fatal(id, n, want, e)
				}
			}
			if e = s.RevokeNode("one"); e != nil {
				t.Fatal(e)
			}
			if e = s.AcceptRuntimeReport("one", "revoked", []byte(`{}`), nil, nil); e == nil {
				t.Fatal("revoked node accepted")
			}
		})
	}
}
func TestRuntimeReportCancellationBoundsPoolWait(t *testing.T) {
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	conn, e := s.db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	timer := time.AfterFunc(time.Second, func() { conn.Close() })
	defer timer.Stop()
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	e = s.WithContext(ctx).AcceptRuntimeReport("node", "nonce", []byte(`{}`), nil, nil)
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatal("report ignored cancellation", e, time.Since(start))
	}
}
func TestDatabaseContractJSONNoOpDoesNotUpdate(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			if _, e := s.db.Exec(`CREATE TABLE setting_update_count(n INTEGER NOT NULL)`); e != nil {
				t.Fatal(e)
			}
			if _, e := s.db.Exec(`INSERT INTO setting_update_count VALUES(0)`); e != nil {
				t.Fatal(e)
			}
			trigger := `CREATE TRIGGER count_updates AFTER UPDATE ON settings BEGIN UPDATE setting_update_count SET n=n+1; END`
			if pg {
				if _, e := s.db.Exec(`CREATE FUNCTION count_setting_updates() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE setting_update_count SET n=n+1; RETURN NEW; END $$`); e != nil {
					t.Fatal(e)
				}
				trigger = `CREATE TRIGGER count_updates AFTER UPDATE ON settings FOR EACH ROW EXECUTE FUNCTION count_setting_updates()`
			}
			if _, e := s.db.Exec(trigger); e != nil {
				t.Fatal(e)
			}
			for i := 0; i < 20; i++ {
				if e := s.SetJSON("roles", []string{"same"}); e != nil {
					t.Fatal(e)
				}
			}
			var n int
			if e := s.db.QueryRow(`SELECT n FROM setting_update_count`).Scan(&n); e != nil || n != 0 {
				t.Fatal("identical metadata caused writes", n, e)
			}
			if e := s.SetJSON("roles", []string{"changed"}); e != nil {
				t.Fatal(e)
			}
			if e := s.db.QueryRow(`SELECT n FROM setting_update_count`).Scan(&n); e != nil || n != 1 {
				t.Fatal("changed metadata not saved", n, e)
			}
		})
	}
}
