package store

import (
	"errors"
	"sync"
	"testing"
)

func TestDatabaseContractHostnameDNS(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(map[bool]string{false: "sqlite", true: "postgres"}[pg], func(t *testing.T) {
			s, _, _ := contractStore(t, pg)
			if pg {
				s.db.SetMaxOpenConns(8)
			}
			a := ApplicationRecord{ID: "host-dns-app", ServiceName: "svc:host-dns", PublicHostnames: []string{"shared.example.com"}}
			if err := s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			es, err := s.Exposures()
			if err != nil || len(es) != 1 {
				t.Fatal(es, err)
			}
			if es[0].CloudflareProxied || es[0].DNSRevision != 0 {
				t.Fatal("default is not DNS-only", es)
			}
			var wg sync.WaitGroup
			results := make(chan error, 8)
			for range 8 {
				wg.Add(1)
				go func() { defer wg.Done(); results <- s.SaveHostnameDNS("SHARED.EXAMPLE.COM.", true, 0) }()
			}
			wg.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else if !errors.Is(err, ErrHostnameDNSConflict) {
					t.Fatal(err)
				}
			}
			if successes != 1 {
				t.Fatalf("concurrent writers accepted: %d", successes)
			}
			// Reports and temporary route membership do not own this policy.
			a.PublicHostnames = nil
			if err = s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			a.PublicHostnames = []string{"shared.example.com"}
			if err = s.UpsertApplication(a); err != nil {
				t.Fatal(err)
			}
			after, err := s.Exposure(es[0].ID)
			if err != nil || !after.CloudflareProxied || after.DNSRevision != 1 {
				t.Fatal(after, err)
			}
			if err = s.SaveHostnameDNS(after.Hostname, false, 1); err != nil {
				t.Fatal(err)
			}
			after, err = s.Exposure(es[0].ID)
			if err != nil || after.CloudflareProxied || after.DNSRevision != 2 {
				t.Fatal(after, err)
			}
		})
	}
}
