package store

import (
	"testing"
	"time"
)

func TestEnrollmentExpiryIsEnforcedAtRedemptionAndBinding(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, code, err := s.CreateEnrollment("tailnet-gateway", "site")
	if err != nil {
		t.Fatal(err)
	}
	var expiry int64
	if err = s.db.QueryRow(`SELECT expires_at FROM enrollments WHERE id=?`, e.ID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	if expiry < time.Now().Unix()+890 || expiry > time.Now().Unix()+900 {
		t.Fatal("wrong lifetime", expiry)
	}
	if _, err = s.db.Exec(`UPDATE enrollments SET expires_at=? WHERE id=?`, time.Now().Unix()-1, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RedeemEnrollment(e.ID, code); err == nil {
		t.Fatal("redeemed expired code")
	}
	if err = s.BindEnrollment(e.ID, code, "public-key", "device-id", "site", nil); err == nil {
		t.Fatal("bound expired code")
	}
	if _, err = s.Node(e.ID); err == nil {
		t.Fatal("expired code created node")
	}
}

func TestEnrollmentExpiryMigrationPreservesBoundIdentity(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	bound, code, err := s.CreateEnrollment("tailnet-gateway", "bound")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindEnrollment(bound.ID, code, "persistent-public-key", "persistent-device", "bound", nil); err != nil {
		t.Fatal(err)
	}
	pending, pendingCode, err := s.CreateEnrollment("tailnet-gateway", "pending")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE enrollments SET created_at=1 WHERE hostname='pending'`, `ALTER TABLE enrollments DROP COLUMN expires_at`, `DELETE FROM schema_migrations WHERE version='016_enrollment_expiry.sql'`} {
		if _, err = s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.Node(bound.ID)
	if err != nil || n.TailnetID != "persistent-device" || n.PublicKey != "persistent-public-key" || n.Revoked {
		t.Fatal("migration changed enrolled identity", n, err)
	}
	if _, err = s.RedeemEnrollment(pending.ID, pendingCode); err == nil {
		t.Fatal("migration extended old pending enrollment")
	}
}
