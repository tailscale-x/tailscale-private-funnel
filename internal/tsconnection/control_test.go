package tsconnection

import (
	"context"
	proxyproto "github.com/pires/go-proxyproto"
	"io"
	"net"
	"tailscale.com/tsnet"
	"testing"
	"time"
)

type serviceConnection struct {
	Connection
	listener net.Listener
	mode     tsnet.ServiceMode
}

func (c *serviceConnection) ListenService(_ string, m tsnet.ServiceMode) (net.Listener, error) {
	c.mode = m
	return c.listener, nil
}
func TestEmbeddedControlServicePreservesVerifiedPeerAddress(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-metadata", true: "source-preserved"}[valid], func(t *testing.T) {
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			c := &serviceConnection{listener: raw}
			ln, err := ListenControl(context.Background(), c, "svc:control", 8080)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			if c.mode.(tsnet.ServiceModeTCP).PROXYProtocolVersion != 2 {
				t.Fatal("tsnet will hide the peer behind loopback")
			}
			done := make(chan error, 1)
			go func() {
				x, e := ln.Accept()
				if e != nil {
					done <- e
					return
				}
				defer x.Close()
				x.SetDeadline(time.Now().Add(time.Second))
				b := make([]byte, 1)
				_, e = io.ReadFull(x, b)
				if valid && e == nil && x.RemoteAddr().String() != "100.64.1.2:1234" {
					t.Error("lost Tailscale peer", x.RemoteAddr())
				}
				done <- e
			}()
			x, err := net.Dial("tcp", raw.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			if valid {
				h := &proxyproto.Header{Version: 2, Command: proxyproto.PROXY, TransportProtocol: proxyproto.TCPv4, SourceAddr: &net.TCPAddr{IP: net.ParseIP("100.64.1.2"), Port: 1234}, DestinationAddr: &net.TCPAddr{IP: net.ParseIP("100.100.1.1"), Port: 8080}}
				h.WriteTo(x)
			}
			x.Write([]byte("x"))
			x.Close()
			e := <-done
			if valid && e != nil {
				t.Fatal(e)
			}
			if !valid && e == nil {
				t.Fatal("unauthenticated peer metadata accepted")
			}
		})
	}
}
