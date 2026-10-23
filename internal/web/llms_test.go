package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentDocumentationUsesRunningReleaseAndLiveSettings(t *testing.T) {
	c, _, _ := rpcFixture(t, Principal{NodeID: "admin"})
	c.BuildVersion = "v3.1.0"
	for _, controlURL := range []string{"http://control.example.ts.net:8080", "http://renamed.example.ts.net:8080"} {
		if err := c.Store.SetJSON("tailnet", map[string]string{"management_url": controlURL, "release_version": "v2.0.0", "oauth_secret": "must-not-render"}); err != nil {
			t.Fatal(err)
		}
		req := WithPrincipal(httptest.NewRequest("GET", "http://untrusted.example/llms.txt", nil), Principal{NodeID: "admin"})
		response := httptest.NewRecorder()
		c.Handler().ServeHTTP(response, req)
		body := response.Body.String()
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("response: %d %v", response.Code, response.Header())
		}
		for _, want := range []string{"releases/download/v3.1.0/tailscale-private-funnel-linux-amd64", "tailscale-private-funnel-linux-arm64", "tailscale-private-funnel-darwin-arm64", "SHA256SUMS", "--version 'v3.1.0'", "manage whoami --control-url '" + controlURL + "' --output json", "native gateways create", "k8s gateways create", "15 minutes", "## Start here", "/docs/api.md", "## Foreground local HTTP tunnel", "tunnel 8080 --hostname app.example.com --control-url", "tag:tailscale-private-funnel-main-tailnet-gateway", "--output json for newline-delimited progress events"} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q", want)
			}
		}
		for _, forbidden := range []string{"untrusted.example", "must-not-render", "releases/download/v2.0.0", "create-remote"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("unexpected %q", forbidden)
			}
		}
	}
}

func TestAgentDocumentationDevelopmentAndAuthorization(t *testing.T) {
	c, _, _ := rpcFixture(t, Principal{NodeID: "admin"})
	c.BuildVersion = "dev-test"
	if err := c.Store.SetJSON("tailnet", map[string]string{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		identity string
		status   int
	}{{"admin", 200}, {"viewer", 200}, {"unknown", 403}} {
		response := httptest.NewRecorder()
		c.Handler().ServeHTTP(response, WithPrincipal(httptest.NewRequest("GET", "/llms.txt", nil), Principal{NodeID: tc.identity}))
		if response.Code != tc.status {
			t.Fatalf("%s: %d", tc.identity, response.Code)
		}
		if tc.status == 200 {
			body := response.Body.String()
			if !strings.Contains(body, "releases/latest/download/") || !strings.Contains(body, "CONTROL_TAILNET_HOST") || strings.Contains(body, "releases/download/dev-test") {
				t.Fatal("invalid development download guidance")
			}
		}
	}
}
