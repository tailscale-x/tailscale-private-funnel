package gateway

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"tailscale.com/tsnet"
)

// Socket allocation/counting is shared by lifecycle and direct-Docker tests.
// Plain and drainable listeners remain distinct to exercise both SDK contracts.
type listenerFixtureConnection struct {
	tsconnection.Connection
	opens     atomic.Int32
	listener  net.Listener
	drainable bool
	drain     *drainFixtureListener
}

func (c *listenerFixtureConnection) ListenService(string, tsnet.ServiceMode) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	c.opens.Add(1)
	c.listener = ln
	if c.drainable {
		c.drain = &drainFixtureListener{Listener: ln}
		c.listener = c.drain
	}
	return c.listener, nil
}
func (c *listenerFixtureConnection) count() int { return int(c.opens.Load()) }

type drainFixtureListener struct {
	net.Listener
	drained, closed atomic.Bool
}

func (l *drainFixtureListener) Drain(context.Context) error {
	l.drained.Store(true)
	return l.Listener.Close()
}
func (l *drainFixtureListener) Close() error { l.closed.Store(true); return l.Listener.Close() }

func listenerManagerFixture(t *testing.T, c tsconnection.Connection) *ListenerManager {
	t.Helper()
	m := NewListenerManager(c)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		m.CloseWait(ctx)
	})
	return m
}
