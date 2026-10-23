//go:build caddy

package proxy

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
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

func TestForwardedHeadersAreGeneratedAtPublicEdge(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1"} {
		for _, direct := range []bool{false, true} {
			name := ip + "/service"
			if direct {
				name = ip + "/http-rule"
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
	sharedDialer.Unlock()
	route := Route{Hostname: "app.example.test", Service: "100.64.0.1", Port: 8080}
	if direct {
		route = Route{Hostname: "app.example.test", Rules: []HTTPRule{{Matches: []Match{{Path: "/"}}, Backends: []Backend{{Key: "backend", Service: "100.64.0.1", Port: 8080, Weight: 1}}}}}
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
	// Certificate-storage maintenance is unrelated to this HTTP header contract.
	// Disable its background writes so TempDir cleanup cannot race Caddy.
	root["apps"] = map[string]any{"http": map[string]any{"servers": map[string]any{"test": selected}}, "tls": map[string]any{"disable_storage_clean": true}}
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
	unknown, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	unknown.Host = "withdrawn.example.test"
	missing, err := http.DefaultClient.Do(unknown)
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("unconfigured hostname returned %d; want 404", missing.StatusCode)
	}
}

func TestLoadBalancerShutdownHasBoundedGracePeriod(t *testing.T) {
	config, err := caddyfile(":443", "", t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(config), nil)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		Apps struct {
			HTTP struct {
				GracePeriod int64 `json:"grace_period"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if root.Apps.HTTP.GracePeriod != 5_000_000_000 {
		t.Fatalf("unbounded or unexpected shutdown grace: %s", raw)
	}
}
