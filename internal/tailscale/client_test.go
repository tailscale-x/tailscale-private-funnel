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
	key, err := client.CreateAuthKey(context.Background(), "ingress", "node", []string{"tag:tailscale-private-funnel"})
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
