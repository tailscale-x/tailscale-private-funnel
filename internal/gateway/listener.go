package gateway

import (
	"context"
	"fmt"
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
	if existing, ok := m.items[key]; ok && existing.target == target {
		m.mu.Unlock()
		return nil
	}
	if existing, ok := m.items[key]; ok {
		existing.cancel()
		if existing.listener != nil {
			_ = existing.listener.Close()
		}
		delete(m.items, key)
	}
	underlying := m.server
	if bound, ok := underlying.(*tsconnection.Bound); ok {
		underlying = bound.Connection
	}
	if host, ok := underlying.(tsconnection.Forwarder); ok {
		if err := host.ForwardService(ctx, service, port, target); err != nil {
			m.mu.Unlock()
			return err
		}
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
	item, ok := m.items[key]
	if ok {
		delete(m.items, key)
		item.cancel()
		if item.listener != nil {
			_ = item.listener.Close()
		}
	}
	m.mu.Unlock()
}

func (m *ListenerManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	items := m.items
	m.items = make(map[string]listenerItem)
	m.mu.Unlock()
	for _, item := range items {
		item.cancel()
		if item.listener != nil {
			_ = item.listener.Close()
		}
	}
}

func (m *ListenerManager) Reconcile(active map[string]struct{}) {
	if m == nil {
		return
	}
	m.mu.Lock()
	var stale []listenerItem
	for key, item := range m.items {
		if _, ok := active[key]; !ok {
			delete(m.items, key)
			stale = append(stale, item)
		}
	}
	m.mu.Unlock()
	for _, item := range stale {
		item.cancel()
		if item.listener != nil {
			_ = item.listener.Close()
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
