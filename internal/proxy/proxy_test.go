//go:build caddy

package proxy

import (
	"strings"
	"testing"
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
