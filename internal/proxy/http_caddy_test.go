//go:build caddy

package proxy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"
)

func TestHTTPRulesCaddyAdapt(t *testing.T) {
	r := HTTPRule{Matches: []Match{{Path: "/api", Method: "GET", Headers: map[string]string{"X-Test": "yes"}, Query: map[string]string{"q": "value"}}}, Backends: []Backend{{Key: "a", Service: "100.70.0.1", Port: 8080, Weight: 1}, {Key: "b", Service: "fd7a:115c:a1e0::1", Port: 8081, Weight: 2}}, RequestHeaders: HeaderFilter{Set: map[string]string{"X-App": "checkout"}}, RequestTimeout: "30s", BackendTimeout: "5s", MaxRequestBodyBytes: 10000}
	config, e := caddyfile(":443", "", t.TempDir(), "", []Route{{Hostname: "app.example.com", Rules: []HTTPRule{r}}})
	if e != nil {
		t.Fatal(e)
	}
	adapted, _, e := caddyconfig.GetAdapter("caddyfile").Adapt([]byte(config), nil)
	if e != nil {
		t.Fatal(e, config)
	}
	if !strings.Contains(string(adapted), "funnel_deadline") || !strings.Contains(string(adapted), "response_header_timeout") {
		t.Fatal(string(adapted))
	}
}

func TestGatewayMatchesAreExactAndSegmentAware(t *testing.T) {
	m := ExactMatch{MatchConfig: Match{Path: "/api", Headers: map[string]string{"X-Test": "*"}, Query: map[string]string{"q": "*"}}}
	r := httptest.NewRequest("GET", "http://example.com/api?q=%2A", nil)
	r.Header.Set("X-Test", "anything")
	if m.Match(r) {
		t.Fatal("literal wildcard treated as pattern")
	}
	r.Header.Set("X-Test", "*")
	if !m.Match(r) {
		t.Fatal("exact header/query did not match")
	}
	r.URL.Path = "/apix"
	if m.Match(r) {
		t.Fatal("prefix matched partial path segment")
	}
}
