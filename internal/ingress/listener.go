package ingress

import (
	"context"
	"fmt"
	"net"
	"sync"

	"tailscale.com/tsnet"
)

// ListenerManager keeps all Service listeners on the ingress node's one
// tsnet identity. Relays are ordinary loopback TCP targets and never join the
// tailnet themselves.
type ListenerManager struct {
	server *tsnet.Server
	mu     sync.Mutex
	items  map[string]listenerItem
}

type listenerItem struct {
	listener net.Listener
	cancel   context.CancelFunc
	target   string
}

func NewListenerManager(server *tsnet.Server) *ListenerManager {
	return &ListenerManager{server: server, items: make(map[string]listenerItem)}
}

func (m *ListenerManager) Listen(ctx context.Context, service string, port uint16, target string) error {
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
		_ = existing.listener.Close()
		delete(m.items, key)
	}
	listener, err := m.server.ListenService(service, tsnet.ServiceModeTCP{Port: port})
	if err != nil {
		m.mu.Unlock()
		return err
	}
	listenCtx, cancel := context.WithCancel(ctx)
	m.items[key] = listenerItem{listener: listener, cancel: cancel, target: target}
	m.mu.Unlock()
	go func() {
		_ = Forward(listenCtx, listener, target)
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
		_ = item.listener.Close()
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
		_ = item.listener.Close()
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
		_ = item.listener.Close()
	}
}
