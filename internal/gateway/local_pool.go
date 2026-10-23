package gateway

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

// LocalPool uses validated ready Pod endpoints, preferring the local node.
// Remote fallback is opt-in and intended to retain an existing advertisement
// while an application moves; it never discovers arbitrary network addresses.
type LocalPool struct {
	DialContext func(context.Context, string, string) (net.Conn, error)
	AllowRemote bool
	addresses   atomic.Value
	next        atomic.Uint64
}

func (p *LocalPool) Update(endpoints []proxy.PodEndpoint, nodeUID, nodeName string) {
	addresses := poolAddresses{}
	seen := map[string]bool{}
	for _, e := range endpoints {
		host, port, err := net.SplitHostPort(e.Address)
		ip, x := netip.ParseAddr(host)
		if err == nil && x == nil && ip.IsGlobalUnicast() && validEndpointPort(port) && ((e.NodeUID == nodeUID) == (e.NodeName == nodeName)) && e.PodUID != "" && e.NodeUID != "" && e.NodeName != "" {
			if !seen[e.Address] {
				if e.NodeUID == nodeUID && e.NodeName == nodeName {
					addresses.local = append(addresses.local, e.Address)
				} else if p.AllowRemote {
					addresses.remote = append(addresses.remote, e.Address)
				}
				seen[e.Address] = true
			}
		}
	}
	sort.Strings(addresses.local)
	sort.Strings(addresses.remote)
	p.addresses.Store(addresses)
}
func (p *LocalPool) Dial(ctx context.Context, network, _ string) (net.Conn, error) {
	return p.dial(ctx, false)
}
func (p *LocalPool) dial(ctx context.Context, localOnly bool) (net.Conn, error) {
	v := p.addresses.Load()
	if v == nil {
		return nil, fmt.Errorf("no validated ready endpoints")
	}
	a := v.(poolAddresses)
	if localOnly {
		a.remote = nil
	}
	if len(a.local)+len(a.remote) == 0 {
		return nil, fmt.Errorf("no validated ready endpoints")
	}
	sequence := p.next.Add(1) - 1
	var last error
	for _, tier := range [][]string{a.local, a.remote} {
		if len(tier) == 0 {
			continue
		}
		start := int(sequence % uint64(len(tier)))
		for i := 0; i < len(tier); i++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			d := net.Dialer{Timeout: time.Second}
			dial := p.DialContext
			if dial == nil {
				dial = d.DialContext
			}
			c, e := dial(ctx, "tcp", tier[(start+i)%len(tier)])
			if e == nil {
				return c, nil
			}
			last = e
		}
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
	return len(v.(poolAddresses).local)
}

// RemoteCount is diagnostic; Count retains its node-local meaning.
func (p *LocalPool) RemoteCount() int {
	v := p.addresses.Load()
	if v == nil {
		return 0
	}
	return len(v.(poolAddresses).remote)
}

type poolAddresses struct{ local, remote []string }

func validEndpointPort(raw string) bool {
	port, err := strconv.ParseUint(raw, 10, 16)
	return err == nil && port > 0
}

// ReadyLocal is required before a new host's first advertisement. Remote
// fallback keeps established hosts available; it never enrolls an empty node.
func (p *LocalPool) ReadyLocal(ctx context.Context) bool {
	c, err := p.dial(ctx, true)
	if c != nil {
		c.Close()
	}
	return err == nil
}
