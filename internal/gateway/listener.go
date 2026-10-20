package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"tailscale.com/tsnet"
)

// ListenerManager keeps Service advertisements independent of backend address
// changes. A new TCP connection uses the current target; existing streams keep
// the backend they connected to until completion or the bounded drain deadline.
type ListenerManager struct {
	server         tsconnection.Connection
	mu             sync.Mutex
	draining       sync.WaitGroup
	items          map[string]*listenerItem
	retiring       map[string]*listenerItem
	fenced, closed bool
}
type listenerTarget struct {
	target string
	dial   func(context.Context, string, string) (net.Conn, error)
}
type listenerItem struct {
	listener net.Listener
	cancel   context.CancelFunc
	target   atomic.Pointer[listenerTarget]
	done     chan struct{}
}

func NewListenerManager(server tsconnection.Connection) *ListenerManager {
	return &ListenerManager{server: server, items: make(map[string]*listenerItem), retiring: make(map[string]*listenerItem)}
}
func (m *ListenerManager) Has(service string, port uint16) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.items[fmt.Sprintf("%s:%d", service, port)] != nil
}
func (m *ListenerManager) Listen(ctx context.Context, service string, port uint16, target string) error {
	d := net.Dialer{Timeout: 5 * time.Second}
	return m.ListenDial(ctx, service, port, target, d.DialContext)
}
func (m *ListenerManager) ListenDial(ctx context.Context, service string, port uint16, target string, dial func(context.Context, string, string) (net.Conn, error)) error {
	if m == nil || m.server == nil || service == "" || port == 0 || target == "" || dial == nil {
		return fmt.Errorf("service listener requires server, service, port, target, and dialer")
	}
	key := fmt.Sprintf("%s:%d", service, port)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		if m.fenced || m.closed {
			m.mu.Unlock()
			return fmt.Errorf("Service assignment is fenced or stopped; broker lease reacquisition required")
		}
		// Delayed final Serve cleanup must never erase a newly opened advertisement.
		if old := m.retiring[key]; old != nil {
			m.mu.Unlock()
			select {
			case <-old.done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		break
	}
	defer m.mu.Unlock()
	underlying := m.server
	if bound, ok := underlying.(*tsconnection.Bound); ok {
		underlying = bound.Connection
	}
	next := &listenerTarget{target: target, dial: dial}
	if host, ok := underlying.(tsconnection.Forwarder); ok {
		if existing := m.items[key]; existing != nil && existing.target.Load().target == target {
			return nil
		}
		// ForwardService updates its exact owned Serve entry atomically. Withdrawing
		// it first would create an avoidable Service-route gap.
		if err := host.ForwardService(ctx, service, port, target); err != nil {
			return err
		}
		item := &listenerItem{cancel: func() {
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := host.RemoveService(bounded, service, port); err != nil {
				slog.Warn("owned host Service withdrawal failed", "service", service, "error", err)
			}
		}}
		item.target.Store(next)
		m.items[key] = item
		slog.Info("tailnet Service listener configured", "service", service, "port", port, "backend", target, "transport", "host")
		return nil
	}
	if existing := m.items[key]; existing != nil {
		previous := existing.target.Swap(next)
		if previous.target != target {
			slog.Info("tailnet Service backend updated without advertisement change", "service", service, "port", port, "backend", target)
		}
		return nil
	}
	listener, err := m.server.ListenService(service, tsnet.ServiceModeTCP{Port: port})
	if err != nil {
		return err
	}
	// Manager Close/Remove quiesce explicitly. Root cancellation must not bypass
	// stream draining by calling ServiceListener.Close in an AfterFunc.
	listenCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	item := &listenerItem{listener: listener, cancel: cancel, done: make(chan struct{})}
	item.target.Store(next)
	m.items[key] = item
	slog.Info("tailnet Service listener opened", "service", service, "port", port, "backend", target, "transport", "embedded")
	m.draining.Add(1)
	go func() {
		defer m.draining.Done()
		defer cancel()
		_ = ForwardDial(listenCtx, listener, key, func(ctx context.Context, network, _ string) (net.Conn, error) {
			v := item.target.Load()
			return v.dial(ctx, network, v.target)
		})
		// ForwardDial has now drained or canceled all streams. Only now remove the
		// SDK's Serve configuration and internal advertisement reference.
		if err := listener.Close(); err != nil && !onlyListenerClosed(err) {
			slog.Warn("tailnet Service final cleanup failed", "assignment", key, "error", err)
		}
		m.mu.Lock()
		if m.items[key] == item {
			delete(m.items, key)
		}
		delete(m.retiring, key)
		close(item.done)
		m.mu.Unlock()
	}()
	return nil
}
func (m *ListenerManager) Remove(service string, port uint16) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(fmt.Sprintf("%s:%d", service, port), "runtime_remove", false)
}

func (m *ListenerManager) removeLocked(key, reason string, hard bool) {
	item := m.items[key]
	if item == nil {
		return
	}
	delete(m.items, key)
	slog.Info("tailnet Service listener withdrawing", "assignment", key, "backend", item.target.Load().target, "reason", reason, "immediate", hard)
	if item.listener == nil {
		item.cancel()
		return
	}
	m.retiring[key] = item
	if !hard {
		if ln, ok := item.listener.(tsconnection.DrainableListener); ok {
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			err := ln.Drain(ctx)
			stop()
			if err == nil {
				return
			}
			slog.Warn("tailnet Service drain failed; closing owned listener", "assignment", key, "error", err)
		} else {
			// Ordinary net.Listeners do not own Serve configuration.
			_ = item.listener.Close()
			return
		}
	}
	// Authorization/lease loss must fence immediately, without a grace period.
	item.cancel()
	_ = item.listener.Close()
}
func (m *ListenerManager) Close()  { m.stop(false) }
func (m *ListenerManager) Fence()  { m.stop(true) }
func (m *ListenerManager) Resume() { m.mu.Lock(); defer m.mu.Unlock(); m.fenced = false }
func (m *ListenerManager) stop(fence bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	reason := "shutdown"
	if fence {
		m.fenced = true
		reason = "lease_fenced"
	} else {
		m.closed = true
	}
	for key := range m.items {
		m.removeLocked(key, reason, fence)
	}
	if fence {
		for _, item := range m.retiring {
			item.cancel()
		}
	}
}
func (m *ListenerManager) Reconcile(active map[string]struct{}) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.items {
		if _, ok := active[key]; !ok {
			m.removeLocked(key, "configuration_withdrawal", true)
		}
	}
	for key, item := range m.retiring {
		if _, ok := active[key]; !ok {
			item.cancel()
		}
	}
}
func (m *ListenerManager) CloseWait(ctx context.Context) {
	m.Close()
	done := make(chan struct{})
	go func() { m.draining.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		m.mu.Lock()
		for _, item := range m.retiring {
			item.cancel()
		}
		m.mu.Unlock()
	}
}

// SDK cleanup joins acceptor-close and configuration errors. Suppress an
// expected repeated acceptor close, never a joined advertisement/config failure.
func onlyListenerClosed(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !onlyListenerClosed(child) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, net.ErrClosed)
}
