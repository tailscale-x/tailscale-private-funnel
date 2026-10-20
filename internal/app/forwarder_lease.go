package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
)

type forwarderLeaseClient interface {
	RenewLease(context.Context) error
	OpenSession(context.Context) (*v1.BrokerSession, error)
}
type assignmentFencer interface {
	Fence()
	Resume()
}

// Lease loss fences advertisements, not the network identity. Keeping the
// process alive avoids turning a temporary broker/API outage into disposable
// device replacement and a new Service-host election.
// Run owns renewal timestamps; mu serializes attaching/fencing listeners.
type forwarderLease struct {
	client                 forwarderLeaseClient
	binding                *v1.BrokerSession
	active                 *atomic.Bool
	mu                     sync.Mutex
	listeners              assignmentFencer
	lastRenew, lastAttempt time.Time
	now                    func() time.Time
}

func newForwarderLease(client forwarderLeaseClient, binding *v1.BrokerSession, active *atomic.Bool, now func() time.Time) *forwarderLease {
	at := now()
	active.Store(true)
	return &forwarderLease{client: client, binding: binding, active: active, lastRenew: at, lastAttempt: at, now: now}
}
func (l *forwarderLease) attach(m assignmentFencer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listeners = m
	if !l.active.Load() {
		m.Fence()
	}
}
func (l *forwarderLease) setActive(active bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if active == l.active.Load() {
		return
	}
	// Never expose an active state before the listener gate has resumed. Never
	// permit a new listener after the active flag has been cleared.
	if !active {
		l.active.Store(false)
		slog.Warn("forwarder assignment fenced; retaining Tailscale identity while broker lease recovers", "controller_id", l.binding.ControllerId, "component_id", l.binding.NodeId, "node_uid", l.binding.NodeUid, "last_renewed", l.lastRenew)
	}
	if l.listeners != nil {
		if active {
			l.listeners.Resume()
		} else {
			l.listeners.Fence()
		}
	}
	if active {
		l.active.Store(true)
		slog.Info("forwarder assignment lease reacquired with unchanged binding and Tailscale identity", "controller_id", l.binding.ControllerId, "component_id", l.binding.NodeId, "node_uid", l.binding.NodeUid)
	}
}
func sameForwarderBinding(a, b *v1.BrokerSession) bool {
	return a != nil && b != nil && a.NodeId == b.NodeId && a.ControllerId == b.ControllerId && a.NodeUid == b.NodeUid && a.NodeName == b.NodeName && a.ManagementUrl == b.ManagementUrl
}
func (l *forwarderLease) step(ctx context.Context) error {
	now := l.now()
	// The server lease is 60 seconds. Fence locally at 40, leaving margin for
	// the bounded five-second request and scheduler delay, as before.
	if now.Sub(l.lastRenew) >= 40*time.Second {
		l.setActive(false)
	}
	interval := 15 * time.Second
	if !l.active.Load() {
		interval = 5 * time.Second
	}
	if now.Sub(l.lastAttempt) < interval {
		return nil
	}
	l.lastAttempt = now
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if l.active.Load() {
		if err := l.client.RenewLease(bounded); err != nil {
			return err
		}
	} else {
		session, err := l.client.OpenSession(bounded)
		if err != nil {
			return err
		}
		if !sameForwarderBinding(l.binding, session) {
			return fmt.Errorf("broker assignment binding changed; forwarding remains fenced")
		}
	}
	l.lastRenew = l.now()
	l.setActive(true)
	return nil
}
func (l *forwarderLease) run(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := l.step(ctx); err != nil {
				slog.Warn("forwarder assignment lease unavailable", "error", err, "fenced", !l.active.Load(), "component_id", l.binding.NodeId, "last_renewed", l.lastRenew)
			}
		}
	}
}
