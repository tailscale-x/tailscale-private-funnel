package tsconnection

import (
	"context"
	"net"
	"net/http"
	"sync"
)

// RPC wrappers are short lived; the network identity owns their connection pool.
// Returning client copies keeps per-caller timeouts and redirect policy separate
// without leaving a new live transport (and idle socket) behind each poll.
type httpClientPool struct {
	mu             sync.Mutex
	clientTemplate *http.Client
}

func (p *httpClientPool) client(dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.clientTemplate == nil {
		p.clientTemplate = HTTPClient(dial)
	}
	copy := *p.clientTemplate
	return &copy
}
func (p *httpClientPool) closeIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.clientTemplate != nil {
		p.clientTemplate.CloseIdleConnections()
	}
}
