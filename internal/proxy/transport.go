//go:build caddy

package proxy

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/caddyserver/caddy/v2"
	caddyfilepkg "github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
)

// Caddy borrows the application-owned tsnet dialer. Reloading HTTP routes must
// never start a second tsnet.Server against the same persisted identity.
var sharedDialer struct {
	sync.RWMutex
	dial    func(context.Context, string, string) (net.Conn, error)
	resolve func(context.Context, string, uint16) (string, error)
}

type tailnetTransport struct {
	reverseproxy.HTTPTransport
	BackendDeviceID string `json:"backend_device_id,omitempty"`
}

// SetPeerResolver supplies identity-bound addresses from the process-owned tsnet
// state. It is called before Caddy starts and remains stable across reloads.
func SetPeerResolver(resolve func(context.Context, string, uint16) (string, error)) {
	sharedDialer.Lock()
	sharedDialer.resolve = resolve
	sharedDialer.Unlock()
}

func (t *tailnetTransport) UnmarshalCaddyfile(d *caddyfilepkg.Dispenser) error {
	d.Next()
	for d.NextBlock(0) {
		if d.Val() != "device" || !d.NextArg() || t.BackendDeviceID != "" {
			return d.Err("expected one device ID")
		}
		t.BackendDeviceID = d.Val()
		if d.NextArg() {
			return d.ArgErr()
		}
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
	resolve := sharedDialer.resolve
	sharedDialer.RUnlock()
	if dial == nil {
		return fmt.Errorf("application tsnet dialer is unavailable")
	}
	// Service traffic must never escape via an environment HTTP proxy.
	t.Transport.Proxy = nil
	if t.BackendDeviceID != "" {
		if resolve == nil {
			return fmt.Errorf("tailnet peer resolver is unavailable")
		}
		deviceID := t.BackendDeviceID
		t.Transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			_, portText, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			port, err := strconv.ParseUint(portText, 10, 16)
			if err != nil || port == 0 {
				return nil, fmt.Errorf("invalid direct backend port")
			}
			target, err := resolve(ctx, deviceID, uint16(port))
			if err != nil {
				return nil, err
			}
			return dial(ctx, "tcp", target)
		}
	} else {
		t.Transport.DialContext = dial
	}
	return nil
}
