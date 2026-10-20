package tsconnection

import (
	"context"
	"errors"
	"golang.org/x/net/dns/dnsmessage"
	"net"
	"net/netip"
	"tailscale.com/types/dnstype"
	"testing"
	"time"
)

type fixtureDNS struct{ ip netip.Addr }

func (f fixtureDNS) QueryDNS(_ context.Context, name, kind string) ([]byte, []*dnstype.Resolver, error) {
	msg := dnsmessage.Message{Header: dnsmessage.Header{Response: true}}
	if kind == "A" {
		msg.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(name + "."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: f.ip.As4()}}}
	}
	b, e := msg.Pack()
	return b, nil, e
}
func TestTailnetDNSUsesSelectedIdentityAndRejectsPublicAnswer(t *testing.T) {
	ips, e := queryTailnetIPs(t.Context(), fixtureDNS{netip.MustParseAddr("100.64.0.4")}, "svc.example.ts.net", "tcp")
	if e != nil || len(ips) != 1 || ips[0].String() != "100.64.0.4" {
		t.Fatal(ips, e)
	}
	if _, e = queryTailnetIPs(t.Context(), fixtureDNS{netip.MustParseAddr("203.0.113.4")}, "svc.example.ts.net", "tcp"); e == nil {
		t.Fatal("public address accepted")
	}
	if _, e = queryTailnetIPs(t.Context(), fixtureDNS{netip.MustParseAddr("100.64.0.4")}, "svc.example.ts.net", "tcp6"); e == nil {
		t.Fatal("IPv4 forced into IPv6 request")
	}
}
func TestTailnetDialRacesFamiliesAndCancelsLoser(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	loser := make(chan struct{})
	peer, remote := net.Pipe()
	defer remote.Close()
	c, e := dialCandidates(ctx, "8080", []netip.Addr{netip.MustParseAddr("100.64.0.4"), netip.MustParseAddr("fd7a:115c:a1e0::4")}, func(ctx context.Context, _ string, address string) (net.Conn, error) {
		if address == "[fd7a:115c:a1e0::4]:8080" {
			<-ctx.Done()
			close(loser)
			return nil, ctx.Err()
		}
		return peer, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	select {
	case <-loser:
	case <-time.After(time.Second):
		t.Fatal("losing dial leaked")
	}
	if _, e = dialCandidates(ctx, "8080", []netip.Addr{netip.MustParseAddr("100.64.0.4")}, func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("refused") }); e == nil {
		t.Fatal("failure ignored")
	}
}
func TestExplicitHostSocketDisablesMacGUIFallback(t *testing.T) {
	if !NewHost("/tmp/explicit.sock", t.TempDir()).Client.UseSocketOnly {
		t.Fatal("explicit daemon can be ignored")
	}
	if NewHost("", t.TempDir()).Client.UseSocketOnly {
		t.Fatal("Mac GUI discovery disabled")
	}
}

// DNS can resolve before the SDK map receives a Service route. Retry the live
// plan without restarting the connection or consulting a second peer snapshot.
func TestResolvedServiceUsesCurrentPlanWithoutHostFallback(t *testing.T) {
	ip := netip.MustParseAddr("100.64.0.9")
	routed := false
	plans, stacks := 0, 0
	sentinel := errors.New("netstack reached")
	plan := func(_ context.Context, _, address string) (netip.AddrPort, bool, error) {
		plans++
		target, err := netip.ParseAddrPort(address)
		return target, routed, err
	}
	stack := func(context.Context, netip.AddrPort) (net.Conn, error) { stacks++; return nil, sentinel }
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialTailnet(ctx, network, address, plan, stack, stack)
	}
	for _, ready := range []bool{false, true, false} {
		routed = ready
		_, err := dialCandidates(t.Context(), "8080", []netip.Addr{ip}, dial)
		if ready && !errors.Is(err, sentinel) || !ready && !errors.Is(err, ErrNoTailnetRoute) {
			t.Fatalf("routed=%v: %v", ready, err)
		}
	}
	if plans != 3 || stacks != 1 {
		t.Fatalf("plans=%d stack calls=%d", plans, stacks)
	}
}
