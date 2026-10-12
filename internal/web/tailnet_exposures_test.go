package web

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func TestTailnetExposureWorkflow(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	secret, _ := s.GetSecret("test-edge-key")
	keyBytes, _ := base64.RawStdEncoding.DecodeString(secret)
	priv := ed25519.PrivateKey(keyBytes)
	pub := priv.Public().(ed25519.PublicKey)
	public := base64.RawStdEncoding.EncodeToString(pub)
	_ = s.UpsertNodeEnrollment("edge-id", "enrolled", "loadbalancer", "edge")
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{})
	_ = s.BindNodeIdentity("edge-id", public, "edge-tailnet", nil)
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{})
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net", "loadbalancer_hostname": "edge.example.com"})
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"admin": {"node:admin"}, "viewer": {"node:viewer"}}})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "allowed", HostnameRegexes: []string{`app[.]example[.]com`}, Enabled: true}}})
	_ = s.SetSecret("tailscale_oauth_client_id", "id")
	_ = s.SetSecret("tailscale_oauth_client_secret", "secret")
	snapshot := nodeconfig.PeerSnapshot{Sources: []string{"tailnet"}, Peers: []nodeconfig.Peer{{ID: "backend", Hostname: "backend", Addresses: []string{"100.70.0.1"}}}, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
	send := func(snapshot nodeconfig.PeerSnapshot, identity string) (*httptest.ResponseRecorder, report.Envelope) {
		env, _ := report.NewEnvelope("edge-id", snapshot, pub, priv, time.Now())
		raw, _ := json.Marshal(env)
		r := WithPrincipal(httptest.NewRequest("POST", "/api/node/peers", bytes.NewReader(raw)), Principal{NodeID: identity, Tags: []string{"tag:tailscale-private-funnel", "tag:backend-access"}, Tagged: true})
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w, env
	}
	if w, _ := send(snapshot, "spoof"); w.Code != 403 {
		t.Fatal("identity spoof", w.Code)
	}
	w, env := send(snapshot, "edge-tailnet")
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	raw, _ := json.Marshal(env)
	r := WithPrincipal(httptest.NewRequest("POST", "/api/node/peers", bytes.NewReader(raw)), Principal{NodeID: "edge-tailnet"})
	w = httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatal("replay accepted", w.Code)
	}
	if w = requestManagement(c, "GET", "/api/nodes/edge-id/peers", nil, "owner"); w.Code != 200 || !strings.Contains(w.Body.String(), "backend-access") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = requestManagement(c, "POST", "/api/nodes/edge-id/probes", map[string]any{"device_id": "backend", "port": 8080}, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var probe nodeconfig.Probe
	_ = json.Unmarshal(w.Body.Bytes(), &probe)
	snapshot.Results = []nodeconfig.ProbeResult{{ID: probe.ID, DeviceID: "backend", Port: 8080, Reachable: true, CheckedAt: time.Now().Unix()}}
	if w, _ = send(snapshot, "edge-tailnet"); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	input := map[string]any{"discovery_loadbalancer_id": "edge-id", "device_id": "backend", "hostname": "app.example.com", "port": 8080, "enabled": true, "probe_id": probe.ID}
	denied := map[string]any{}
	for k, v := range input {
		denied[k] = v
	}
	denied["hostname"] = "denied.example.com"
	if w = requestManagement(c, "POST", "/api/tailnet-exposures", denied, "owner"); w.Code != 403 {
		t.Fatal("policy denied", w.Code, w.Body.String())
	}
	viewerBody, _ := json.Marshal(input)
	viewerRequest := WithPrincipal(httptest.NewRequest("POST", "/api/tailnet-exposures", bytes.NewReader(viewerBody)), Principal{NodeID: "viewer", Login: "viewer@example.com"})
	viewerRequest.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	c.Handler().ServeHTTP(w, viewerRequest)
	if w.Code != 403 {
		t.Fatal("viewer mutation", w.Code)
	}
	w = requestManagement(c, "POST", "/api/tailnet-exposures", input, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	apps, _ := s.Applications()
	if len(apps) != 1 || apps[0].BackendSource != "tailnet" || apps[0].BackendDeviceID != "backend" {
		t.Fatal(apps)
	}
	app := apps[0]
	input["revision"] = app.PolicyRevision
	input["hostname"] = "app.example.com"
	w = requestManagement(c, "PUT", "/api/tailnet-exposures/"+app.ID, input, "owner")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	apps, _ = s.Applications()
	if len(apps) != 1 {
		t.Fatal("edit duplicated assignment")
	}
	if w = requestManagement(c, "PUT", "/api/tailnet-exposures/"+app.ID, input, "owner"); w.Code != 409 {
		t.Fatal("stale edit", w.Code)
	}
	input["revision"] = apps[0].PolicyRevision
	input["enabled"] = false
	if w = requestManagement(c, "PUT", "/api/tailnet-exposures/"+app.ID, input, "owner"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	apps, _ = s.Applications()
	if apps[0].Enabled {
		t.Fatal("disable failed")
	}
	if w = requestManagement(c, "DELETE", "/api/tailnet-exposures/"+app.ID, map[string]any{"revision": apps[0].PolicyRevision}, "owner"); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = requestManagement(c, "DELETE", "/api/tailnet-exposures/"+app.ID, map[string]any{"revision": apps[0].PolicyRevision}, "owner"); w.Code != 404 {
		t.Fatal("deleted exposure accepted again", w.Code)
	}
	if w = requestManagement(c, "DELETE", "/api/tailnet-exposures/remote-000000000000000000000000", map[string]any{"revision": 0}, "owner"); w.Code != 404 {
		t.Fatal("unowned Service deletion", w.Code)
	}
	snapshot.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	if w, _ = send(snapshot, "edge-tailnet"); w.Code != 400 {
		t.Fatal("expired peers accepted", w.Code)
	}
	_ = s.RevokeNode("edge-id")
	snapshot.ExpiresAt = time.Now().Add(time.Minute).Unix()
	if w, _ = send(snapshot, "edge-tailnet"); w.Code != 403 {
		t.Fatal("revoked snapshot", w.Code)
	}
}
