package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func settingsRequest(c *Control, path string, values url.Values, role string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r = WithPrincipal(r, Principal{NodeID: role, Login: role + "@example.com", TailnetRole: role})
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	return w
}
func TestSettingsDefaultsClearAndRejectMisleadingFields(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "management_url": "http://control", "loadbalancer_hostname": "edge.example.com"})
	c.ReleaseCheck = func(_ context.Context, v string) error {
		if v != "v1.0.2" {
			return fmt.Errorf("missing release")
		}
		return nil
	}
	good := url.Values{"section": {"deployment"}, "management_url": {" "}, "release_version": {"v1.0.2"}}
	if w := settingsRequest(c, "/settings", good, "owner"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var data map[string]any
	_ = s.GetJSON("tailnet", &data)
	if data["management_url"] != "" || data["name"] != "example.ts.net" || data["loadbalancer_hostname"] != "edge.example.com" {
		t.Fatal(data)
	}
	for _, bad := range []url.Values{
		{"section": {"deployment"}, "management_url": {"http://user:password@control"}, "release_version": {"v1.0.2"}},
		{"section": {"deployment"}, "management_url": {"http://control"}, "release_version": {"dev"}},
		{"section": {"deployment"}, "management_url": {"http://control"}, "release_version": {"v9.0.0"}},
		{"section": {"deployment"}, "management_url": {"http://control"}, "release_version": {"v1.0.2"}, "loadbalancer_public_ipv4": {"8.8.8.8"}},
	} {
		if w := settingsRequest(c, "/settings", bad, "owner"); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := settingsRequest(c, "/settings", good, "member"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
func TestOAuthTestRotatePreserveAndFailure(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net"})
	_ = s.SetSecrets(map[string]string{"tailscale_oauth_client_id": "old-id", "tailscale_oauth_client_secret": "old-secret"})
	fail := false
	gotID, gotSecret := "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			_ = r.ParseForm()
			gotID = r.FormValue("client_id")
			gotSecret = r.FormValue("client_secret")
			fmt.Fprint(w, `{"access_token":"test-token"}`)
			return
		}
		if fail {
			http.Error(w, "denied", 403)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/devices") {
			fmt.Fprint(w, `{"devices":[]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/users") {
			fmt.Fprint(w, `{"users":[]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c.TailscaleClient = &tailscale.Client{APIBase: server.URL + "/api/v2", OAuthBase: server.URL, HTTP: server.Client()}
	values := url.Values{"section": {"oauth"}, "action": {"test"}, "client_id": {"new-id"}, "client_secret": {"new-secret"}}
	if w := settingsRequest(c, "/settings", values, "owner"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	secret, _ := s.GetSecret("tailscale_oauth_client_secret")
	if secret != "old-secret" || gotID != "new-id" || gotSecret != "new-secret" {
		t.Fatal("test mutated credentials or used old credentials")
	}
	values.Set("action", "save")
	if w := settingsRequest(c, "/settings", values, "owner"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	values.Set("client_id", "")
	values.Set("client_secret", "")
	if w := settingsRequest(c, "/settings", values, "owner"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if gotID != "new-id" || gotSecret != "new-secret" {
		t.Fatal("blank replacement not preserved")
	}
	fail = true
	values.Set("client_secret", "bad-secret")
	if w := settingsRequest(c, "/settings", values, "owner"); w.Code != 502 {
		t.Fatal(w.Code)
	}
	secret, _ = s.GetSecret("tailscale_oauth_client_secret")
	if secret != "new-secret" {
		t.Fatal("failed test persisted credentials")
	}
}
func TestRemovedNodeOverrideAndReadFailure(t *testing.T) {
	c, s := testControl(t)
	_ = s.UpsertNode("one", "loadbalancer", "one")
	w := settingsRequest(c, "/api/nodes/one/settings", url.Values{"public_ipv4": {"8.8.8.8"}}, "owner")
	if w.Code != 404 && w.Code != 405 {
		t.Fatal("removed override endpoint accepted", w.Code)
	}
	_ = s.SetJSON("node_deployment:one", 123)
	node, _ := s.Node("one")
	if err := c.reconcileLoadBalancerNode(context.Background(), node); err == nil {
		t.Fatal("corrupt deployment accepted")
	}
}
func TestSettingsReadOnlyAndEnrollmentFields(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"viewer": {"node:viewer"}, "admin": {"node:admin"}}})
	for _, path := range []string{"/settings", "/loadbalancers", "/enroll/docker-gateway"} {
		r := WithPrincipal(httptest.NewRequest("GET", path, nil), Principal{NodeID: "viewer"})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 200 || strings.Contains(w.Body.String(), "<form") || strings.Contains(w.Body.String(), "Create enrollment") {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	for _, mode := range []string{"docker-gateway", "loadbalancer"} {
		r := WithPrincipal(httptest.NewRequest("GET", "/enroll/"+mode, nil), Principal{NodeID: "admin"})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		field := "loadbalancer_hostname"
		if mode == "docker-gateway" {
			field = "label_keys"
		}
		if !strings.Contains(w.Body.String(), `name="`+field+`"`) {
			t.Fatal("missing deployment hostname", mode)
		}
	}
	for _, d := range []nodeDeployment{{DenyNetworks: "["}, {LabelKeys: "bad label"}, {CertEmail: "invalid"}} {
		if validateNodeDeployment(d, "docker-gateway") == nil {
			t.Fatal("invalid deployment allowed", d)
		}
	}
}

func TestGatewayIdentityUsesHostnameAndRemovesDeploymentChoices(t *testing.T) {
	c, s := testControl(t)
	e, code, err := s.CreateEnrollment("docker-gateway", "gateway-node")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.BindEnrollment(e.ID, code, "key", "tailnode", "actual-host-gateway.example.ts.net.", nil); err != nil {
		t.Fatal(err)
	}
	n, err := s.Node(e.ID)
	if err != nil || n.Hostname != "actual-host-gateway" {
		t.Fatal(n, err)
	}
	for _, path := range []string{"/nodes/" + e.ID + "/settings", "/enroll/docker-gateway"} {
		r := WithPrincipal(httptest.NewRequest("GET", path, nil), Principal{NodeID: "owner", Login: "owner@example.com", TailnetRole: "owner"})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, removed := range []string{"Deployment choices"} {
			if strings.Contains(w.Body.String(), removed) {
				t.Fatalf("%s contains %s", path, removed)
			}
		}
	}
	for _, path := range []string{"/api/nodes/" + e.ID + "/name", "/api/enrollments/" + e.ID + "/name"} {
		if w := requestManagement(c, "PATCH", path, map[string]any{"name": "Override"}, "owner"); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
}
