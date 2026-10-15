package web

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestSpecializedGatewayEnrollmentAndRemovedSelectors(t *testing.T) {
	_, s := testControl(t)
	c := enrollmentTestControl(t, s)
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "management_url": "http://control:8080", "release_version": "v1.1.0"})
	for _, mode := range []string{"docker-gateway", "tailnet-gateway", "k8s-gateway"} {
		w := requestManagement(c, "POST", "/api/enrollments/"+mode, map[string]any{"name": "A name", "hostname": "test-" + mode}, "owner")
		if w.Code != 200 {
			t.Fatal(mode, w.Code, w.Body.String())
		}
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		script := body["deployment_script"].(string)
		if mode == "k8s-gateway" {
			if !strings.Contains(script, "helm upgrade --install") || !strings.Contains(script, "namespace='tailscale-private-funnel-main-system'") || strings.Contains(script, "--set auth") {
				t.Fatal(script)
			}
		} else if !strings.Contains(script, mode+" join") {
			t.Fatal(script)
		}
		for _, field := range []string{"loadbalancer_id", "loadbalancer_ids"} {
			w = requestManagement(c, "POST", "/api/enrollments/"+mode, map[string]any{"name": "A name", field: []string{"edge"}}, "owner")
			if w.Code != 400 {
				t.Fatal("removed field accepted", mode, field, w.Code)
			}
		}
	}
	if w := requestManagement(c, "POST", "/api/enrollments/ingress", map[string]any{}, "owner"); w.Code != 400 {
		t.Fatal("obsolete mode accepted", w.Code)
	}
	if w := requestManagement(c, "PUT", "/api/exposures/unknown/loadbalancers", map[string]any{"loadbalancer_ids": []string{"edge"}}, "owner"); w.Code != 404 {
		t.Fatal("override endpoint survived", w.Code)
	}
}
func TestKubernetesDesiredBindingReplayAndPolicy(t *testing.T) {
	_, s := testControl(t)
	c := enrollmentTestControl(t, s)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Kubernetes", HostnameRegexes: []string{`(?:app|alias)[.]example[.]com`}, Enabled: true}}})
	_ = s.UpsertNode("kube", "k8s-gateway", "kube")
	pub, key, _ := report.GenerateKey()
	_ = s.BindNodeIdentity("kube", encodePublicKey(pub), "kube-device", nil)
	b := proxy.Backend{Key: "service:8080", Address: "10.43.0.1:8080", Port: 8080, Weight: 1}
	snapshot := nodeconfig.KubernetesSnapshot{ClusterID: "cluster", ExpiresAt: time.Now().Add(time.Minute).Unix(), Hosts: []nodeconfig.KubernetesHost{{Hostname: "app.example.com", Rules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/"}}, Backends: []proxy.Backend{b}}}}}}
	alias := snapshot.Hosts[0]
	alias.Hostname = "alias.example.com"
	snapshot.Hosts = append(snapshot.Hosts, alias)
	env, _ := report.NewEnvelope("kube", snapshot, pub, key, time.Now())
	raw, _ := json.Marshal(env)
	send := func(who string) *httptest.ResponseRecorder {
		r := WithPrincipal(httptest.NewRequest("POST", "/api/node/kubernetes", bytes.NewReader(raw)), Principal{NodeID: who, Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	if w := send("spoof"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := send("kube-device"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	apps, _ := s.Applications()
	if len(apps) != 2 || apps[0].ServiceName != apps[1].ServiceName || len(apps[0].HTTPBackends) != 1 || apps[0].HTTPBackends[0].Service == "" || apps[0].AllowedGatewayNodes[0] != "kube" {
		t.Fatal(apps)
	}
	if w := send("kube-device"); w.Code != 409 {
		t.Fatal("replay", w.Code, w.Body.String())
	}
	// Endpoint loss withdraws the application; recovery must retain its ownership.
	apps[0].Enabled = false
	if err := s.UpsertApplication(apps[0]); err != nil {
		t.Fatal(err)
	}
	env, _ = report.NewEnvelope("kube", snapshot, pub, key, time.Now())
	raw, _ = json.Marshal(env)
	if w := send("kube-device"); w.Code != 200 {
		t.Fatal("recovery", w.Code, w.Body.String())
	}
	apps, _ = s.Applications()
	if len(apps) != 2 || !apps[0].Enabled {
		t.Fatal("recovery duplicated or failed to reactivate application", apps)
	}
}

func TestStaleGatewayPreservesPersistentIdentity(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("persistent", "tailnet-gateway", "persistent")
	pub, key, _ := report.GenerateKey()
	_ = s.BindNodeIdentity("persistent", encodePublicKey(pub), "stable-device", nil)
	env, _ := report.NewEnvelope("persistent", map[string]any{"expires_at": time.Now().Add(time.Minute).Unix(), "exposures": []any{}}, pub, key, time.Now())
	raw, _ := json.Marshal(env)
	if err := s.AcceptReport("persistent", env.Nonce, raw, env.PublicKey); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CleanupStaleNodes(context.Background(), -time.Hour); err != nil {
		t.Fatal(err)
	}
	n, err := s.Node("persistent")
	if err != nil || n.Revoked || n.TailnetID != "stable-device" || n.PublicKey != encodePublicKey(pub) {
		t.Fatal(n, err)
	}
}

func TestProgrammingWaitsForCurrentRouteAcknowledgment(t *testing.T) {
	c, s := testControl(t)
	a := store.ApplicationRecord{ID: "http", ServiceName: "svc:http", ServiceFQDN: "100.70.0.1", ServicePort: 8080, BackendPort: 8080, PublicHostnames: []string{"app.example.com"}, Enabled: true, HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/old"}}}}}
	if err := s.UpsertApplication(a); err != nil {
		t.Fatal(err)
	}
	_ = s.SetJSON("node_sent_config:edge-id", map[string]any{"revision": "acked", "routes": []proxy.Route{{Hostname: "app.example.com", Service: a.ServiceFQDN, Port: 8080, Rules: a.HTTPRules}}})
	_ = s.SetJSON("node_config_ack:edge-id", map[string]any{"revision": "acked", "at": time.Now().Unix()})
	if len(c.publishedGatewayHosts()) != 1 {
		t.Fatal("acknowledged route not published")
	}
	a.HTTPRules[0].Matches[0].Path = "/new"
	_ = s.UpsertApplication(a)
	if len(c.publishedGatewayHosts()) != 0 {
		t.Fatal("old Caddy config acknowledged a new route")
	}
}

func TestRevokedGatewayCleanupToleratesRemovedServices(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetSecret("tailscale_oauth_client_id", "test-id")
	_ = s.SetSecret("tailscale_oauth_client_secret", "test-secret")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			writeJSON(w, map[string]string{"access_token": "test-token"})
			return
		}
		if strings.Contains(r.URL.Path, "/services/") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/devices") {
			writeJSON(w, map[string]any{"devices": []any{}})
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
	_ = s.UpsertNode("gone", "k8s-gateway", "gone")
	_ = s.RevokeNode("gone")
	_ = s.UpsertApplication(store.ApplicationRecord{ID: "gone-app", BackendSource: "kubernetes", ServiceName: "svc:tpf-k8s-removed", ServiceFQDN: "100.70.0.1", ServicePort: 80, BackendPort: 80, AllowedGatewayNodes: []string{"gone"}})
	n, _ := s.Node("gone")
	if err := c.cleanupRevokedNode(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	var result struct{ Pending bool }
	if err := s.GetJSON("node_cleanup:gone", &result); err != nil || result.Pending {
		t.Fatal(result, err)
	}
}

func TestKubernetesEnrollmentSelectedClasses(t *testing.T) {
	d := nodeDeployment{KubernetesNamespace: "apps", GatewayClass: "private-http", IngressClass: "private-ingress"}
	if err := validateNodeDeployment(d, "k8s-gateway"); err != nil {
		t.Fatal(err)
	}
	script := helmEnrollmentScript("id", "code", "key", "control", "http://control", "gateway", "image:dev", d)
	for _, value := range []string{"namespace='apps'", "gatewayClass='private-http'", "ingressClass='private-ingress'"} {
		if !strings.Contains(script, value) {
			t.Fatal(value, script)
		}
	}
	example := selectedGatewayExample("k8s-gateway", d)
	if !strings.Contains(example, "gatewayClassName: private-http") || !strings.Contains(example, "ingressClassName: private-ingress") {
		t.Fatal(example)
	}
	if validateNodeDeployment(nodeDeployment{GatewayClass: "bad; shell"}, "k8s-gateway") == nil {
		t.Fatal("invalid class accepted")
	}
	if validateNodeDeployment(d, "docker-gateway") == nil {
		t.Fatal("Kubernetes settings accepted in Docker mode")
	}
}

func TestHelmEnrollmentUsesPinnedOCIChart(t *testing.T) {
	for _, tc := range []struct{ image, version string }{
		{"ghcr.io/tailscale-x/tailscale-private-funnel:v1.2.3", "1.2.3"},
		{"ghcr.io/tailscale-x/tailscale-private-funnel:v1.2.3-rc.1", "1.2.3-rc.1"},
		{"ghcr.io/tailscale-x/tailscale-private-funnel:dev-specialized-r12", "0.0.0-dev-specialized-r12"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			script := helmEnrollmentScript("id", "code", "auth-key", "control-key", "http://control", "gateway", tc.image, nodeDeployment{})
			for _, expected := range []string{"chart='oci://ghcr.io/tailscale-x/charts/tailscale-private-funnel'", "chart_version='" + tc.version + "'", `helm upgrade --install "$release" "$chart" --version "$chart_version"`, "image.reference='" + tc.image + "'"} {
				if !strings.Contains(script, expected) {
					t.Fatalf("missing %s", expected)
				}
			}
			if strings.Contains(script, "./charts/") || strings.Contains(script, "FUNNEL_CHART") {
				t.Fatal("checkout-dependent chart instructions")
			}
			if strings.Index(script, "helm show chart") > strings.Index(script, "kubectl apply") {
				t.Fatal("chart availability must be verified before cluster mutation")
			}
		})
	}
}
