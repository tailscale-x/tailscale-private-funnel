package dns

import (
	"context"
	"fmt"
	"testing"

	"github.com/libdns/libdns"
)

func TestCatalogIncludesCloudflareAndCommonProviders(t *testing.T) {
	ids := map[string]bool{}
	for _, id := range ProviderIDs() {
		ids[id] = true
	}
	for _, id := range []string{"cloudflare", "route53", "googleclouddns", "rfc2136"} {
		if !ids[id] {
			t.Fatalf("catalog missing %s", id)
		}
	}
}

func TestConcreteProviderFactories(t *testing.T) {
	for _, id := range []string{"cloudflare", "godaddy", "route53"} {
		factory, ok := Registry[id]
		if !ok {
			t.Fatalf("missing factory %s", id)
		}
		provider, err := factory(map[string]string{})
		if err != nil || provider == nil {
			t.Fatalf("factory %s: %v", id, err)
		}
	}
}

func TestPlanOnlyRemovesOwnedRecords(t *testing.T) {
	old := libdns.RR{Name: "old.example.com.", Type: "A", Data: "192.0.2.1"}
	keep := libdns.RR{Name: "keep.example.com.", Type: "A", Data: "192.0.2.2"}
	want := libdns.RR{Name: "new.example.com.", Type: "CNAME", Data: "loadbalancer.example.com."}
	add, remove := Plan("installation-a", []LedgerEntry{{Record: old, OwnerID: "installation-a"}, {Record: keep, OwnerID: "other"}}, []libdns.Record{want})
	if len(add) != 1 || add[0].RR().Name != want.Name {
		t.Fatalf("unexpected additions: %#v", add)
	}
	if len(remove) != 1 || remove[0].RR().Name != old.Name {
		t.Fatalf("unexpected removals: %#v", remove)
	}
}

func TestOwnershipPlanRequiresExplicitAdoption(t *testing.T) {
	desired := libdns.RR{Name: "app.example.com.", Type: "A", Data: "192.0.2.8"}
	existing := libdns.RR{Name: "app.example.com.", Type: "A", Data: "192.0.2.8"}
	if _, _, err := OwnershipPlan("owner", nil, []libdns.Record{existing}, []libdns.Record{desired}, false); err == nil {
		t.Fatal("expected unowned exact record to require explicit permission")
	}
	add, remove, err := OwnershipPlan("owner", nil, []libdns.Record{existing}, []libdns.Record{desired}, true)
	if err != nil || len(add) != 0 || len(remove) != 0 {
		t.Fatalf("explicit adoption should be non-destructive: add=%d remove=%d err=%v", len(add), len(remove), err)
	}
}

func TestOwnershipPlanRequiresOverrideForAnotherOwner(t *testing.T) {
	desired := libdns.RR{Name: "app.example.com.", Type: "A", Data: "192.0.2.8"}
	if _, _, err := OwnershipPlan("owner", []LedgerEntry{{Record: desired, OwnerID: "other"}}, []libdns.Record{desired}, []libdns.Record{desired}, false); err == nil {
		t.Fatal("expected another owner's record to require explicit permission")
	}
	if _, _, err := OwnershipPlan("owner", []LedgerEntry{{Record: desired, OwnerID: "other"}}, []libdns.Record{desired}, []libdns.Record{desired}, true); err != nil {
		t.Fatalf("explicit override should permit migration: %v", err)
	}
}

func TestExactApplyRestoresOwnedCNAMEAfterPartialAFailure(t *testing.T) {
	old := libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}
	a := libdns.RR{Name: old.Name, Type: "A", Data: "203.0.113.1"}
	b := libdns.RR{Name: old.Name, Type: "A", Data: "203.0.113.2"}
	records := []libdns.Record{old}
	appendOne := func(_ context.Context, r libdns.Record) error {
		if r.RR().Data == b.Data {
			return fmt.Errorf("provider append failure")
		}
		records = append(records, r)
		return nil
	}
	deleteOne := func(_ context.Context, r libdns.Record) error {
		out := []libdns.Record{}
		for _, v := range records {
			if RecordKey(v) != RecordKey(r) {
				out = append(out, v)
			}
		}
		records = out
		return nil
	}
	err := applyExact(context.Background(), []libdns.Record{a, b}, []libdns.Record{old}, appendOne, deleteOne)
	if err == nil || len(records) != 1 || RecordKey(records[0]) != RecordKey(old) {
		t.Fatal(err, records)
	}
}
