package tsconnection

import (
	"context"
	"net"
	"tailscale.com/tsnet"
	"testing"
)

func TestDrainQuiescesAcceptorWithoutPrematureSDKCleanup(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	// No Server is present: invoking SDK Close would panic. Quiescing must only
	// close its exported raw acceptor, retaining the SDK's Service references.
	wrapped := &serviceListener{ServiceListener: &tsnet.ServiceListener{Listener: ln}}
	if e = wrapped.Drain(t.Context()); e != nil {
		t.Fatal(e)
	}
	if c, e := net.Dial("tcp", ln.Addr().String()); e == nil {
		c.Close()
		t.Fatal("acceptor remains open")
	}
}
func TestCanceledDrainDoesNotMutateListener(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	wrapped := &serviceListener{ServiceListener: &tsnet.ServiceListener{Listener: ln}}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if e = wrapped.Drain(ctx); e == nil {
		t.Fatal("canceled drain succeeded")
	}
	c, e := net.Dial("tcp", ln.Addr().String())
	if e != nil {
		t.Fatal("canceled drain changed acceptor", e)
	}
	c.Close()
}
