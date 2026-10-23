//go:build caddy

package web

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"testing"

	"github.com/caddyserver/caddy/v2"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

func init() {
	macQAEdge = func(t *testing.T, dial func(context.Context, string, string) (net.Conn, error)) (string, func(string, uint16)) {
		t.Helper()
		proxy.SetDialer(dial)
		t.Cleanup(func() { _ = proxy.Stop() })
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := ln.Addr().String()
		ln.Close()
		load := func(service string, port uint16) {
			t.Helper()
			handler := map[string]any{"handler": "static_response", "status_code": 503}
			if service != "" {
				handler = map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": net.JoinHostPort(service, strconv.Itoa(int(port)))}}, "transport": map[string]any{"protocol": "funnel_tsnet", "dial_timeout": "8s", "response_header_timeout": "8s"}}
			}
			config := map[string]any{"admin": map[string]any{"disabled": true}, "apps": map[string]any{"http": map[string]any{"servers": map[string]any{"mac-qa": map[string]any{"listen": []string{address}, "automatic_https": map[string]any{"disable": true}, "routes": []any{map[string]any{"handle": []any{handler}}}}}}}}
			raw, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err = caddy.Load(raw, true); err != nil {
				t.Fatal(err)
			}
		}
		load("", 0)
		return "http://" + address, load
	}
}
