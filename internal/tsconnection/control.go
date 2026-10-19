package tsconnection

import (
	"context"
	"errors"
	"net"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
	"tailscale.com/tsnet"
)

// ListenControl preserves the verified Tailscale source across tsnet Service forwarding.
// Host-daemon control is unavailable until upstream supports a protected peer-preserving path.
func ListenControl(ctx context.Context, c Connection, service string, port uint16) (net.Listener, error) {
	if _, ok := c.(*Host); ok {
		// tailscaled 1.102.2 explicitly rejects PROXY protocol for Unix targets.
		// A plain TCP/HTTP fallback would let local requests forge caller identity.
		return nil, ErrHostControlUnsupported
	}
	ln, err := c.ListenService(service, tsnet.ServiceModeTCP{Port: port, PROXYProtocolVersion: 2})
	if err != nil {
		return nil, err
	}
	// tsnet Services forward through a process-local listener. Require its
	// authenticated transport metadata rather than asking WhoIs about loopback.
	return &proxyproto.Listener{Listener: ln, ReadHeaderTimeout: 5 * time.Second,
		Policy: func(net.Addr) (proxyproto.Policy, error) { return proxyproto.REQUIRE, nil },
		ValidateHeader: func(h *proxyproto.Header) error {
			if h.Version != 2 || h.Command != proxyproto.PROXY {
				return errors.New("tsnet PROXY v2 peer metadata required")
			}
			return nil
		}}, nil
}
