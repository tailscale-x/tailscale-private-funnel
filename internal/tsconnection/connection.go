// Package tsconnection shares transport without sharing identity ownership.
package tsconnection

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
)

var ErrHostControlUnsupported = errors.New("host-reused control is unsupported: tailscaled rejects PROXY v2 with Unix socket targets; use embedded Tailscale for control")

type Connection interface {
	Up(context.Context) (*ipnstate.Status, error)
	LocalClient() (*local.Client, error)
	Dial(context.Context, string, string) (net.Conn, error)
	HTTPClient() *http.Client
	ListenService(string, tsnet.ServiceMode) (net.Listener, error)
	Listen(string, string) (net.Listener, error)
	Close() error
}
type Embedded struct{ *tsnet.Server }

func Wrap(s *tsnet.Server) *Embedded { return &Embedded{s} }
func (e *Embedded) ListenService(name string, mode tsnet.ServiceMode) (net.Listener, error) {
	return e.Server.ListenService(name, mode)
}

// Forwarder publishes a native daemon Service directly, without an extra relay.
type Forwarder interface {
	ForwardService(context.Context, string, uint16, string) error
	RemoveService(context.Context, string, uint16) error
}

func HTTPClient(dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: dial, ForceAttemptHTTP2: true, MaxIdleConns: 100, IdleConnTimeout: 90 * time.Second}}
}
func ValidateMode(mode string) error {
	if mode != "embedded" && mode != "host" {
		return fmt.Errorf("tailscale must be embedded or host")
	}
	return nil
}
