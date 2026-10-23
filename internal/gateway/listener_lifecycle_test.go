package gateway

import (
	"context"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"sync"
	"testing"
	"time"
)

func TestBackendChangesKeepServiceListenerOpen(t *testing.T) {
	c := &listenerFixtureConnection{}
	m := listenerManagerFixture(t, c)
	for i := 0; i < 20; i++ {
		if err := m.Listen(t.Context(), "svc:app", 80, "127.0.0.1:8000"); err != nil {
			t.Fatal(err)
		}
		m.Reconcile(map[string]struct{}{"svc:app:80": {}})
	}
	if c.count() != 1 {
		t.Fatal("unchanged config reopened listener", c.count())
	}
	if err := m.Listen(t.Context(), "svc:app", 80, "127.0.0.1:8001"); err != nil {
		t.Fatal(err)
	}
	if c.count() != 1 {
		t.Fatal("changed backend replaced stable listener")
	}
}
func TestFencedServiceCannotReopenUntilLeaseReacquired(t *testing.T) {
	c := &listenerFixtureConnection{}
	m := listenerManagerFixture(t, c)
	if err := m.Listen(t.Context(), "svc:app", 80, "127.0.0.1:8000"); err != nil {
		t.Fatal(err)
	}
	m.Fence()
	for i := 0; i < 5; i++ {
		if err := m.Listen(t.Context(), "svc:app", 80, "127.0.0.1:8000"); err == nil {
			t.Fatal("reopened while fenced")
		}
	}
	if c.count() != 1 {
		t.Fatal("fenced poll changed advertisement")
	}
	m.Resume()
	if err := m.Listen(t.Context(), "svc:app", 80, "127.0.0.1:8000"); err != nil {
		t.Fatal(err)
	}
	if c.count() != 2 {
		t.Fatal("listener did not resume")
	}
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	m.CloseWait(ctx)
}

type withdrawalFixture struct {
	tsconnection.Connection
	withdrawOnce sync.Once
	mu           sync.Mutex
	target       string
	withdrawing  chan struct{}
	finish       chan struct{}
}

func (f *withdrawalFixture) ForwardService(_ context.Context, _ string, _ uint16, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.target = target
	return nil
}
func (f *withdrawalFixture) RemoveService(context.Context, string, uint16) error {
	f.withdrawOnce.Do(func() { close(f.withdrawing) })
	<-f.finish
	f.mu.Lock()
	defer f.mu.Unlock()
	f.target = ""
	return nil
}

func TestReconciliationWithdrawalCannotEraseReplacementAdvertisement(t *testing.T) {
	f := &withdrawalFixture{withdrawing: make(chan struct{}), finish: make(chan struct{})}
	m := listenerManagerFixture(t, f)
	if err := m.Listen(t.Context(), "svc:app", 80, "old:80"); err != nil {
		t.Fatal(err)
	}
	retired := make(chan struct{})
	go func() { m.Reconcile(nil); close(retired) }()
	<-f.withdrawing
	replaced := make(chan error, 1)
	go func() { replaced <- m.Listen(t.Context(), "svc:app", 80, "new:80") }()
	// A replacement must wait for the old daemon withdrawal to finish. Otherwise
	// that old RemoveService deletes the newly installed advertisement.
	select {
	case err := <-replaced:
		close(f.finish)
		<-retired
		t.Fatalf("replacement raced unfinished withdrawal: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(f.finish)
	<-retired
	if err := <-replaced; err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.target != "new:80" {
		t.Fatalf("replacement withdrawn: %q", f.target)
	}
}
