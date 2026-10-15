package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestPolicyProvisioning(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	installTestEdge(t, s)
	_ = s.SetJSON("node_deployment:gateway-one", nodeDeployment{})
	s.UpsertNodeEnrollment("gateway-one", "enroll-one", "docker-gateway", "node")
	s.BindNodeIdentity("gateway-one", "key", "ts-one", nil)
	s.SetJSON("tailnet", map[string]string{"name": "example.ts.net", "loadbalancer_hostname": "edge.example.com"})
	s.SetSecret("tailscale_oauth_client_id", "id")
	s.SetSecret("tailscale_oauth_client_secret", "secret")
	s.SaveProvider("provider", "cloudflare", map[string]string{"api_token": "fixture"}, true)
	s.AddZone("provider", "example.com")
	s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "apps", HostnameRegexes: []string{`app[.]example[.]com`}, GatewayIDs: []string{"gateway-one"}, Enabled: true}}})
	puts := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "oauth/token") {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
			return
		}
		if r.Method == http.MethodPut {
			puts++
			var v tailscale.Service
			json.NewDecoder(r.Body).Decode(&v)
			v.Addresses = []string{"100.100.1.2", "fd7a:115c:a1e0::1"}
			json.NewEncoder(w).Encode(v)
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()
	client := tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
	c := &Control{Store: s, TailscaleClient: &client}
	h := c.Handler()
	request := func(host string, port int, identity string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"hostname": host, "backend_port": port})
		r := WithPrincipal(httptest.NewRequest("POST", "/api/node/config", bytes.NewReader(body)), Principal{NodeID: identity})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, v := range []struct {
		host     string
		port     int
		identity string
		status   int
	}{{"denied.example.com", 8080, "ts-one", 403}, {"app.example.com", 8080, "spoof", 403}, {"app.example.com", 0, "ts-one", 400}, {"app.example.com", 8080, "ts-one", 200}, {"app.example.com", 8080, "ts-one", 200}, {"app.example.com", 9090, "ts-one", 409}} {
		w := request(v.host, v.port, v.identity)
		if w.Code != v.status {
			t.Fatalf("%+v: status=%d body=%s", v, w.Code, w.Body.String())
		}
	}
	if puts != 1 {
		t.Fatalf("Service API mutations=%d", puts)
	}
	apps, _ := s.Applications()
	if len(apps) != 1 || len(apps[0].AllowedGatewayNodes) != 1 || apps[0].AllowedGatewayNodes[0] != "gateway-one" {
		t.Fatalf("applications=%+v", apps)
	}
	s.RevokeNode("gateway-one")
	if w := request("app.example.com", 8080, "ts-one"); w.Code != 403 {
		t.Fatalf("revoked=%d", w.Code)
	}
}

func TestAccountRolesAreVerifiedAndDoNotApplyToTaggedNodes(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := &Control{Store: s, roleUsers: []tailscale.User{{ID: "u1", LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "active"}}, rolesExpire: time.Now().Add(time.Minute)}
	for _, v := range []struct {
		p    Principal
		want string
	}{{Principal{UserID: "u1", Login: "owner@example.com"}, "owner"}, {Principal{UserID: "49961107773209184", Login: "owner@example.com"}, "owner"}, {Principal{UserID: "u1", Login: "spoof@example.com"}, ""}, {Principal{UserID: "u1", Login: "owner@example.com", Tagged: true}, ""}} {
		got := c.tailnetRole(t.Context(), v.p)
		if got != v.want {
			t.Fatalf("role=%q want=%q", got, v.want)
		}
	}
	for _, role := range []string{"owner", "admin", "member"} {
		a := (AccessSettings{}).permissions(Principal{TailnetRole: role})
		if a.Admin != (role == "owner") {
			t.Fatalf("role=%s access=%+v", role, a)
		}
	}
	if (AccessSettings{}).permissions(Principal{TailnetRole: "owner", Tagged: true}).Admin {
		t.Fatal("tagged node inherited owner")
	}
}

func TestOwnerUIAccessUsesTailscaleUsersAPI(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.SetJSON("access_roles", AccessSettings{Admins: []string{"node:original-admin"}})
	s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	s.SetSecret("tailscale_oauth_client_id", "id")
	s.SetSecret("tailscale_oauth_client_secret", "secret")
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "oauth/token") {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/users") || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected Users request %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"users": []tailscale.User{{ID: "u1", LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "active"}, {ID: "u2", LoginName: "member@example.com", Role: "member", Type: "member", Status: "active"}}})
	}))
	defer api.Close()
	client := tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
	h := (&Control{Store: s, TailscaleClient: &client}).Handler()
	for _, v := range []struct {
		p      Principal
		status int
	}{{Principal{NodeID: "owner-node", UserID: "49961107773209184", Login: "owner@example.com"}, 200}, {Principal{NodeID: "member-node", UserID: "u2", Login: "member@example.com"}, 403}, {Principal{NodeID: "tagged-node", UserID: "u1", Login: "owner@example.com", Tagged: true, Tags: []string{"tag:tailscale-private-funnel"}}, 403}} {
		w := httptest.NewRecorder()
		r := WithPrincipal(httptest.NewRequest("GET", "/api/access", nil), v.p)
		h.ServeHTTP(w, r)
		if w.Code != v.status {
			t.Fatalf("principal=%+v status=%d body=%s", v.p, w.Code, w.Body.String())
		}
	}
}

func TestTailnetOwnerRoleRejectsSuspendedAndSharedUsers(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, u := range []tailscale.User{
		{ID: "opaque-owner-id", LoginName: "owner@example.com", Role: "owner", Type: "shared", Status: "active"},
		{ID: "opaque-owner-id", LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "suspended"},
		{ID: "opaque-owner-id", LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "needs-approval"},
	} {
		c := &Control{Store: s, roleUsers: []tailscale.User{u}, rolesExpire: time.Now().Add(time.Minute)}
		if role := c.tailnetRole(t.Context(), Principal{UserID: "123456", Login: "owner@example.com"}); role != "" {
			t.Fatalf("granted role=%s for %+v", role, u)
		}
	}
}
