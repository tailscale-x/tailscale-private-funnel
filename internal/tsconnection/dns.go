package tsconnection

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"tailscale.com/net/netx"
	"tailscale.com/types/dnstype"
)

type dnsAPI interface {
	QueryDNS(context.Context, string, string) ([]byte, []*dnstype.Resolver, error)
}

// Query inside the selected identity. Falling back to the OS resolver can use
// another host identity, which may not be authorized to discover this Service.
func queryTailnetIPs(ctx context.Context, api dnsAPI, name, network string) ([]netip.Addr, error) {
	var ips []netip.Addr
	for _, typ := range []string{"A", "AAAA"} {
		if network == "tcp4" && typ == "AAAA" || network == "tcp6" && typ == "A" {
			continue
		}
		wire, _, err := api.QueryDNS(ctx, name, typ)
		if err != nil {
			continue
		}
		var message dnsmessage.Message
		if message.Unpack(wire) != nil {
			continue
		}
		for _, answer := range message.Answers {
			var ip netip.Addr
			switch v := answer.Body.(type) {
			case *dnsmessage.AResource:
				ip = netip.AddrFrom4(v.A)
			case *dnsmessage.AAAAResource:
				ip = netip.AddrFrom16(v.AAAA)
			default:
				continue
			}
			if !tailnetAddress(ip) {
				return nil, fmt.Errorf("MagicDNS returned a non-Tailscale address; host-network fallback refused")
			}
			ips = append(ips, ip)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("MagicDNS did not resolve %s through the selected Tailscale identity", name)
	}
	return ips, nil
}

// Race both address families without forcing IPv4, IPv6, or another identity.
func dialCandidates(ctx context.Context, port string, ips []netip.Addr, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	if len(ips) == 0 {
		return nil, fmt.Errorf("no Tailscale addresses resolved")
	}
	var addresses []netip.AddrPort
	for _, ip := range ips {
		address, err := netip.ParseAddrPort(net.JoinHostPort(ip.String(), port))
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, address)
	}
	return netx.RaceDial(ctx, addresses, dial, 200*time.Millisecond)
}

func (e *Embedded) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return e.Server.Dial(ctx, network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, parseErr := netip.ParseAddr(host)
	if parseErr == nil {
		if !tailnetAddress(ip) {
			return e.Server.Dial(ctx, network, address)
		}
		return e.dialStack(ctx, network, address)
	}
	status, err := e.Server.Up(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if status.CurrentTailnet == nil || !strings.HasSuffix(name, "."+strings.ToLower(strings.TrimSuffix(status.CurrentTailnet.MagicDNSSuffix, "."))) {
		return e.Server.Dial(ctx, network, address)
	}
	api, err := e.Server.LocalClient()
	if err != nil {
		return nil, err
	}
	ips, err := queryTailnetIPs(ctx, api, name, network)
	if err != nil {
		return nil, err
	}
	// Each candidate commits through the authoritative SDK route plan exactly
	// once. A separate Status peer scan can lag that plan and reject new routes.
	return dialCandidates(ctx, port, ips, e.dialStack)
}
