package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
)

func echoBackend(t *testing.T, name string) net.Listener {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); fmt.Fprintln(c, name); io.Copy(c, c) }()
		}
	}()
	return ln
}
func connectFixture(t *testing.T, ln net.Listener, want string) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, e := net.Dial("tcp", ln.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	reader := bufio.NewReader(c)
	s, e := reader.ReadString('\n')
	if e != nil || s != want+"\n" {
		t.Fatalf("got %q %v", s, e)
	}
	return c, reader
}
func TestBackendMoveKeepsAdvertisementAndEstablishedStream(t *testing.T) {
	a, b := echoBackend(t, "old"), echoBackend(t, "new")
	f := &listenerFixtureConnection{drainable: true}
	m := listenerManagerFixture(t, f)
	if e := m.Listen(t.Context(), "svc:app", 8080, a.Addr().String()); e != nil {
		t.Fatal(e)
	}
	first, reader := connectFixture(t, f.listener, "old")
	defer first.Close()
	if e := m.Listen(t.Context(), "svc:app", 8080, b.Addr().String()); e != nil {
		t.Fatal(e)
	}
	second, _ := connectFixture(t, f.listener, "new")
	second.Close()
	fmt.Fprintln(first, "still-old-stream")
	s, e := reader.ReadString('\n')
	if e != nil || s != "still-old-stream\n" {
		t.Fatal(s, e)
	}
	if f.count() != 1 || f.drain.drained.Load() {
		t.Fatal("backend move changed advertisement")
	}
}
func TestGracefulWithdrawalDrainsBeforeFinalCleanup(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &listenerFixtureConnection{drainable: true}
	m := listenerManagerFixture(t, f)
	if e := m.Listen(t.Context(), "svc:app", 8080, backend.Addr().String()); e != nil {
		t.Fatal(e)
	}
	c, reader := connectFixture(t, f.listener, "backend")
	m.Remove("svc:app", 8080)
	if !f.drain.drained.Load() || f.drain.closed.Load() {
		t.Fatal("Serve configuration removed before streams drained")
	}
	fmt.Fprintln(c, "after-drain")
	s, e := reader.ReadString('\n')
	if e != nil || s != "after-drain\n" {
		t.Fatal("drain truncated stream", s, e)
	}
	if n, e := net.DialTimeout("tcp", f.listener.Addr().String(), 100*time.Millisecond); e == nil {
		n.Close()
		t.Fatal("draining listener accepted a new connection")
	}
	c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	m.CloseWait(ctx)
	if !f.drain.closed.Load() {
		t.Fatal("final Serve configuration not cleaned")
	}
}
func TestFenceCancelsStreamsWithoutGrace(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &listenerFixtureConnection{drainable: true}
	m := listenerManagerFixture(t, f)
	if e := m.Listen(t.Context(), "svc:app", 8080, backend.Addr().String()); e != nil {
		t.Fatal(e)
	}
	c, r := connectFixture(t, f.listener, "backend")
	defer c.Close()
	m.Fence()
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := r.ReadByte(); e == nil {
		t.Fatal("fenced stream remained open")
	} else if n, ok := e.(net.Error); ok && n.Timeout() {
		t.Fatal("fence waited for graceful drain")
	}
}

func TestReopenWaitsForDrainingServeCleanup(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &listenerFixtureConnection{drainable: true}
	m := listenerManagerFixture(t, f)
	if e := m.Listen(t.Context(), "svc:app", 8080, backend.Addr().String()); e != nil {
		t.Fatal(e)
	}
	c, _ := connectFixture(t, f.listener, "backend")
	m.Remove("svc:app", 8080)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if e := m.Listen(ctx, "svc:app", 8080, backend.Addr().String()); e == nil {
		t.Fatal("replacement raced pending Serve cleanup")
	}
	c.Close()
	if e := m.Listen(t.Context(), "svc:app", 8080, backend.Addr().String()); e != nil {
		t.Fatal(e)
	}
	if f.count() != 2 {
		t.Fatal("replacement did not open after drain")
	}
}
func TestAuthorizationRemovalCancelsAlreadyDrainingStream(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &listenerFixtureConnection{drainable: true}
	m := listenerManagerFixture(t, f)
	if e := m.Listen(t.Context(), "svc:app", 8080, backend.Addr().String()); e != nil {
		t.Fatal(e)
	}
	c, r := connectFixture(t, f.listener, "backend")
	defer c.Close()
	m.Remove("svc:app", 8080)
	m.Reconcile(nil)
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := r.ReadByte(); e == nil {
		t.Fatal("revoked draining stream remained open")
	} else if n, ok := e.(net.Error); ok && n.Timeout() {
		t.Fatal("revocation waited for drain")
	}
}

type hostUpdateFixture struct {
	tsconnection.Connection
	target  string
	removes int
	fail    bool
}

func (h *hostUpdateFixture) ForwardService(_ context.Context, _ string, _ uint16, target string) error {
	if h.fail {
		return fmt.Errorf("update failed")
	}
	h.target = target
	return nil
}
func (h *hostUpdateFixture) RemoveService(context.Context, string, uint16) error {
	h.removes++
	return nil
}
func TestHostBackendUpdatePreservesAdvertisementAndOldTargetOnFailure(t *testing.T) {
	h := &hostUpdateFixture{}
	m := listenerManagerFixture(t, h)
	if e := m.Listen(t.Context(), "svc:app", 80, "old:80"); e != nil {
		t.Fatal(e)
	}
	if e := m.Listen(t.Context(), "svc:app", 80, "new:80"); e != nil {
		t.Fatal(e)
	}
	if h.removes != 0 || h.target != "new:80" {
		t.Fatal("host update withdrew advertisement", h.removes, h.target)
	}
	h.fail = true
	if e := m.Listen(t.Context(), "svc:app", 80, "bad:80"); e == nil {
		t.Fatal("failed update accepted")
	}
	if !m.Has("svc:app", 80) || h.target != "new:80" || h.removes != 0 {
		t.Fatal("failed update destroyed current route")
	}
}
func TestCleanupErrorFilteringDoesNotHideAdvertisementFailure(t *testing.T) {
	if !onlyListenerClosed(net.ErrClosed) {
		t.Fatal("expected close not recognized")
	}
	if onlyListenerClosed(errors.Join(net.ErrClosed, fmt.Errorf("Serve cleanup failed"))) {
		t.Fatal("configuration failure hidden by repeated close")
	}
}

func TestCloseWaitDeadlineAbortsStalledStream(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &listenerFixtureConnection{drainable: true}
	m := listenerManagerFixture(t, f)
	if e := m.Listen(t.Context(), "svc:app", 8080, backend.Addr().String()); e != nil {
		t.Fatal(e)
	}
	c, r := connectFixture(t, f.listener, "backend")
	defer c.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	m.CloseWait(ctx)
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := r.ReadByte(); e == nil {
		t.Fatal("expired drain left stream open")
	} else if n, ok := e.(net.Error); ok && n.Timeout() {
		t.Fatal("expired drain leaked forwarding")
	}
}
