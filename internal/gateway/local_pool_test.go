package gateway

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

func TestPoolNeverFallsBackToRemoteNode(t *testing.T) {
	for _, address := range []string{"127.0.0.1:80", "10.0.0.2:80"} {
		t.Run(address, func(t *testing.T) {
			dialed := false
			p := &LocalPool{DialContext: func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				c, peer := net.Pipe()
				peer.Close()
				return c, nil
			}}
			p.Update([]proxy.PodEndpoint{{Address: address, NodeUID: "remote", NodeName: "other", PodUID: "pod"}}, "local", "here")
			if c, err := p.Dial(t.Context(), "tcp", ""); err == nil || dialed {
				if c != nil {
					c.Close()
				}
				t.Fatal("selected remote endpoint without opt-in")
			}
		})
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

func TestLocalFirstPoolFallsBackOnlyToValidatedConfiguredPods(t *testing.T) {
	calls := []string{}
	p := &LocalPool{AllowRemote: true, DialContext: func(_ context.Context, _, a string) (net.Conn, error) {
		calls = append(calls, a)
		if a == "10.1.0.1:80" {
			return nil, fmt.Errorf("local failed")
		}
		c, b := net.Pipe()
		b.Close()
		return c, nil
	}}
	p.Update([]proxy.PodEndpoint{{Address: "10.1.0.1:80", PodUID: "local-pod", NodeUID: "local", NodeName: "here"}, {Address: "10.2.0.2:80", PodUID: "remote-pod", NodeUID: "remote", NodeName: "there"}, {Address: "10.9.0.1:0", PodUID: "bad", NodeUID: "remote", NodeName: "there"}, {Address: "10.9.0.2:80"}}, "local", "here")
	c, e := p.Dial(t.Context(), "tcp", "")
	if e != nil {
		t.Fatal(e)
	}
	c.Close()
	if len(calls) != 2 || calls[0] != "10.1.0.1:80" || calls[1] != "10.2.0.2:80" || p.Count() != 1 || p.RemoteCount() != 1 {
		t.Fatal(calls, p.Count(), p.RemoteCount())
	}
	// A replacement snapshot revokes the old Pod address, even when fallback is on.
	p.Update(nil, "local", "here")
	if _, e = p.Dial(t.Context(), "tcp", ""); e == nil {
		t.Fatal("stale endpoint retained")
	}
}

func TestFirstAdvertisementRequiresReachableLocalBackend(t *testing.T) {
	p := &LocalPool{AllowRemote: true, DialContext: func(_ context.Context, _, a string) (net.Conn, error) {
		if a == "10.1.0.1:80" {
			return nil, fmt.Errorf("local failed")
		}
		c, b := net.Pipe()
		b.Close()
		return c, nil
	}}
	p.Update([]proxy.PodEndpoint{{Address: "10.1.0.1:80", PodUID: "local", NodeUID: "local", NodeName: "here"}, {Address: "10.2.0.2:80", PodUID: "remote", NodeUID: "remote", NodeName: "there"}}, "local", "here")
	if p.ReadyLocal(t.Context()) {
		t.Fatal("remote reachability admitted a new Service host")
	}
	if !p.Ready(t.Context()) {
		t.Fatal("established host cannot use authorized remote fallback")
	}
}
