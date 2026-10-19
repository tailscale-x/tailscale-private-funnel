package tailscale

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceLifecycleUsesExactAPIEndpoints(t *testing.T) {
	var paths []string
	var authKeyBody map[string]any
	var serviceBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/api/v2/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
		case strings.HasSuffix(r.URL.Path, "/tailnet/example.ts.net/keys") && r.Method == http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&authKeyBody); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"key": "tskey-test"})
		case strings.HasSuffix(r.URL.Path, "/services/svc:app") && r.Method == http.MethodDelete:
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/services/svc:app") && r.Method == http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&serviceBody); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			_ = json.NewEncoder(w).Encode(Service{Name: "svc:app", Ports: []string{"tcp:8080"}})
		case strings.HasSuffix(r.URL.Path, "/services/svc:app/devices"):
			_ = json.NewEncoder(w).Encode(map[string]any{"hosts": []ServiceHost{{NodeID: "node-1", ApprovalLevel: "not-approved"}}})
		case strings.HasSuffix(r.URL.Path, "/services/svc:app/device/node-1/approved"):
			_ = json.NewEncoder(w).Encode(map[string]bool{"approved": true})
		case r.URL.Path == "/api/v2/device/node-sidecar":
			_ = json.NewEncoder(w).Encode(Device{NodeID: "node-sidecar", Hostname: "sidecar", Authorized: true, Tags: []string{"tag:tailscale-private-funnel"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := Client{HTTP: srv.Client(), ClientID: "id", ClientSecret: "secret", Tailnet: "example.ts.net", APIBase: srv.URL + "/api/v2", OAuthBase: srv.URL}
	key, err := client.CreateAuthKey(context.Background(), "docker-gateway", "node", []string{"tag:tailscale-private-funnel"})
	if err != nil || key != "tskey-test" {
		t.Fatalf("auth key=%q err=%v", key, err)
	}
	devices := authKeyBody["capabilities"].(map[string]any)["devices"].(map[string]any)["create"].(map[string]any)
	if devices["ephemeral"] != false || devices["reusable"] != false || devices["preauthorized"] != true {
		t.Fatalf("auth key is not persistent, one-use, and preauthorized: %#v", devices)
	}
	if got, ok := authKeyBody["expirySeconds"].(float64); !ok || got != 900 {
		t.Fatalf("auth key expiry=%#v", authKeyBody["expirySeconds"])
	}
	if _, err := client.CreateService(context.Background(), Service{Name: "svc:app", Ports: []string{"tcp:8080"}, Tags: []string{"tag:tailscale-private-funnel"}}); err != nil {
		t.Fatal(err)
	}
	if tags, ok := serviceBody["tags"].([]any); !ok || len(tags) != 1 || tags[0] != "tag:tailscale-private-funnel" {
		t.Fatalf("service tags=%#v", serviceBody["tags"])
	}
	if _, err := client.ListServiceHosts(context.Background(), "svc:app"); err != nil {
		t.Fatal(err)
	}
	if approved, err := client.ApproveServiceDevice(context.Background(), "svc:app", "node-1", true); err != nil || !approved {
		t.Fatalf("approved=%v err=%v", approved, err)
	}
	device, err := client.GetDevice(context.Background(), "node-sidecar")
	if err != nil || device.NodeID != "node-sidecar" || !device.Authorized {
		t.Fatalf("device=%#v err=%v", device, err)
	}
	if err := client.DeleteService(context.Background(), "svc:app"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DELETE /api/v2/tailnet/example.ts.net/services/svc:app", "PUT /api/v2/tailnet/example.ts.net/services/svc:app", "GET /api/v2/tailnet/example.ts.net/services/svc:app/devices", "POST /api/v2/tailnet/example.ts.net/services/svc:app/device/node-1/approved"} {
		found := false
		for _, got := range paths {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s in %#v", want, paths)
		}
	}
	found := false
	for _, got := range paths {
		if got == "GET /api/v2/device/node-sidecar" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing device lookup in %#v", paths)
	}
}

func TestSingleInstallationIssuerMintsOneRolePerKey(t *testing.T) {
	issuerCalls, keyCalls := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/oauth/token" {
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			id, secret, ok := r.BasicAuth()
			if !ok {
				id = r.Form.Get("client_id")
				secret = r.Form.Get("client_secret")
			}
			if id != "installation-issuer" || secret != "issuer-secret" {
				t.Error("role-specific credential selected")
			}
			issuerCalls++
			json.NewEncoder(w).Encode(map[string]string{"access_token": "issuer-token"})
			return
		}
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/keys") {
			t.Errorf("unexpected API %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var b struct {
			Capabilities struct {
				Devices struct {
					Create struct {
						Tags                               []string `json:"tags"`
						Reusable, Ephemeral, Preauthorized bool
					} `json:"create"`
				} `json:"devices"`
			} `json:"capabilities"`
			Expiry int `json:"expirySeconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		tags := b.Capabilities.Devices.Create.Tags
		if len(tags) != 1 || tags[0] == "tag:tailscale-private-funnel-main-issuer" {
			t.Error("issuer or multiple roles leaked into auth key", tags)
		}
		if b.Expiry != 900 || b.Capabilities.Devices.Create.Reusable || !b.Capabilities.Devices.Create.Preauthorized {
			t.Error("unsafe key capabilities")
		}
		keyCalls++
		json.NewEncoder(w).Encode(map[string]string{"key": "one-use-role-key"})
	}))
	defer srv.Close()
	for _, role := range []string{"control", "loadbalancer", "docker-gateway", "tailnet-gateway", "k8s-gateway"} {
		c := Client{HTTP: srv.Client(), ClientID: "installation-issuer", ClientSecret: "issuer-secret", Tailnet: "-", APIBase: srv.URL + "/api/v2", OAuthBase: srv.URL}
		if _, err := c.CreateWorkloadAuthKey(context.Background(), role, "test", []string{"tag:tailscale-private-funnel-main-" + role}, true); err != nil {
			t.Fatal(err)
		}
	}
	if issuerCalls != 5 || keyCalls != 5 {
		t.Fatal("not all roles exercised", issuerCalls, keyCalls)
	}
}
