package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestParseLabel(t *testing.T) {
	got, err := ParseLabel(" 8080:Checkout.dev.example.com:http, 8443:admin.dev.example.com:http ")
	if err != nil || len(got) != 2 || got[0].Hostname != "checkout.dev.example.com" || got[1].Port != 8443 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}

func TestParseLabelRejectsGenericProtocolsAndConflicts(t *testing.T) {
	for _, input := range []string{"8080:app.example:tcp", "8080:app.example:http,8080:app.example:http", "8080:app.example"} {
		if _, err := ParseLabel(input); err == nil {
			t.Fatalf("accepted invalid label %q", input)
		}
	}
}

func TestSelectNetworks(t *testing.T) {
	got := SelectNetworks([]Network{{ID: "1", Name: "host", UserDefined: false}, {ID: "2", Name: "app-net", UserDefined: true}, {ID: "3", Name: "secret", UserDefined: true}}, []string{"secret"})
	if len(got) != 1 || got[0].Name != "app-net" {
		t.Fatalf("selected=%#v", got)
	}
}

func TestForward(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		c, e := backend.Accept()
		if e == nil {
			defer c.Close()
			_, _ = c.Write([]byte("ok"))
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = Forward(ctx, listener, backend.Addr().String()) }()
	c, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	buf := make([]byte, 2)
	if _, err := c.Read(buf); err != nil || string(buf) != "ok" {
		t.Fatalf("read=%q err=%v", buf, err)
	}
}

func TestValidateNoOverlap(t *testing.T) {
	if err := ValidateNoOverlap([]Network{{Name: "app", CIDR: "10.0.0.0/24"}, {Name: "shared", CIDR: "10.0.0.0/25"}}); err == nil {
		t.Fatal("overlapping Docker networks were accepted")
	}
	if err := ValidateNoOverlap([]Network{{Name: "app", CIDR: "10.0.0.0/24"}, {Name: "shared", CIDR: "10.0.1.0/24"}}); err != nil {
		t.Fatal(err)
	}
}

func TestForwardDialPreservesHalfClosedStreaming(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	payload := bytes.Repeat([]byte("raw-stream"), 8192)
	go func() {
		c, err := backend.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		data, _ := io.ReadAll(c)
		_, _ = c.Write(append([]byte("response:"), data...))
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = ForwardDial(ctx, listener, "bound-device", func(ctx context.Context, network, target string) (net.Conn, error) {
			if target != "bound-device" {
				return nil, fmt.Errorf("unexpected target")
			}
			return (&net.Dialer{}).DialContext(ctx, network, backend.Addr().String())
		})
	}()
	c, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err = c.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = c.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(c)
	if err != nil || !bytes.Equal(data, append([]byte("response:"), payload...)) {
		t.Fatalf("half-close truncated reverse stream: %d bytes %v", len(data), err)
	}
}

func TestBackendEndpointUsesReachableSelectedNetwork(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	port := uint16(backend.Addr().(*net.TCPAddr).Port)
	ip, target, err := backendEndpoint(context.Background(), []Network{
		{Name: "missing", IPAddress: ""},
		{Name: "allowed", IPAddress: "127.0.0.1"},
	}, port)
	if err != nil || ip != "127.0.0.1" || target != backend.Addr().String() {
		t.Fatalf("selected backend = %q %q, %v", ip, target, err)
	}
	if _, _, err := backendEndpoint(context.Background(), []Network{{Name: "denied", IPAddress: ""}}, port); err == nil {
		t.Fatal("accepted an unavailable selected backend")
	}
}
