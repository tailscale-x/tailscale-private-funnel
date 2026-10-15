package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKubernetesPublicEndpointIdentityReplayAndRetention(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	_ = f
	pub, priv, _ := report.GenerateKey()
	_ = s.UpsertNode("lb", "loadbalancer", "lb")
	_ = s.BindNodeIdentity("lb", encodePublicKey(pub), "tail-lb", nil)
	d := nodeDeployment{AddressSource: "kubernetes", DeploymentTarget: "kubernetes", KubernetesNamespace: "system", PublicService: "edge-public", LoadBalancerHostname: "edge.example.com"}
	_ = s.SetJSON("node_deployment:lb", d)
	_ = s.SetJSON("node_health:lb", map[string]any{"received_at": time.Now().Unix()})
	n, _ := s.Node("lb")
	in := publicEndpoint{Namespace: "system", Service: "edge-public", UID: "service-uid", ObservedAt: time.Now().Unix(), Ingress: []core.LoadBalancerIngress{{IP: "8.8.8.8"}, {IP: "9.9.9.9"}}}
	envelope := func(p publicEndpoint) []byte {
		env, _ := report.NewEnvelope("lb", p, pub, priv, time.Now())
		b, _ := json.Marshal(env)
		return b
	}
	request := func(b []byte, identity string) *httptest.ResponseRecorder {
		r := WithPrincipal(httptest.NewRequest(http.MethodPost, "/api/node/public-endpoint", bytes.NewReader(b)), Principal{NodeID: identity})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	body := envelope(in)
	if w := request(body, "other"); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(body, "tail-lb"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(body, "tail-lb"); w.Code != 409 {
		t.Fatal("replay", w.Code)
	}
	ips, e := c.kubernetesAddresses(context.Background(), n, d)
	if e != nil || len(ips) != 2 {
		t.Fatal(ips, e)
	}
	// Pending allocation preserves the exact owner's existing A records.
	if e = s.ReplaceLedger("fixture", "example.com", "loadbalancer:lb", []libdns.Record{libdns.RR{Name: "edge.example.com", Type: "A", Data: "8.8.8.8"}}); e != nil {
		t.Fatal(e)
	}
	in.Ingress = []core.LoadBalancerIngress{{IP: "10.0.0.1"}}
	if w := request(envelope(in), "tail-lb"); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if _, e = c.kubernetesAddresses(context.Background(), n, d); e == nil {
		t.Fatal("published private address")
	}
	ledger, _ := s.Ledger("fixture", "example.com", "loadbalancer:lb")
	if len(ledger) != 1 {
		t.Fatal("lost prior ownership ledger")
	}
	in.UID = "other"
	if w := request(envelope(in), "tail-lb"); w.Code != 409 {
		t.Fatal("Service UID rebound", w.Code)
	}
	in.UID = "service-uid"
	in.Service = "another"
	if w := request(envelope(in), "tail-lb"); w.Code != 400 {
		t.Fatal("Service name mismatch", w.Code)
	}
	_ = s.RevokeNode("lb")
	in.Service = "edge-public"
	if w := request(envelope(in), "tail-lb"); w.Code != 403 {
		t.Fatal("revoked accepted", w.Code)
	}
}
func TestKubernetesDeploymentUsesSeparateReleaseAndNamespace(t *testing.T) {
	d := resolveKubernetesDeployment(nodeDeployment{InstallationName: "team", HelmRelease: "edge-2", DeploymentTarget: "kubernetes", LoadBalancerHostname: "edge.example.com"})
	if d.KubernetesNamespace != "tailscale-private-funnel-team-system" {
		t.Fatal(d)
	}
	script := loadBalancerHelmScript("id", "code", "key", "control", "http://control", "edge", "image:dev-test", d)
	for _, s := range []string{`"component": "loadbalancer"`, `"installation": "team"`, `release='edge-2'`, `loadbalancer-hostname`, `get service`, `rollout status deployment/"$release-loadbalancer"`} {
		if !bytes.Contains([]byte(script), []byte(s)) {
			t.Fatal(s, script)
		}
	}
}

func TestControlHelmInstructionsAreAdminOnlyAndContainNoCredentials(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net"})
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "viewer": {"node:viewer"}}})
	_ = s.SetSecret("tailscale_oauth_client_secret", "must-never-render")
	for _, id := range []string{"viewer", "admin"} {
		r := WithPrincipal(httptest.NewRequest("GET", "/api/kubernetes/control-install?installation=team", nil), Principal{NodeID: id})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if id == "viewer" && w.Code != 403 {
			t.Fatal(w.Code)
		}
		if id == "admin" && (w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("tailscale-private-funnel-team-system")) || bytes.Contains(w.Body.Bytes(), []byte("must-never-render"))) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestKubernetesServiceHostnameAndStaleDiscovery(t *testing.T) {
	c, s := testControl(t)
	n := store.NodeRecord{ID: "lb"}
	d := nodeDeployment{KubernetesNamespace: "system", PublicService: "public"}
	p := publicEndpoint{Namespace: "system", Service: "public", UID: "uid", ObservedAt: time.Now().Unix(), Ingress: []core.LoadBalancerIngress{{Hostname: "lb.example.net"}}}
	_ = s.SetJSON("node_public_endpoint:lb", p)
	c.PublicLookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.1.2.3")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	ips, e := c.kubernetesAddresses(context.Background(), n, d)
	if e != nil || len(ips) != 1 || ips[0] != "8.8.8.8" {
		t.Fatal(ips, e)
	}
	c.PublicLookup = func(context.Context, string) ([]net.IPAddr, error) { return nil, fmt.Errorf("DNS unavailable") }
	if _, e = c.kubernetesAddresses(context.Background(), n, d); e == nil {
		t.Fatal("failed hostname resolved through fallback")
	}
	p.ObservedAt = time.Now().Add(-2 * time.Minute).Unix()
	_ = s.SetJSON("node_public_endpoint:lb", p)
	if _, e = c.kubernetesAddresses(context.Background(), n, d); e == nil {
		t.Fatal("stale status accepted")
	}
}

func TestPublicServiceBindingRequiresAdministratorAndStoppedConfirmation(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("lb", "loadbalancer", "edge")
	_ = s.SetJSON("node_deployment:lb", nodeDeployment{LoadBalancerHostname: "edge.example.com"})
	_ = s.SetJSON("node_public_endpoint:lb", publicEndpoint{UID: "old"})
	for _, tc := range []struct {
		admin, stopped bool
		code           int
	}{{false, true, 403}, {true, false, 400}, {true, true, 200}} {
		body := fmt.Sprintf(`{"namespace":"funnel-system","service":"edge-public","confirm_stopped":%t}`, tc.stopped)
		r := httptest.NewRequest("POST", "/api/nodes/lb/public-service", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), accessKey{}, Access{Admin: tc.admin}))
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("id", "lb")
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
		w := httptest.NewRecorder()
		c.bindPublicService(w, r)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	d, e := c.nodeDeployment("lb")
	if e != nil || d.AddressSource != "kubernetes" {
		t.Fatal(d, e)
	}
	var p publicEndpoint
	if e = s.GetJSON("node_public_endpoint:lb", &p); e != sql.ErrNoRows {
		t.Fatal("old UID binding retained", e)
	}
}
