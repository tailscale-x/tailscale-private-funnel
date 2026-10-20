package gateway

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"tailscale.com/tsnet"
)

type drainFixtureListener struct {
	net.Listener
	drained, closed atomic.Bool
}

func (l *drainFixtureListener) Drain(context.Context) error {
	l.drained.Store(true)
	return l.Listener.Close()
}
func (l *drainFixtureListener) Close() error { l.closed.Store(true); return l.Listener.Close() }

type drainFixtureConnection struct {
	tsconnection.Connection
	listener *drainFixtureListener
	opens    int
}

func (c *drainFixtureConnection) ListenService(string, tsnet.ServiceMode) (net.Listener, error) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	c.listener = &drainFixtureListener{Listener: ln}
	c.opens++
	return c.listener, nil
}
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
	f := &drainFixtureConnection{}
	m := NewListenerManager(f)
	defer m.Close()
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
	if f.opens != 1 || f.listener.drained.Load() {
		t.Fatal("backend move changed advertisement")
	}
}
func TestGracefulWithdrawalDrainsBeforeFinalCleanup(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &drainFixtureConnection{}
	m := NewListenerManager(f)
	if e := m.Listen(t.Context(), "svc:app", 8080, backend.Addr().String()); e != nil {
		t.Fatal(e)
	}
	c, reader := connectFixture(t, f.listener, "backend")
	m.Remove("svc:app", 8080)
	if !f.listener.drained.Load() || f.listener.closed.Load() {
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
	if !f.listener.closed.Load() {
		t.Fatal("final Serve configuration not cleaned")
	}
}
func TestFenceCancelsStreamsWithoutGrace(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &drainFixtureConnection{}
	m := NewListenerManager(f)
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
func TestLocalFirstPoolFallsBackOnlyToValidatedConfiguredPods(t *testing.T) {
	calls := []string{}
	p := &LocalPool{AllowRemote: true, DialContext: func(_ context.Context, _, a string) (net.Conn, error) {
		calls = append(calls, a)
		if a == "10.1.0.1:80" {
			return nil, fmt.Errorf("local failed")
		}
		c, b := net.Pipe()
		b.Close()
		return c, nil
	}}
	p.Update([]proxy.PodEndpoint{{Address: "10.1.0.1:80", PodUID: "local-pod", NodeUID: "local", NodeName: "here"}, {Address: "10.2.0.2:80", PodUID: "remote-pod", NodeUID: "remote", NodeName: "there"}, {Address: "10.9.0.1:0", PodUID: "bad", NodeUID: "remote", NodeName: "there"}, {Address: "10.9.0.2:80"}}, "local", "here")
	c, e := p.Dial(t.Context(), "tcp", "")
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	if len(calls) != 2 || calls[0] != "10.1.0.1:80" || calls[1] != "10.2.0.2:80" || p.Count() != 1 || p.RemoteCount() != 1 {
		t.Fatal(calls, p.Count(), p.RemoteCount())
	}
	// A replacement snapshot revokes the old Pod address, even when fallback is on.
	p.Update(nil, "local", "here")
	if _, e = p.Dial(t.Context(), "tcp", ""); e == nil {
		t.Fatal("stale endpoint retained")
	}
}

func TestReopenWaitsForDrainingServeCleanup(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &drainFixtureConnection{}
	m := NewListenerManager(f)
	defer m.Close()
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
	if f.opens != 2 {
		t.Fatal("replacement did not open after drain")
	}
}
func TestAuthorizationRemovalCancelsAlreadyDrainingStream(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &drainFixtureConnection{}
	m := NewListenerManager(f)
	defer m.Close()
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
	m := NewListenerManager(h)
	defer m.Close()
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
func TestFirstAdvertisementRequiresReachableLocalBackend(t *testing.T) {
	p := &LocalPool{AllowRemote: true, DialContext: func(_ context.Context, _, a string) (net.Conn, error) {
		if a == "10.1.0.1:80" {
			return nil, fmt.Errorf("local failed")
		}
		c, b := net.Pipe()
		b.Close()
		return c, nil
	}}
	p.Update([]proxy.PodEndpoint{{Address: "10.1.0.1:80", PodUID: "local", NodeUID: "local", NodeName: "here"}, {Address: "10.2.0.2:80", PodUID: "remote", NodeUID: "remote", NodeName: "there"}}, "local", "here")
	if p.ReadyLocal(t.Context()) {
		t.Fatal("remote reachability admitted a new Service host")
	}
	if !p.Ready(t.Context()) {
		t.Fatal("established host cannot use authorized remote fallback")
	}
}

func TestCloseWaitDeadlineAbortsStalledStream(t *testing.T) {
	backend := echoBackend(t, "backend")
	f := &drainFixtureConnection{}
	m := NewListenerManager(f)
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
