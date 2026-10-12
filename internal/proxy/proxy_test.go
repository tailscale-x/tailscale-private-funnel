//go:build caddy

package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedCaddyConfigValidates(t *testing.T) {
	if err := ValidateConfig("loadbalancer"); err != nil {
		t.Fatalf("loadbalancer config: %v", err)
	}
}

func TestCaddyUsesServiceAddressNotControlName(t *testing.T) {
	config, err := caddyfile(":443", "", "/tmp/private-funnel", "", []Route{{Hostname: "app.example.test", Service: "checkout.example.ts.net", Port: 8080}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, "reverse_proxy checkout.example.ts.net:8080") {
		t.Fatalf("service target was not rendered as a routable address: %s", config)
	}
	if _, err := caddyfile(":443", "", "/tmp/private-funnel", "", []Route{{Hostname: "app.example.test", Service: "svc:checkout", Port: 8080}}); err == nil {
		t.Fatal("accepted a control-plane svc name as a network target")
	}
}

func TestDirectRouteContainsStableDeviceIdentity(t *testing.T) {
	config, err := caddyfile(":443", "", "/tmp/private-funnel", "", []Route{{Hostname: "voice.example.test", BackendDeviceID: "device-123", Port: 8766}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, `device "device-123"`) || !strings.Contains(config, "reverse_proxy direct.invalid:8766") {
		t.Fatal(config)
	}
	if _, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(config), nil); err != nil {
		t.Fatal(err)
	}
	for _, route := range []Route{{Hostname: "voice.example.test", BackendDeviceID: "bad id", Port: 8766}, {Hostname: "voice.example.test", BackendDeviceID: "device", Service: "svc.example", Port: 8766}} {
		if _, err := caddyfile(":443", "", "/tmp/private-funnel", "", []Route{route}); err == nil {
			t.Fatalf("accepted invalid direct route: %+v", route)
		}
	}
}

func TestDirectRouteResolvesIdentityForEachConnection(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events" {
			w.Header().Set("Connection", "close")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: first\n\n"))
			w.(http.Flusher).Flush()
			time.Sleep(350 * time.Millisecond)
			_, _ = w.Write([]byte("data: second\n\n"))
			return
		}
		w.Header().Set("Connection", "close")
		_, _ = w.Write([]byte(r.Host))
	}))
	defer backend.Close()
	endpoint := strings.TrimPrefix(backend.URL, "http://")
	sharedDialer.Lock()
	sharedDialer.dial = func(ctx context.Context, network, target string) (net.Conn, error) {
		d := net.Dialer{}
		return d.DialContext(ctx, network, target)
	}
	count := 0
	sharedDialer.resolve = func(_ context.Context, id string, port uint16) (string, error) {
		if id != "device-123" || port != 8766 {
			t.Errorf("unexpected target %s:%d", id, port)
		}
		count++
		if count > 2 {
			return "", errors.New("peer disappeared")
		}
		return endpoint, nil
	}
	sharedDialer.Unlock()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	config, err := caddyfile(":443", "", t.TempDir(), "", []Route{{Hostname: "voice.example.test", BackendDeviceID: "device-123", Port: 8766}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(config), nil)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err = json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	servers := root["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	var selected map[string]any
	for _, value := range servers {
		server := value.(map[string]any)
		if listen, ok := server["listen"].([]any); ok && len(listen) > 0 && listen[0] == ":443" {
			selected = server
		}
	}
	if selected == nil {
		t.Fatal("public listener missing")
	}
	selected["listen"] = []string{addr}
	delete(selected, "tls_connection_policies")
	selected["automatic_https"] = map[string]any{"disable": true}
	root["admin"] = map[string]any{"disabled": true}
	root["apps"] = map[string]any{"http": map[string]any{"servers": map[string]any{"test": selected}}}
	raw, _ = json.Marshal(root)
	if err = caddy.Load(raw, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = caddy.Stop(); time.Sleep(100 * time.Millisecond) })
	for i := 0; i < 3; i++ {
		path := "/"
		if i == 1 {
			path = "/events"
		}
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		req.Host = "voice.example.test"
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		reader := bufio.NewReader(res.Body)
		if i == 1 {
			start := time.Now()
			line, readErr := reader.ReadString('\n')
			if readErr != nil || line != "data: first\n" || time.Since(start) > 250*time.Millisecond {
				t.Fatalf("SSE was not flushed promptly: %q %v", line, readErr)
			}
		}
		body, _ := io.ReadAll(reader)
		res.Body.Close()
		if i == 0 && res.StatusCode != 200 {
			t.Fatalf("first route HTTP %d", res.StatusCode)
		}
		if i == 0 && string(body) != "voice.example.test" {
			t.Fatalf("public Host was not preserved: %q", body)
		}
		if i == 1 && res.StatusCode != 200 {
			t.Fatalf("SSE HTTP %d", res.StatusCode)
		}
		if i == 1 && !strings.Contains(string(body), "data: second") {
			t.Fatalf("SSE stream truncated: %q", body)
		}
		if i == 2 && res.StatusCode != 502 {
			t.Fatalf("missing peer HTTP %d", res.StatusCode)
		}
	}
	if count != 3 {
		t.Fatalf("resolved %d connections", count)
	}
}

