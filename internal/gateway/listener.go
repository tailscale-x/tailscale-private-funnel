package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"
	"tailscale.com/tsnet"
)

// ListenerManager keeps all Service listeners on the gateway node's one
// tsnet identity and forwards directly to selected backend addresses.
type ListenerManager struct {
	server   tsconnection.Connection
	mu       sync.Mutex
	draining sync.WaitGroup
	items    map[string]listenerItem
	fenced   bool
}

type listenerItem struct {
	listener net.Listener
	cancel   context.CancelFunc
	target   string
}

func NewListenerManager(server tsconnection.Connection) *ListenerManager {
	return &ListenerManager{server: server, items: make(map[string]listenerItem)}
}

func (m *ListenerManager) Listen(ctx context.Context, service string, port uint16, target string) error {
	d := net.Dialer{}
	return m.ListenDial(ctx, service, port, target, d.DialContext)
}
func (m *ListenerManager) ListenDial(ctx context.Context, service string, port uint16, target string, dial func(context.Context, string, string) (net.Conn, error)) error {
	if m == nil || m.server == nil || service == "" || port == 0 || target == "" {
		return fmt.Errorf("service listener requires server, service, port, and target")
	}
	key := fmt.Sprintf("%s:%d", service, port)
	m.mu.Lock()
	if m.fenced {
		m.mu.Unlock()
		return fmt.Errorf("Service assignment is fenced; broker lease reacquisition required")
	}
	if existing, ok := m.items[key]; ok && existing.target == target {
		m.mu.Unlock()
		return nil
	}
	m.removeLocked(key, "backend_changed")
	underlying := m.server
	if bound, ok := underlying.(*tsconnection.Bound); ok {
		underlying = bound.Connection
	}
	if host, ok := underlying.(tsconnection.Forwarder); ok {
		if err := host.ForwardService(ctx, service, port, target); err != nil {
			m.mu.Unlock()
			return err
		}
		slog.Info("tailnet Service listener opened", "service", service, "port", port, "backend", target, "transport", "host")
		m.items[key] = listenerItem{target: target, cancel: func() {
			bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_ = host.RemoveService(bounded, service, port)
		}}
		m.mu.Unlock()
		return nil
	}
	listener, err := m.server.ListenService(service, tsnet.ServiceModeTCP{Port: port})
	if err != nil {
		m.mu.Unlock()
		return err
	}
	slog.Info("tailnet Service listener opened", "service", service, "port", port, "backend", target, "transport", "embedded")
	listenCtx, cancel := context.WithCancel(ctx)
	m.items[key] = listenerItem{listener: listener, cancel: cancel, target: target}
	m.draining.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.draining.Done()
		_ = ForwardDial(listenCtx, listener, target, dial)
		m.mu.Lock()
		if current, ok := m.items[key]; ok && current.listener == listener {
			delete(m.items, key)
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *ListenerManager) Remove(service string, port uint16) {
	if m == nil {
		return
	}
	key := fmt.Sprintf("%s:%d", service, port)
	m.mu.Lock()
	m.removeLocked(key, "runtime_remove")
	m.mu.Unlock()
}

// removeLocked serializes every withdrawal with replacement, including
// reconciliation. Otherwise a delayed RemoveService can erase a new host Serve
// entry for the same Service/port. Call only while holding m.mu.
func (m *ListenerManager) removeLocked(key, reason string) {
	item, ok := m.items[key]
	if !ok {
		return
	}
	delete(m.items, key)
	slog.Info("tailnet Service listener withdrawn", "assignment", key, "backend", item.target, "reason", reason)
	item.cancel()
	if item.listener != nil {
		_ = item.listener.Close()
	}
}

func (m *ListenerManager) Close() {
	m.stop(false)
}

// Fence closes listeners and prevents a concurrent reconciliation from opening
// them again. It does not close or replace the process-owned Tailscale identity.
func (m *ListenerManager) Fence() { m.stop(true) }

func (m *ListenerManager) Resume() {
	m.mu.Lock()
	m.fenced = false
	m.mu.Unlock()
}

func (m *ListenerManager) stop(fence bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if fence {
		m.fenced = true
	}
	reason := "shutdown"
	if fence {
		reason = "lease_fenced"
	}
	for key := range m.items {
		m.removeLocked(key, reason)
	}
	m.mu.Unlock()
}

func (m *ListenerManager) Reconcile(active map[string]struct{}) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key := range m.items {
		if _, ok := active[key]; !ok {
			m.removeLocked(key, "configuration_withdrawal")
		}
	}
}

// CloseWait stops new connections and waits for existing TCP streams to drain.
func (m *ListenerManager) CloseWait(ctx context.Context) {
	m.Close()
	done := make(chan struct{})
	go func() { m.draining.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
