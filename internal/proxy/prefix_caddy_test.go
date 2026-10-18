//go:build caddy

package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/coder/websocket"
)

func TestPrefixRoutingAndRewriteThroughCaddy(t *testing.T) {
	type echo struct{ Backend, URI, Host, Method, Body, Client, Forwarded, Spoof string }
	backend := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/ws" {
				c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
				if err != nil {
					return
				}
				defer c.CloseNow()
				typ, payload, err := c.Read(r.Context())
				if err == nil {
					_ = c.Write(r.Context(), typ, payload)
				}
				return
			}
			if r.URL.Path == "/stream" {
				w.Write([]byte("first\n"))
				w.(http.Flusher).Flush()
				w.Write([]byte("second\n"))
				return
			}
			b, _ := io.ReadAll(r.Body)
			json.NewEncoder(w).Encode(echo{name, r.RequestURI, r.Host, r.Method, string(b), r.Header.Get("X-Forwarded-For"), r.Header.Get("Forwarded"), r.Header.Get("X-Forwarded-Secret")})
		}))
	}
	a, b := backend("api"), backend("admin")
	defer a.Close()
	defer b.Close()
	sharedDialer.Lock()
	sharedDialer.dial = func(ctx context.Context, network, target string) (net.Conn, error) {
		address := a.Listener.Addr().String()
		if external := os.Getenv("FUNNEL_PATH_ECHO_API"); external != "" {
			address = external
		}
		if strings.HasPrefix(target, "100.64.0.2:") {
			address = b.Listener.Addr().String()
			if external := os.Getenv("FUNNEL_PATH_ECHO_ADMIN"); external != "" {
				address = external
			}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	sharedDialer.Unlock()
	defer func() { sharedDialer.Lock(); sharedDialer.dial = nil; sharedDialer.Unlock() }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	rule := func(path, replacement, service string) HTTPRule {
		return HTTPRule{Matches: []Match{{Path: path}}, Rewrite: &PrefixRewrite{ReplacePrefix: replacement}, Backends: []Backend{{Key: service, Service: service, Port: 8080, Weight: 1}}}
	}
	routes := []Route{
		{Hostname: "app.example.test", Service: "100.64.0.2", Port: 8080},
		{Hostname: "app.example.test", Rules: []HTTPRule{rule("/api", "/", "100.64.0.1"), rule("/api/admin", "/internal", "100.64.0.2"), rule("/keep", "/keep", "100.64.0.1")}},
		{Hostname: "root.example.test", Rules: []HTTPRule{rule("/", "/internal", "100.64.0.1")}},
	}
	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	dataDir := t.TempDir()
	load := func(routes []Route) {
		t.Helper()
		config, err := caddyfile(":443", "", dataDir, "", routes)
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(config), nil)
		if err != nil {
			t.Fatal(err, config)
		}
		var root map[string]any
		json.Unmarshal(raw, &root)
		servers := root["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
		var selected map[string]any
		for _, value := range servers {
			server := value.(map[string]any)
			for _, v := range server["listen"].([]any) {
				if v == ":443" {
					selected = server
				}
			}
		}
		if selected == nil {
			t.Fatal("public server missing")
		}
		selected["listen"] = []string{address}
		delete(selected, "tls_connection_policies")
		selected["automatic_https"] = map[string]any{"disable": true}
		root["admin"] = map[string]any{"disabled": true}
		root["apps"] = map[string]any{"http": map[string]any{"servers": map[string]any{"test": selected}}, "tls": map[string]any{"disable_storage_clean": true}}
		raw, _ = json.Marshal(root)
		if err = caddy.Load(raw, true); err != nil {
			t.Fatal(err)
		}
		client.CloseIdleConnections()
	}
	load(routes)
	defer caddy.Stop()
	check := func(host, path, wantBackend, wantURI string) {
		t.Helper()
		req, _ := http.NewRequest("POST", "http://"+address+path, strings.NewReader("payload"))
		req.Host = host
		req.Header.Set("X-Forwarded-For", "spoof")
		req.Header.Set("Forwarded", "spoof")
		req.Header.Set("X-Forwarded-Secret", "spoof")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var got echo
		if err = json.NewDecoder(res.Body).Decode(&got); err != nil {
			t.Fatal(err, res.Status)
		}
		if got.Backend != wantBackend || got.URI != wantURI || got.Host != host || got.Method != "POST" || got.Body != "payload" || got.Client != "127.0.0.1" || got.Forwarded != "" || got.Spoof != "" {
			t.Fatalf("%s: %+v; want %s %s", path, got, wantBackend, wantURI)
		}
	}
	for _, tt := range []struct{ path, backend, uri string }{
		{"/api/users?q=1", "api", "/users?q=1"},
		{"/api", "api", "/"},
		{"/api/", "api", "/"},
		{"/api/admin/users", "admin", "/internal/users"},
		{"/api/admin", "admin", "/internal"},
		{"/apiculture", "admin", "/apiculture"},
		{"/api/a%2Fb?q=%2F", "api", "/a%2Fb?q=%2F"},
		{"/a%70i/users", "api", "/users"},
		{"/api%2Fusers", "api", "/users"},
		{"/api/%7Benv.SECRET%7D", "api", "/%7Benv.SECRET%7D"},
		{"/keep/path", "api", "/keep/path"},
	} {
		check("app.example.test", tt.path, tt.backend, tt.uri)
	}
	check("root.example.test", "/users?q=1", "api", "/internal/users?q=1")
	// A WebSocket upgrade traverses the same rewritten route without an additional proxy.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://"+address+"/api/ws", &websocket.DialOptions{Host: "app.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	if err = ws.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, payload, err := ws.Read(ctx)
	if err != nil || string(payload) != "hello" {
		t.Fatal(string(payload), err)
	}
	req, _ := http.NewRequest("GET", "http://"+address+"/api/stream", nil)
	req.Host = "app.example.test"
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(data) != "first\nsecond\n" {
		t.Fatal(string(data))
	}
	ws.Close(websocket.StatusNormalClosure, "done")
	// Withdraw one prefix and reload; the sibling and whole-host fallback stay live.
	routes[1].Rules = routes[1].Rules[1:]
	load(routes)
	check("app.example.test", "/api/users", "admin", "/api/users")
	check("app.example.test", "/api/admin/users", "admin", "/internal/users")
}
