//go:build caddy

package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	caddyfilepkg "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
)

// Caddy borrows the application-owned tsnet dialer. Reloading HTTP routes must
// never start a second tsnet.Server against the same persisted identity.
var sharedDialer struct {
	sync.RWMutex
	dial func(context.Context, string, string) (net.Conn, error)
}

// SetDialer binds Caddy's transport to the process-owned Tailscale identity.
// Call before loading configuration; route reloads retain this same callback.
func SetDialer(dial func(context.Context, string, string) (net.Conn, error)) {
	sharedDialer.Lock()
	sharedDialer.dial = dial
	sharedDialer.Unlock()
}

type tailnetTransport struct {
	reverseproxy.HTTPTransport
	BackendRequestTimeout caddy.Duration `json:"backend_request_timeout,omitempty"`
}

func (t *tailnetTransport) UnmarshalCaddyfile(d *caddyfilepkg.Dispenser) error {
	d.Next()
	for d.NextBlock(0) {
		if d.Val() == "backend_request_timeout" {
			if !d.NextArg() {
				return d.ArgErr()
			}
			duration, err := time.ParseDuration(d.Val())
			if err != nil {
				return err
			}
			t.BackendRequestTimeout = caddy.Duration(duration)
			continue
		}
		if d.Val() == "response_header_timeout" {
			if !d.NextArg() {
				return d.ArgErr()
			}
			duration, err := time.ParseDuration(d.Val())
			if err != nil {
				return err
			}
			t.ResponseHeaderTimeout = caddy.Duration(duration)
			continue
		}
		return d.Err("unsupported Tailscale transport option")
	}
	return nil
}

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
	t.Transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		// Replacing Caddy's dialer must preserve its configured connect timeout.
		// A Service first seen before its peer route arrives must not leave an
		// unbounded host-network fallback dial stuck after the route is ready.
		if t.DialTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(t.DialTimeout))
			defer cancel()
		}
		return dial(ctx, network, address)
	}
	return nil
}

func (t *tailnetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.BackendRequestTimeout <= 0 {
		return t.HTTPTransport.RoundTrip(r)
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(t.BackendRequestTimeout))
	response, err := t.HTTPTransport.RoundTrip(r.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	if upgraded, ok := response.Body.(io.ReadWriteCloser); ok {
		response.Body = &cancelDuplexBody{ReadWriteCloser: upgraded, cancel: cancel}
	} else {
		response.Body = &cancelBody{ReadCloser: response.Body, cancel: cancel}
	}
	return response, nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelBody) Close() error { defer b.cancel(); return b.ReadCloser.Close() }

// Upgraded responses must retain Write for Caddy's WebSocket tunnel.
type cancelDuplexBody struct {
	io.ReadWriteCloser
	cancel context.CancelFunc
}

func (b *cancelDuplexBody) Close() error { defer b.cancel(); return b.ReadWriteCloser.Close() }
