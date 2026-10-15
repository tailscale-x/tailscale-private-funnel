package gateway

import (
	"context"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"net"
	"testing"
)

func TestPoolNeverFallsBackToRemoteNode(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p := &LocalPool{}
	p.Update([]proxy.PodEndpoint{{Address: l.Addr().String(), NodeUID: "remote", NodeName: "other", PodUID: "pod"}}, "local", "here")
	if _, e = p.Dial(context.Background(), "tcp", ""); e == nil {
		t.Fatal("dialed remote endpoint")
	}
	p.Update([]proxy.PodEndpoint{{Address: "10.0.0.2:80", NodeUID: "remote", NodeName: "other", PodUID: "pod"}}, "local", "here")
	if _, e = p.Dial(context.Background(), "tcp", ""); e == nil {
		t.Fatal("selected remote endpoint")
	}
}

func TestLocalPoolRoundRobinAndFailureStayLocal(t *testing.T) {
	calls := []string{}
	p := &LocalPool{DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
		calls = append(calls, address)
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}}
	p.Update([]proxy.PodEndpoint{{Address: "10.1.1.1:80", NodeUID: "local", NodeName: "here", PodUID: "one"}, {Address: "10.1.1.2:80", NodeUID: "local", NodeName: "here", PodUID: "two"}, {Address: "10.2.1.1:80", NodeUID: "other", NodeName: "there", PodUID: "three"}}, "local", "here")
	for i := 0; i < 4; i++ {
		c, e := p.Dial(context.Background(), "tcp", "")
		if e != nil {
			t.Fatal(e)
		}
		c.Close()
	}
	if len(calls) != 4 || calls[0] != calls[2] || calls[1] != calls[3] || calls[0] == calls[1] {
		t.Fatal(calls)
	}
	p.Update(nil, "local", "here")
	if _, e := p.Dial(context.Background(), "tcp", ""); e == nil {
		t.Fatal("stale endpoint retained")
	}
}
