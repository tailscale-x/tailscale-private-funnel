// Package tsconnection shares transport without sharing identity ownership.
package tsconnection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
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
type Embedded struct {
	*tsnet.Server
	closeOnce sync.Once
	closeErr  error
	routes    routeObserver
}

func Wrap(s *tsnet.Server) *Embedded { return &Embedded{Server: s} }
func (e *Embedded) HTTPClient() *http.Client {
	client := e.Server.HTTPClient()
	client.Transport.(*http.Transport).DialContext = e.Dial
	return client
}

// Disposable workloads must retire their owned device, rather than leave a
// departed Service host eligible until the coordination server's expiry timer.
// Persistent native identities and borrowed host daemons are never logged out.
func (e *Embedded) Close() error {
	e.closeOnce.Do(func() {
		e.closeErr = closeEmbedded(e.Server.Ephemeral, func(ctx context.Context) error {
			api, err := e.Server.LocalClient()
			if err != nil {
				return err
			}
			return api.Logout(ctx)
		}, e.Server.Close)
	})
	return e.closeErr
}
func closeEmbedded(ephemeral bool, logout func(context.Context) error, closeServer func() error) error {
	var logoutErr error
	if ephemeral {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		logoutErr = logout(ctx)
		cancel()
		if logoutErr != nil {
			slog.Warn("owned ephemeral Tailscale logout failed", "error", logoutErr)
		}
	}
	return errors.Join(logoutErr, closeServer())
}

func (e *Embedded) ListenService(name string, mode tsnet.ServiceMode) (net.Listener, error) {
	return e.listenService(name, mode)
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
