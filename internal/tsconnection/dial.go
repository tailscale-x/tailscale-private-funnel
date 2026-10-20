package tsconnection

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// ErrNoTailnetRoute means the current authenticated map has no route. It is
// not evidence that the application, its hostname claim, or DNS was removed.
var ErrNoTailnetRoute = errors.New("no authenticated Tailscale route")

type dialPlan func(context.Context, string, string) (netip.AddrPort, bool, error)
type stackDial func(context.Context, netip.AddrPort) (net.Conn, error)

// dialTailnet resolves exactly once and commits directly to the existing
// userspace stack. Calling Server.Dial after checking the plan is insufficient:
// a concurrent peer removal could make UserDial fall back to SystemDial.
func dialTailnet(ctx context.Context, network, address string, plan dialPlan, tcp, udp stackDial) (net.Conn, error) {
	var dial stackDial
	switch network {
	case "tcp", "tcp4", "tcp6":
		dial = tcp
	case "udp", "udp4", "udp6":
		dial = udp
	default:
		return nil, fmt.Errorf("unsupported Tailscale network %q", network)
	}
	ipp, via, err := plan(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if !via {
		return nil, fmt.Errorf("%w for %s (%s); host-network fallback refused", ErrNoTailnetRoute, address, ipp)
	}
	if dial == nil {
		return nil, errors.New("Tailscale userspace dialer unavailable")
	}
	return dial(ctx, ipp)
}

func (e *Embedded) dialStack(ctx context.Context, network, address string) (net.Conn, error) {
	// Up is performed by runtime startup. Start also makes this safe for callers
	// that use the transport independently, without opening a second identity.
	if err := e.Server.Start(); err != nil {
		return nil, err
	}
	d := e.Server.Sys().Dialer.Get()
	start := time.Now()
	conn, err := dialTailnet(ctx, network, address, d.UserDialPlan, d.NetstackDialTCP, d.NetstackDialUDP)
	if e.routes.observe(ctx, network, address, time.Since(start), err, time.Now()) {
		e.logRouteMap(address)
	}
	return conn, err
}
