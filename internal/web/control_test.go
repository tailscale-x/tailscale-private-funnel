package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestSignedReportRequiresBoundTailnetIdentityAndRejectsReplay(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub, priv, err := report.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNodeEnrollment("node-1", "enrollment-1", "ingress", "ingress-one"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindNodeIdentity("node-1", encodePublicKey(pub), "tailnode-1", []string{"100.64.0.5"}); err != nil {
		t.Fatal(err)
	}
	e, err := report.NewEnvelope("node-1", map[string]any{"exposures": []any{}, "expires_at": time.Now().Add(time.Minute).Unix()}, pub, priv, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(e)
	h := (&Control{Store: s}).Handler()
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/reports", bytes.NewReader(body))
		r = WithPrincipal(r, Principal{NodeID: "tailnode-1", Hostname: "ingress-one", Addresses: []string{"100.64.0.5"}})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := request()
	if got := first.Code; got != http.StatusAccepted {
		t.Fatalf("first report status=%d body=%s", got, first.Body.String())
	}
	if got := request().Code; got != http.StatusConflict {
		t.Fatalf("replay status=%d", got)
	}
}

func TestIngressReportRequiresHostnamePolicy(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pub, priv, err := report.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNodeEnrollment("node-policy", "enrollment-policy", "ingress", "ingress-policy"); err != nil {
		t.Fatal(err)
	}
	if err := s.BindNodeIdentity("node-policy", encodePublicKey(pub), "tailnode-policy", []string{"100.64.0.5"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertApplication(store.ApplicationRecord{ID: "app-dev", ServiceName: "svc:app-dev", ServiceFQDN: "svc:app-dev", ServicePort: 18080, BackendPort: 18080, PublicHostnames: []string{"app.dev.example.com"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "dev", HostnameRegex: `[^.]+\.dev\.example\.com`, Roles: []string{"tag:ingress-dev"}, Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	reportBody := func(host string) []byte {
		e, envelopeErr := report.NewEnvelope("node-policy", map[string]any{
			"exposures":  []map[string]any{{"application_id": "app-dev", "service_name": "svc:app-dev", "public_hostname": host, "backend_hostname": "ingress-policy", "backend_ip": "100.64.0.5", "loadbalancer_hostname": "loadbalancer.example.com", "port": 18080, "reachable": true}},
			"expires_at": time.Now().Add(time.Minute).Unix(),
		}, pub, priv, time.Now())
		if envelopeErr != nil {
			t.Fatal(envelopeErr)
		}
		body, _ := json.Marshal(e)
		return body
	}
	h := (&Control{Store: s}).Handler()
	request := func(host string, tags []string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/reports", bytes.NewReader(reportBody(host)))
		r = WithPrincipal(r, Principal{NodeID: "tailnode-policy", Hostname: "ingress-policy", Tags: tags, Addresses: []string{"100.64.0.5"}})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if got := request("app.dev.example.com", []string{"tag:ingress-dev"}).Code; got != http.StatusAccepted {
		t.Fatalf("allowed report status=%d", got)
	}
	if got := request("app.prod.example.com", []string{"tag:ingress-dev"}).Code; got != http.StatusForbidden {
		t.Fatalf("hostname policy status=%d", got)
	}
}

func TestEnrollmentCommandsUseConfiguredImageAndWritableBindDirs(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetJSON("tailnet", map[string]any{
		"name":           "example.ts.net",
		"tags":           "tag:ingress",
		"management_url": "http://100.64.0.10:8080",
		"bootstrap_url":  "http://control-bootstrap:8081",
		"image":          "ghcr.io/example/funnel:test",
		"deny_networks":  "secret-*",
	}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/enrollments/ingress", bytes.NewBufferString(`{"hostname":"ingress-test"}`))
	r.Header.Set("Content-Type", "application/json")
	r = WithPrincipal(r, Principal{NodeID: "admin-node", Login: "admin@example.com"})
	w := httptest.NewRecorder()
	enrollmentTestControl(t, s).Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		DeploymentScript string `json:"deployment_script"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(response.DeploymentScript, "ingress join") || !strings.Contains(response.DeploymentScript, "enrollment-code-file") {
		t.Fatalf("deployment script does not use the native deploy flow: %s", response.DeploymentScript)
	}
}

func TestDeploymentSettingsDoNotClearOAuthCredentials(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSecret("tailscale_oauth_client_id", "client-id"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSecret("tailscale_oauth_client_secret", "client-secret"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("image=ghcr.io/example/funnel:v1.0.0&loadbalancer_hostname=loadbalancer.example.com"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = WithPrincipal(r, Principal{NodeID: "admin-node", Login: "admin@example.com"})
	w := httptest.NewRecorder()
	(&Control{Store: s}).Handler().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("settings status=%d body=%s", w.Code, w.Body.String())
	}
	for key, want := range map[string]string{
		"tailscale_oauth_client_id":     "client-id",
		"tailscale_oauth_client_secret": "client-secret",
	} {
		got, getErr := s.GetSecret(key)
		if getErr != nil || got != want {
			t.Fatalf("%s changed after deployment settings: got=%q err=%v", key, got, getErr)
		}
	}
}

func TestManagementAuthorizationUsesTailscaleRoles(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetJSON("tailnet", map[string]any{
		"name": "example.ts.net", "tags": "tag:ingress", "management_url": "http://100.64.0.10:8080", "bootstrap_url": "http://control-bootstrap:8081",
		"roles": map[string][]string{"admin": {"group:platform-admins"}, "viewer": {"group:readonly"}},
	}); err != nil {
		t.Fatal(err)
	}
	h := enrollmentTestControl(t, s).Handler()
	request := func(groups []string) int {
		r := httptest.NewRequest(http.MethodPost, "/api/enrollments/ingress", bytes.NewBufferString(`{"hostname":"ingress-role"}`))
		r.Header.Set("Content-Type", "application/json")
		r = WithPrincipal(r, Principal{NodeID: "admin-node", Login: "member@example.com", Groups: groups})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := request([]string{"group:readonly"}); got != http.StatusForbidden {
		t.Fatalf("viewer should not create enrollments: status=%d", got)
	}
	if got := request([]string{"group:platform-admins"}); got != http.StatusOK {
		t.Fatalf("admin group should create enrollments: status=%d", got)
	}
}

func TestTaggedNodeDoesNotInheritCreatorAndCapabilityCanGrantRole(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetJSON("access_roles", map[string]any{
		"admins":     []string{"owner@example.com"},
		"roles":      map[string][]string{},
		"capability": DefaultCapability,
	}); err != nil {
		t.Fatal(err)
	}
	h := (&Control{Store: s}).Handler()
	request := func(p Principal) int {
		r := httptest.NewRequest(http.MethodGet, "/settings", nil)
		r = WithPrincipal(r, p)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := request(Principal{NodeID: "tagged-node", Login: "owner@example.com", Tagged: true, Tags: []string{"tag:ingress"}}); got != http.StatusForbidden {
		t.Fatalf("tagged node inherited creator access: status=%d", got)
	}
	if got := request(Principal{NodeID: "cap-node", Tagged: true, Tags: []string{"tag:ingress"}, Capabilities: map[string][]json.RawMessage{DefaultCapability: {json.RawMessage(`{"roles":["admin"]}`)}}}); got != http.StatusOK {
		t.Fatalf("capability admin should authorize: status=%d", got)
	}
}

func TestManualIngressExposureIsDeniedByDefaultPolicy(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := (&Control{Store: s}).Handler()
	body := bytes.NewBufferString(`{"kind":"ingress","provider_id":"missing","owner_id":"ingress-1","public_hostname":"app.dev.example.com","backend_hostname":"ingress-1","backend_ip":"100.64.0.5","loadbalancer_hostname":"loadbalancer.example.com","port":18080}`)
	r := httptest.NewRequest(http.MethodPost, "/api/exposures/sync", body)
	r.Header.Set("Content-Type", "application/json")
	r = WithPrincipal(r, Principal{NodeID: "admin-node", Login: "admin@example.com"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("empty policy should deny manual exposure: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestEnrollmentPageExplainsGeneratedCommands(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := (&Control{Store: s}).Handler()
	r := httptest.NewRequest(http.MethodGet, "/enroll/ingress", nil)
	r = WithPrincipal(r, Principal{NodeID: "admin-node", Login: "admin@example.com"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("enrollment page status=%d body=%s", w.Code, w.Body.String())
	}
	for _, text := range []string{"Ingress enrollment", "One-copy native deployment", "Exposure policy:"} {
		if !strings.Contains(w.Body.String(), text) {
			t.Fatalf("enrollment page missing %q", text)
		}
	}
}

func TestRedesignedPagesRenderSharedShellForAdministrator(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetJSON("access_roles", map[string]any{"admins": []string{"admin@example.com"}, "roles": map[string][]string{}, "capability": DefaultCapability}); err != nil {
		t.Fatal(err)
	}
	h := (&Control{Store: s}).Handler()
	for _, path := range []string{"/", "/settings", "/access", "/providers", "/zones", "/applications", "/loadbalancers", "/ingresses", "/policies", "/exposures", "/operations", "/enroll/loadbalancer", "/enroll/ingress"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r = WithPrincipal(r, Principal{NodeID: "admin-node", Login: "admin@example.com"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if !strings.Contains(body, `class="pf-app"`) || !strings.Contains(body, `aria-label="Primary navigation"`) {
			t.Fatalf("%s missing shared shell", path)
		}
		if !strings.Contains(body, "Local Funnel") || !strings.Contains(body, "/assets/brand/local-funnel-logo.png") {
			t.Fatalf("%s missing Local Funnel branding", path)
		}
	}
}

func TestPolicyRuleEditorPersistsGuidedRule(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := (&Control{Store: s}).Handler()
	r := httptest.NewRequest(http.MethodPost, "/api/policies/rule", strings.NewReader("name=dev&hostname_regex=%5B%5E.%5D%2B%5C.dev%5C.example%5C.com&roles=tag%3Aingress-dev&loadbalancer_hostnames=loadbalancer.example.com&enabled=on"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	r = WithPrincipal(r, Principal{NodeID: "admin-node", Login: "admin@example.com"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("rule status=%d body=%s", w.Code, w.Body.String())
	}
	document := (&Control{Store: s}).loadExposurePolicy()
	if len(document.Rules) != 1 || document.Rules[0].Name != "dev" {
		t.Fatalf("guided rule not persisted: %#v", document)
	}
}

func encodePublicKey(key []byte) string {
	return base64.RawStdEncoding.EncodeToString(key)
}

func enrollmentTestControl(t *testing.T, s *store.Store) *Control {
	t.Helper()
	for key, value := range map[string]string{"tailscale_oauth_client_id": "test-id", "tailscale_oauth_client_secret": "test-secret"} {
		if err := s.SetSecret(key, value); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/keys") {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
			}
			create := payload["capabilities"].(map[string]any)["devices"].(map[string]any)["create"].(map[string]any)
			if create["ephemeral"] != false || create["reusable"] != false || create["preauthorized"] != true || payload["expirySeconds"] != float64(900) {
				t.Errorf("invalid bootstrap key settings: %v", payload)
			}
			json.NewEncoder(w).Encode(map[string]string{"key": "tskey-bootstrap-test"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	return &Control{Store: s, TailscaleClient: &tailscale.Client{HTTP: server.Client(), ClientID: "test-id", ClientSecret: "test-secret", Tailnet: "example.ts.net", APIBase: server.URL + "/api/v2", OAuthBase: server.URL}}
}
