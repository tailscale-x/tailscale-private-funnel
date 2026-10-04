//go:build caddy

package proxy

import (
	"context"
	"fmt"
	"net"
	"sync"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
)

// Caddy borrows the application-owned tsnet dialer. Reloading HTTP routes must
// never start a second tsnet.Server against the same persisted identity.
var sharedDialer struct {
	sync.RWMutex
	dial func(context.Context, string, string) (net.Conn, error)
}

type tailnetTransport struct{ reverseproxy.HTTPTransport }

func init() { caddy.RegisterModule(tailnetTransport{}) }
func (tailnetTransport) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.reverse_proxy.transport.funnel_tsnet", New: func() caddy.Module { return new(tailnetTransport) }}
}
func (t *tailnetTransport) Provision(ctx caddy.Context) error {
	if err := t.HTTPTransport.Provision(ctx); err != nil {
		return err
	}
	sharedDialer.RLock()
	dial := sharedDialer.dial
	sharedDialer.RUnlock()
	if dial == nil {
		return fmt.Errorf("application tsnet dialer is unavailable")
	}
	// Service traffic must never escape via an environment HTTP proxy.
	t.Transport.Proxy = nil
	t.Transport.DialContext = dial
	return nil
}
