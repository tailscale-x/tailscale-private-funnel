package report

import (
	"testing"
	"time"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

func TestTypedSnapshotRoundTrip(t *testing.T) {
	now := time.Now()
	p := &v1.ExposureReport{ExpiresAt: now.Add(time.Minute).Unix()}
	a, e := NewSnapshot("report", "node", p, now)
	if e != nil {
		t.Fatal(e)
	}
	var out v1.ExposureReport
	if e = Decode(a, &out); e != nil || out.ExpiresAt != p.ExpiresAt {
		t.Fatal(out.ExpiresAt, e)
	}
	b, _ := NewSnapshot("report", "node", p, now)
	if a.Nonce == b.Nonce {
		t.Fatal("duplicate bookkeeping nonce")
	}
	if e = a.Validate(now, 5*time.Minute); e != nil {
		t.Fatal(e)
	}
	if e = a.Validate(now.Add(6*time.Minute), 5*time.Minute); e == nil {
		t.Fatal("stale snapshot accepted")
	}
}
