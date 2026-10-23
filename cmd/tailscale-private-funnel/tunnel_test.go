package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestForegroundTunnelCLIHasNoBootstrapOrServiceSurface(t *testing.T) {
	r := NewRoot("dev-test")
	var out bytes.Buffer
	r.SetOut(&out)
	r.SetArgs([]string{"tunnel", "--help"})
	if err := r.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tunnel <local-port>", "--hostname", "--control-url", "--local-address", "--tailscale-socket", "--output"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(want, out.String())
		}
	}
	for _, flag := range []string{"--auth-key", "--oauth-client-id-file", "--install-service", "--background"} {
		root := NewRoot("dev-test")
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"tunnel", "8080", "--hostname", "app.example.com", flag, "value"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatal("obsolete or unsupported path accepted", flag, err)
		}
	}
	for _, port := range []string{"0", "65536", "-1", "http://localhost:8080"} {
		root := NewRoot("dev-test")
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"tunnel", port, "--hostname", "app.example.com"})
		if err := root.Execute(); err == nil {
			t.Fatal("invalid local port accepted", port)
		}
	}
}
