package dns

import (
	"testing"
	"time"

	"github.com/libdns/libdns"
)

func TestNormalizeNestedProviderNames(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"api.tuyensinhhcmulaw", "api.tuyensinhhcmulaw.troly.ai.vn"},
		{"sso.tuyensinhhcmulaw", "sso.tuyensinhhcmulaw.troly.ai.vn"},
		{"tuyensinhhcmulaw", "tuyensinhhcmulaw.troly.ai.vn"},
		{"api.tuyensinhhcmulaw.troly.ai.vn", "api.tuyensinhhcmulaw.troly.ai.vn"},
		{"api.example.org.", "api.example.org"},
		{"api.nottroly.ai.vn", "api.nottroly.ai.vn.troly.ai.vn"},
		{"troly.ai.vn", "troly.ai.vn"},
		{"troly.ai.vn.", "troly.ai.vn"},
		{"@", "troly.ai.vn"}, {"", "troly.ai.vn"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got := NormalizeRecords([]libdns.Record{libdns.RR{Name: tc.in, Type: "CNAME", Data: "target.example.org."}}, "troly.ai.vn.")[0].RR()
			if got.Name != tc.want || got.Data != "target.example.org." {
				t.Fatalf("got %v want name %s", got, tc.want)
			}
		})
	}
}

func TestNestedProviderRecordsReconcileWithoutDuplicateAppend(t *testing.T) {
	const zone = "troly.ai.vn"
	desired := []libdns.Record{}
	relative := []libdns.Record{}
	ledger := []LedgerEntry{}
	for _, name := range []string{"tuyensinhhcmulaw", "sso.tuyensinhhcmulaw", "api.tuyensinhhcmulaw"} {
		rr := libdns.RR{Name: name + "." + zone, Type: "A", Data: "192.0.2.1", TTL: time.Minute}
		desired = append(desired, rr)
		ledger = append(ledger, LedgerEntry{Record: rr, OwnerID: "admissions"})
		rr.Name = name
		relative = append(relative, rr)
	}
	for cycle := 0; cycle < 3; cycle++ {
		existing := NormalizeRecords(relative, zone)
		add, remove, err := OwnershipPlan("admissions", ledger, existing, desired, false)
		if err != nil || len(add) != 0 || len(remove) != 0 {
			t.Fatalf("cycle %d: add=%v remove=%v err=%v", cycle, add, remove, err)
		}
		if _, _, err := OwnershipPlan("unrelated-owner", ledger, existing, desired, false); err == nil {
			t.Fatal("normalization must not allow adopting another owner's records")
		}
	}
}
