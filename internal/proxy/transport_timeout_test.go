//go:build caddy

package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
)

func TestBackendTimeoutPreservesUpgradedDuplexBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: funnel-test\r\n\r\n")
		rw.Flush()
		b := make([]byte, 4)
		if _, err = io.ReadFull(rw, b); err == nil {
			rw.Write(b)
			rw.Flush()
		}
	}))
	defer server.Close()
	transport := &http.Transport{}
	defer transport.CloseIdleConnections()
	tr := tailnetTransport{HTTPTransport: reverseproxy.HTTPTransport{Transport: transport}, BackendRequestTimeout: caddy.Duration(5 * time.Second)}
	req, _ := http.NewRequest("GET", server.URL, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "funnel-test")
	response, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	stream, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		t.Fatal("timeout wrapper discarded upgraded connection's Write method")
	}
	if _, err = stream.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4)
	if _, err = io.ReadFull(stream, b); err != nil || string(b) != "ping" {
		t.Fatal(string(b), err)
	}
}

func TestSelectedTailnetDialerRetainsCaddyConnectTimeout(t *testing.T) {
	SetDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	defer SetDialer(nil)
	ctx, cancel := caddy.NewContext(caddy.Context{Context: t.Context()})
	defer cancel()
	transport := tailnetTransport{HTTPTransport: reverseproxy.HTTPTransport{DialTimeout: caddy.Duration(25 * time.Millisecond)}}
	if err := transport.Provision(ctx); err != nil {
		t.Fatal(err)
	}
	defer transport.Cleanup()
	start := time.Now()
	_, err := transport.Transport.DialContext(t.Context(), "tcp", "100.64.0.1:8080")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("connect timeout lost: %v after %v", err, time.Since(start))
	}
}
