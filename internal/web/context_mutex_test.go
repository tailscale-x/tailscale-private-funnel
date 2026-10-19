package web

import (
	"context"
	"errors"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"testing"
	"time"
)

func TestQueuedDNSWorkHonorsCancellation(t *testing.T) {
	var m contextMutex
	if err := m.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer m.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.Lock(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

type stallFirstDNS struct {
	*fixtureDNS
	first bool
}

func (f *stallFirstDNS) List(ctx context.Context, zone string) ([]libdns.Record, error) {
	if !f.first {
		f.first = true
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return f.fixtureDNS.List(ctx, zone)
}
func TestExposureDeadlineResumesAtNextOwner(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	if err := s.UpsertApplication(store.ApplicationRecord{ID: "second", ServiceName: "svc:second", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"z.example.com"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Exposures()
	for _, e := range es {
		r := libdns.RR{Name: e.Hostname, Type: "A", Data: "203.0.113.2"}
		f.records["example.com"] = append(f.records["example.com"], r)
		if err := s.ReplaceLedger("fixture", "example.com", "exposure:"+e.ID, []libdns.Record{r}); err != nil {
			t.Fatal(err)
		}
	}
	slow := &stallFirstDNS{fixtureDNS: f}
	factory := dns.Registry["fixture"]
	defer func() { dns.Registry["fixture"] = factory }()
	dns.Registry["fixture"] = func(map[string]string) (dns.Provider, error) { return slow, nil }
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := c.reconcileExposures(ctx); err == nil {
		t.Fatal("provider timeout hidden")
	}
	if c.exposureCursor.Load() != 1 {
		t.Fatal("deadline did not advance owner cursor", c.exposureCursor.Load())
	}
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := exposureLedger(t, s, "fixture", "example.com", "z.example.com")
	if err != nil || len(entries) != 0 {
		t.Fatal("later owner's cleanup starved", entries, err)
	}
}

func TestCancelledExposureReadNeverRemovesOwnedDNS(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := s.LedgerPartitions()
	if err != nil || len(before) == 0 {
		t.Fatal(before, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.reconcileExposures(ctx); err == nil {
		t.Fatal("cancelled snapshot accepted")
	}
	after, err := s.LedgerPartitions()
	if err != nil || len(after) != len(before) {
		t.Fatal("cancelled reads removed owned state", after, err)
	}
}

func TestUnchangedDNSDoesNotRewriteLedgerOrAudit(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := s.AuditEvents(1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.reconcileExposures(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := s.AuditEvents(1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatal("verified no-op produced durable audit writes", len(before), len(after))
	}
}

type cancelAfterDNSApply struct {
	*fixtureDNS
	cancel context.CancelFunc
}

func (f *cancelAfterDNSApply) Apply(ctx context.Context, zone string, add, remove []libdns.Record) error {
	if err := f.fixtureDNS.Apply(ctx, zone, add, remove); err != nil {
		return err
	}
	f.cancel()
	return nil
}
func TestSuccessfulDNSMutationCommitsOwnershipAfterDeadline(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory := dns.Registry["fixture"]
	defer func() { dns.Registry["fixture"] = factory }()
	dns.Registry["fixture"] = func(map[string]string) (dns.Provider, error) { return &cancelAfterDNSApply{f, cancel}, nil }
	r := libdns.RR{Name: "new.example.com", Type: "A", Data: "203.0.113.2"}
	if _, err := c.reconcile(ctx, "fixture", "example.com", "exposure:test", []libdns.Record{r}, false, false); err != nil {
		t.Fatal(err)
	}
	ledger, err := s.Ledger("fixture", "example.com", "exposure:test")
	if err != nil || len(ledger) != 1 {
		t.Fatal("provider succeeded but ownership was abandoned", ledger, err)
	}
}
