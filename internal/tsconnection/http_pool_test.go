package tsconnection

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"tailscale.com/tsnet"
	"testing"
	"time"
)

type pollingHostAPI struct {
	*fakeHost
	target string
}

func (f pollingHostAPI) UserDial(ctx context.Context, network, _ string, _ uint16) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, f.target)
}

// Polling builds new RPC client wrappers on every pass. Those wrappers must
// reuse the connection owner's transport instead of keeping one socket per pass.
func TestPollingHTTPClientsReuseConnectionsAndCloseIdle(t *testing.T) {
	var accepted atomic.Int32
	backend := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }))
	backend.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			accepted.Add(1)
		}
	}
	backend.Start()
	defer backend.Close()
	host, f := hostFixture(t)
	host.API = pollingHostAPI{f, backend.Listener.Addr().String()}
	bound := &Bound{Connection: host, ComponentID: "component", InstanceID: "instance"}
	for range 200 {
		client := bound.HTTPClient()
		res, err := client.Get("http://100.64.0.1:8080/poll")
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}
	if n := accepted.Load(); n != 1 {
		t.Fatalf("200 polls retained %d connections; want one shared connection", n)
	}
	// Closing through a bound RPC wrapper must reach the shared transport.
	bound.HTTPClient().CloseIdleConnections()
	res, err := bound.HTTPClient().Get("http://100.64.0.1:8080/poll")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if n := accepted.Load(); n != 2 {
		t.Fatalf("CloseIdleConnections did not reach owned transport: %d connections", n)
	}
	if err = host.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedHTTPClientSharesTransportWithoutSharingMutableOptions(t *testing.T) {
	e := Wrap(&tsnet.Server{})
	first, second := e.HTTPClient(), e.HTTPClient()
	if first == second {
		t.Fatal("callers share mutable client options")
	}
	if first.Transport != second.Transport {
		t.Fatal("embedded polling creates a new transport per client")
	}
	first.Timeout = time.Second
	if second.Timeout == first.Timeout {
		t.Fatal("one caller changed another caller's timeout")
	}
	transport := first.Transport.(*http.Transport)
	if transport.IdleConnTimeout <= 0 || transport.MaxIdleConns <= 0 {
		t.Fatal("unbounded idle transport")
	}
	e.http.closeIdle()
}

func TestHTTPClientPoolConcurrentCallers(t *testing.T) {
	e := Wrap(&tsnet.Server{})
	clients := make(chan *http.Client, 100)
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() { defer wg.Done(); clients <- e.HTTPClient() }()
	}
	wg.Wait()
	close(clients)
	var transport http.RoundTripper
	for client := range clients {
		if transport == nil {
			transport = client.Transport
		}
		if client.Transport != transport {
			t.Fatal("concurrent callers created separate transports")
		}
	}
	e.http.closeIdle()
}
