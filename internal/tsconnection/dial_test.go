package tsconnection

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

func TestDialTailnetMissingRouteNeverCallsStack(t *testing.T) {
	for _, address := range []string{"100.112.216.176:80", "[fd7a:115c:a1e0::936:d8b1]:80", "service.example.ts.net:80"} {
		t.Run(address, func(t *testing.T) {
			called := false
			plan := func(context.Context, string, string) (netip.AddrPort, bool, error) {
				return netip.MustParseAddrPort("100.112.216.176:80"), false, nil
			}
			stack := func(context.Context, netip.AddrPort) (net.Conn, error) { called = true; return nil, nil }
			conn, err := dialTailnet(context.Background(), "tcp", address, plan, stack, stack)
			if conn != nil || !errors.Is(err, ErrNoTailnetRoute) || called {
				t.Fatalf("conn=%v err=%v called=%v", conn, err, called)
			}
		})
	}
}

func TestDialTailnetPinsResolvedAddressAcrossRouteRemoval(t *testing.T) {
	for _, network := range []string{"tcp", "tcp4", "tcp6", "udp", "udp4", "udp6"} {
		t.Run(network, func(t *testing.T) {
			target := netip.MustParseAddrPort("[fd7a:115c:a1e0::936:d8b1]:80")
			plans, calls := 0, 0
			plan := func(_ context.Context, n, a string) (netip.AddrPort, bool, error) {
				plans++
				if n != network || a != "harbor.example.ts.net:80" {
					t.Fatal(n, a)
				}
				return target, true, nil
			}
			removed := errors.New("peer removed after planning")
			stack := func(_ context.Context, ip netip.AddrPort) (net.Conn, error) {
				calls++
				if ip != target {
					t.Fatal(ip)
				}
				return nil, removed
			}
			_, err := dialTailnet(context.Background(), network, "harbor.example.ts.net:80", plan, stack, stack)
			if !errors.Is(err, removed) || plans != 1 || calls != 1 {
				t.Fatalf("err=%v plans=%d calls=%d", err, plans, calls)
			}
		})
	}
}

func TestDialTailnetForwardsStreamConnection(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	plan := func(context.Context, string, string) (netip.AddrPort, bool, error) {
		return netip.MustParseAddrPort("100.112.216.176:80"), true, nil
	}
	stack := func(context.Context, netip.AddrPort) (net.Conn, error) { return a, nil }
	got, err := dialTailnet(context.Background(), "tcp", "service:80", plan, stack, nil)
	if err != nil || got != a {
		t.Fatalf("connection wrapped or lost: %v", err)
	}
}

func TestDialTailnetResolutionAndNetworkErrors(t *testing.T) {
	sentinel := errors.New("resolution failed")
	plan := func(context.Context, string, string) (netip.AddrPort, bool, error) {
		return netip.AddrPort{}, false, sentinel
	}
	if _, err := dialTailnet(context.Background(), "tcp", "invalid", plan, nil, nil); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := dialTailnet(context.Background(), "unix", "invalid", plan, nil, nil); err == nil || errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}