func TestForwardedHeadersAreGeneratedAtPublicEdge(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1"} {
		for _, direct := range []bool{false, true} {
			name := ip + "/service"
			if direct {
				name = ip + "/direct"
			}
			t.Run(name, func(t *testing.T) { testEdgeHeaders(t, ip, direct) })
		}
	}
}
func testEdgeHeaders(t *testing.T, clientIP string, direct bool) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"Host": r.Host, "For": r.Header.Get("X-Forwarded-For"), "Proto": r.Header.Get("X-Forwarded-Proto"), "ForwardedHost": r.Header.Get("X-Forwarded-Host"), "Port": r.Header.Get("X-Forwarded-Port"), "RealIP": r.Header.Get("X-Real-IP"), "Spoof": r.Header.Get("X-Forwarded-Secret"), "Forwarded": r.Header.Get("Forwarded")})
	}))
	defer backend.Close()
	listener, err := net.Listen("tcp", net.JoinHostPort(clientIP, "0"))
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	_, port, _ := net.SplitHostPort(addr)
	sharedDialer.Lock()
	sharedDialer.dial = func(ctx context.Context, network, target string) (net.Conn, error) {
		d := net.Dialer{}
		return d.DialContext(ctx, network, strings.TrimPrefix(backend.URL, "http://"))
	}
	sharedDialer.resolve = func(_ context.Context, id string, port uint16) (string, error) {
		if id != "device-123" || port != 8080 {
			t.Errorf("wrong direct identity %s:%d", id, port)
		}
		return "100.64.0.1:8080", nil
	}
	sharedDialer.Unlock()
	route := Route{Hostname: "app.example.test", Service: "100.64.0.1", Port: 8080}
	if direct {
		route = Route{Hostname: "app.example.test", BackendDeviceID: "device-123", Port: 8080}
	}
	config, err := caddyfile(":443", "", t.TempDir(), "", []Route{route})
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(config), nil)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(raw, &root)
	servers := root["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	var selected map[string]any
	for _, v := range servers {
		server := v.(map[string]any)
		if listeners, ok := server["listen"].([]any); ok && len(listeners) > 0 && listeners[0] == ":443" {
			selected = server
		}
	}
	if selected == nil {
		t.Fatal("public edge server missing")
	}
	selected["listen"] = []string{addr}
	delete(selected, "tls_connection_policies")
	selected["automatic_https"] = map[string]any{"disable": true}
	root["admin"] = map[string]any{"disabled": true}
	root["apps"] = map[string]any{"http": map[string]any{"servers": map[string]any{"test": selected}}}
	raw, _ = json.Marshal(root)
	if err = caddy.Load(raw, true); err != nil {
		t.Fatal(err)
	}
	defer caddy.Stop()
	req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	req.Host = "app.example.test"
	for _, name := range []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Host", "X-Forwarded-Port", "X-Real-IP", "X-Forwarded-Secret", "Forwarded"} {
		req.Header.Set(name, "spoofed")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got map[string]string
	if err = json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"Host": "app.example.test", "For": clientIP, "Proto": "http", "ForwardedHost": "app.example.test", "Port": port, "RealIP": clientIP, "Spoof": "", "Forwarded": ""} {
		if got[key] != want {
			t.Fatalf("%s=%q want %q; response %v", key, got[key], want, got)
		}
	}
}
