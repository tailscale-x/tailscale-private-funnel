package gateway

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"sync/atomic"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

// LocalPool cannot select a remote-node endpoint, even during backend failures.
type LocalPool struct {
	DialContext func(context.Context, string, string) (net.Conn, error)
	addresses   atomic.Value
	next        atomic.Uint64
}

func (p *LocalPool) Update(endpoints []proxy.PodEndpoint, nodeUID, nodeName string) {
	addresses := []string{}
	seen := map[string]bool{}
	for _, e := range endpoints {
		host, port, err := net.SplitHostPort(e.Address)
		ip, x := netip.ParseAddr(host)
		if err == nil && x == nil && ip.IsGlobalUnicast() && port != "" && e.PodUID != "" && e.NodeUID == nodeUID && e.NodeName == nodeName {
			if !seen[e.Address] {
				addresses = append(addresses, e.Address)
				seen[e.Address] = true
			}
		}
	}
	sort.Strings(addresses)
	p.addresses.Store(addresses)
}
func (p *LocalPool) Dial(ctx context.Context, network, _ string) (net.Conn, error) {
	v := p.addresses.Load()
	if v == nil {
		return nil, fmt.Errorf("no local endpoints")
	}
	a := v.([]string)
	if len(a) == 0 {
		return nil, fmt.Errorf("no local endpoints")
	}
	start := int((p.next.Add(1) - 1) % uint64(len(a)))
	var last error
	for i := 0; i < len(a); i++ {
		d := net.Dialer{Timeout: 3 * time.Second}
		dial := p.DialContext
		if dial == nil {
			dial = d.DialContext
		}
		c, e := dial(ctx, "tcp", a[(start+i)%len(a)])
		if e == nil {
			return c, nil
		}
		last = e
	}
	return nil, last
}
func (p *LocalPool) Ready(ctx context.Context) bool {
	c, e := p.Dial(ctx, "tcp", "")
	if c != nil {
		c.Close()
	}
	return e == nil
}

func (p *LocalPool) Count() int {
	v := p.addresses.Load()
	if v == nil {
		return 0
	}
	return len(v.([]string))
}
