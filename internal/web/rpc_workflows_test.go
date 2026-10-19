package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libdns/libdns"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"google.golang.org/protobuf/proto"
)

func rpcServer(t *testing.T, c *Control, p Principal) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { c.controlHandler().ServeHTTP(w, WithPrincipal(r, p)) }))
	t.Cleanup(server.Close)
	return server
}
func managementClient(t *testing.T, c *Control, p Principal) funnelv1connect.ManagementServiceClient {
	s := rpcServer(t, c, p)
	return funnelv1connect.NewManagementServiceClient(s.Client(), s.URL)
}
func nodeClient(t *testing.T, c *Control, p Principal) funnelv1connect.NodeServiceClient {
	n, e := c.Store.NodeByTailnetID(p.NodeID)
	if e == nil {
		reg, _ := c.Store.ComponentRegistration(n.ID)
		if len(p.Tags) == 0 || len(p.Tags) == 1 && p.Tags[0] == "tag:tailscale-private-funnel" {
			p.Tags = []string{identity.Tag("main", reg.Role())}
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e == nil {
			r.Header.Set("Funnel-Component-ID", n.ID)
			r.Header.Set("Funnel-Instance-ID", "test-"+n.ID)
		}
		c.controlHandler().ServeHTTP(w, WithPrincipal(r, p))
	}))
	t.Cleanup(server.Close)
	return funnelv1connect.NewNodeServiceClient(server.Client(), server.URL)

}
func typedMessage[T proto.Message](value T) T { return value }

func TestRPCPolicyLifecycleAndImmediateWithdrawal(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	client := managementClient(t, c, Principal{NodeID: "admin"})
	d, err := client.GetPolicy(t.Context(), connect.NewRequest(&v1.Empty{}))
	if err != nil {
		t.Fatal(err)
	}
	rule := &v1.Rule{Name: "Development", HostnameRegexes: []string{`app[.]example[.]com`, `other[.]example[.]com`}, Enabled: true}
	saved, err := client.SaveRule(t.Context(), connect.NewRequest(&v1.SaveRuleRequest{Revision: d.Msg.Revision, Rule: rule}))
	if err != nil {
		t.Fatal(err)
	}
	current := saved.Msg.Policy
	rule = proto.Clone(current.Rules[len(current.Rules)-1]).(*v1.Rule)
	id := rule.Id
	rule.Name = "Renamed"
	saved, err = client.SaveRule(t.Context(), connect.NewRequest(&v1.SaveRuleRequest{Revision: current.Revision, Rule: rule}))
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Msg.Policy.Rules) != len(current.Rules) || saved.Msg.Policy.Rules[len(current.Rules)-1].Id != id {
		t.Fatal("rule edit duplicated identity")
	}
	if _, err = client.SaveRule(t.Context(), connect.NewRequest(&v1.SaveRuleRequest{Revision: current.Revision, Rule: rule})); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale edit: %v", err)
	}
	if _, err = client.ValidatePolicy(t.Context(), connect.NewRequest(saved.Msg.Policy)); err != nil {
		t.Fatal(err)
	}
	if c.loadExposurePolicy().Revision != saved.Msg.Policy.Revision {
		t.Fatal("validation mutated policy")
	}
	result, err := client.TestPolicy(t.Context(), connect.NewRequest(&v1.PolicyTestRequest{Hostname: "app.example.com"}))
	if err != nil || !result.Msg.Allowed {
		t.Fatal(result, err)
	}
	zoneID := testZoneID(t, s, "example.com")
	if _, err = client.ApplyDNS(t.Context(), connect.NewRequest(&v1.DNSRequest{ZoneId: zoneID})); err != nil {
		t.Fatal(err)
	}
	if len(f.records["example.com"]) != 1 {
		t.Fatal(f.records)
	}
	impact, err := client.PreviewPolicy(t.Context(), connect.NewRequest(&v1.Policy{Revision: saved.Msg.Policy.Revision}))
	if err != nil || len(impact.Msg.AffectedHostnames) != 1 {
		t.Fatal(impact, err)
	}
	if _, err = client.SavePolicy(t.Context(), connect.NewRequest(&v1.Policy{Revision: saved.Msg.Policy.Revision})); err != nil {
		t.Fatal(err)
	}
	if len(f.records["example.com"]) != 0 {
		t.Fatal("denied exposure DNS was not withdrawn")
	}
}
func TestRPCProviderDiscoveryAssignmentsAndCleanupGuards(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com")
	client := managementClient(t, c, Principal{NodeID: "admin"})
	discovered, err := client.DiscoverZones(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "fixture"}))
	if err != nil || len(discovered.Msg.Items) != 3 {
		t.Fatal(discovered, err)
	}
	zones, _ := s.Zones()
	if len(zones) != 1 {
		t.Fatal("discovery mutated assignments")
	}
	for _, domain := range []string{"example.net", "example.org"} {
		if _, err = client.AddZone(t.Context(), connect.NewRequest(&v1.AddZoneRequest{ProviderId: "fixture", Domain: domain})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = client.AddZone(t.Context(), connect.NewRequest(&v1.AddZoneRequest{ProviderId: "fixture", Domain: "example.net"})); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err = client.AddZone(t.Context(), connect.NewRequest(&v1.AddZoneRequest{ProviderId: "fixture", Domain: "unknown.example"})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("undiscovered: %v", err)
	}
	f.fail = "discovery"
	if _, err = client.DiscoverZones(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "fixture"})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	f.fail = ""
	f.zones = []libdns.Zone{{Name: "example.net"}, {Name: "example.net."}}
	if _, err = client.AddZone(t.Context(), connect.NewRequest(&v1.AddZoneRequest{ProviderId: "fixture", Domain: "example.net"})); err == nil {
		t.Fatal("ambiguous discovery accepted")
	}
	f.zones = nil
	id := testZoneID(t, s, "example.com")
	_ = s.ReplaceLedger("fixture", "example.com", "gateway:private", []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "edge.example.com"}})
	if _, err = client.DeleteZone(t.Context(), connect.NewRequest(&v1.ResourceID{Id: id})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("owned zone deletion", err)
	}
	if _, err = client.DeleteProvider(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "fixture"})); err == nil {
		t.Fatal("assigned provider deleted")
	}
	if _, err = client.EnableProvider(t.Context(), connect.NewRequest(&v1.EnableRequest{Id: "fixture", Enabled: false})); err != nil {
		t.Fatal(err)
	}
	p, cfg, _ := s.ProviderConfig("fixture")
	if p.Enabled || cfg["api_token"] != "secret" {
		t.Fatal("disable lost credentials")
	}
}
func TestRPCDNSApprovalBoundToPreviewAndZone(t *testing.T) {
	c, s := testControl(t)
	f := installDNSFixture(t, s, "example.com", "example.net")
	zoneReportFixture(t, c, s)
	f.records["example.com"] = []libdns.Record{libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "unmanaged.example.net."}}
	f.records["example.net"] = []libdns.Record{libdns.RR{Name: "other.example.net", Type: "TXT", Data: "keep"}}
	client := managementClient(t, c, Principal{NodeID: "admin"})
	request := &v1.DNSRequest{ZoneId: testZoneID(t, s, "example.com")}
	preview, err := client.PreviewDNS(t.Context(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Msg.Results) != 1 || !preview.Msg.Results[0].RequiresApproval || preview.Msg.Results[0].ApprovalToken == "" {
		t.Fatal(preview.Msg)
	}
	applied, err := client.ApplyDNS(t.Context(), connect.NewRequest(request))
	if err != nil || !applied.Msg.Pending {
		t.Fatal("unowned overwrite accepted", applied, err)
	}
	old := preview.Msg.Results[0]
	request.Approvals = map[string]string{old.ResourceId: old.ApprovalToken}
	f.records["example.com"][0] = libdns.RR{Name: "app.example.com", Type: "CNAME", Data: "changed.example.net."}
	applied, err = client.ApplyDNS(t.Context(), connect.NewRequest(request))
	if err != nil || !applied.Msg.Pending {
		t.Fatal("stale preview accepted", applied, err)
	}
	request.Approvals = nil
	preview, err = client.PreviewDNS(t.Context(), connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	request.Approvals = map[string]string{preview.Msg.Results[0].ResourceId: preview.Msg.Results[0].ApprovalToken}
	applied, err = client.ApplyDNS(t.Context(), connect.NewRequest(request))
	if err != nil || applied.Msg.Pending {
		t.Fatal(applied, err)
	}
	if len(f.records["example.net"]) != 1 || f.records["example.net"][0].RR().Data != "keep" {
		t.Fatal("independent zone modified")
	}
	_ = s.SetJSON("exposure_policy", policy.Document{})
	request.Approvals = nil
	if _, err = client.ApplyDNS(t.Context(), connect.NewRequest(request)); err != nil {
		t.Fatal(err)
	}
	if len(f.records["example.com"]) != 0 || len(f.records["example.net"]) != 1 {
		t.Fatal("ownership cleanup crossed zone", f.records)
	}
}
func TestRPCNodeReportIdentityReplayExpiryRevocationAndConfiguration(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)

	_ = s.UpsertNode("test-gateway", "docker-gateway", "test-gateway")
	_ = bindTestNode(s, "test-gateway", "tail-test-gateway", nil)
	_ = s.SetJSON("node_deployment:test-gateway", nodeDeployment{})
	client := nodeClient(t, c, Principal{NodeID: "tail-test-gateway", Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
	config, err := client.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = nodeconfig.FromPayload(config.Msg, "test-gateway", time.Now()); err != nil {
		t.Fatal(err)
	}
	message := typedMessage(&v1.ExposureReport{ExpiresAt: time.Now().Add(time.Minute).Unix()})
	spoof := nodeClient(t, c, Principal{NodeID: "other"})
	if _, err = spoof.SubmitReport(t.Context(), connect.NewRequest(message)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("spoof", err)
	}
	if _, err = client.SubmitReport(t.Context(), connect.NewRequest(message)); err != nil {
		t.Fatal(err)
	}
	expired := typedMessage(&v1.ExposureReport{ExpiresAt: time.Now().Add(-time.Minute).Unix()})
	if _, err = client.SubmitReport(t.Context(), connect.NewRequest(expired)); err == nil {
		t.Fatal("expired report accepted")
	}
	_ = s.RevokeNode("test-gateway")
	if _, err = client.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("revoked", err)
	}
}
func TestRPCPublicEndpointServiceBindingAndRetention(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")

	_ = s.UpsertNode("lb", "loadbalancer", "lb")
	_ = bindTestNode(s, "lb", "tail-lb", nil)
	d := nodeDeployment{AddressSource: "kubernetes", DeploymentTarget: "kubernetes", KubernetesNamespace: "system", PublicService: "edge-public", LoadBalancerHostname: "edge.example.com"}
	_ = s.SetJSON("node_deployment:lb", d)
	_ = s.SetJSON("node_health:lb", loadBalancerHealth{ReceivedAt: time.Now().Unix()})
	client := nodeClient(t, c, Principal{NodeID: "tail-lb"})
	payload := &v1.PublicEndpointPayload{Namespace: "system", Service: "edge-public", Uid: "service-uid", ObservedAt: time.Now().Unix(), Ingress: []*v1.PublicIngress{{Ip: "8.8.8.8"}}}
	message := typedMessage(payload)
	if _, err := client.SubmitPublicEndpoint(t.Context(), connect.NewRequest(message)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubmitPublicEndpoint(t.Context(), connect.NewRequest(message)); err != nil {
		t.Fatal("idempotent report repeat", err)
	}
	n, _ := s.Node("lb")
	ips, err := c.kubernetesAddresses(t.Context(), n, d)
	if err != nil || len(ips) != 1 {
		t.Fatal(ips, err)
	}
	_ = s.ReplaceLedger("fixture", "example.com", "loadbalancer:lb", []libdns.Record{libdns.RR{Name: "edge.example.com", Type: "A", Data: "8.8.8.8"}})
	payload.Ingress = []*v1.PublicIngress{{Ip: "10.0.0.1"}}
	if _, err = client.SubmitPublicEndpoint(t.Context(), connect.NewRequest(typedMessage(payload))); err != nil {
		t.Fatal(err)
	}
	if _, err = c.kubernetesAddresses(t.Context(), n, d); err == nil {
		t.Fatal("private IP accepted")
	}
	ledger, _ := s.Ledger("fixture", "example.com", "loadbalancer:lb")
	if len(ledger) != 1 {
		t.Fatal("prior owned A lost")
	}
	payload.Uid = "replaced"
	if _, err = client.SubmitPublicEndpoint(t.Context(), connect.NewRequest(typedMessage(payload))); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("Service identity changed", err)
	}
}
func TestRPCShellDocsAndRemovedEndpoints(t *testing.T) {
	c, _ := testControl(t)
	server := rpcServer(t, c, Principal{NodeID: "admin"})
	for _, path := range []string{"/", "/providers", "/zones", "/settings", "/gateways/new", "/tailnet-exposures", "/docs", "/llms.txt", "/openapi.json"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("%s: %d", path, response.StatusCode)
		}
	}
	// The retired REST paths must not fall through to the SPA shell.
	for _, path := range []string{"/api/providers", "/api/node/config", "/api/enrollments/docker-gateway"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			t.Fatalf("retired endpoint %s: %d", path, response.StatusCode)
		}
	}
}
func TestRPCStrictUnknownFieldsAndOrigin(t *testing.T) {
	c, _ := testControl(t)
	server := rpcServer(t, c, Principal{NodeID: "admin"})
	for _, tc := range []struct {
		body, origin string
		status       int
	}{{`{"provider_id":"fixture","zone":"example.com"}`, "", 400}, {`{}`, "https://untrusted.example", 403}} {
		request, _ := http.NewRequestWithContext(context.Background(), "POST", server.URL+funnelv1connect.ManagementServiceAddZoneProcedure, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		if tc.origin != "" {
			request.Header.Set("Origin", tc.origin)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatalf("unknown field/origin: %d", response.StatusCode)
		}
	}
}
func TestRPCControlInstructionsDoNotExposeCredentials(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]any{"name": "preview.ts.net", "management_url": "http://control:8080", "release_version": "v2.0.0"})
	server := rpcServer(t, c, Principal{NodeID: "admin"})
	client := funnelv1connect.NewDeploymentServiceClient(server.Client(), server.URL)

	result, e := client.Instructions(t.Context(), connect.NewRequest(&v1.DeploymentRequest{Mode: "control", Target: "kubernetes", ResourceName: "other", Context: "fixture", Tailnet: "preview.ts.net"}))
	if e != nil {
		t.Fatal(e)
	}

	if result.Msg.Credentials != nil {
		t.Fatal("instructions minted enrollment")
	}
	if strings.Contains(result.Msg.Script, "helm upgrade") || !strings.Contains(result.Msg.Script, "k8s") {
		t.Fatal(result.Msg.Script)
	}
}

func TestRPCTailnetPeerProbeExposureLifecycle(t *testing.T) {
	_, s := testControl(t)
	c := registrationTestControl(t, s)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Apps", HostnameRegexes: []string{`app[.]example[.]com`}, Enabled: true}}})

	_ = s.UpsertNode("tail-gateway", "tailnet-gateway", "tail-gateway")
	_ = bindTestNode(s, "tail-gateway", "tail-device", nil)
	_ = s.SetJSON("node_deployment:tail-gateway", nodeDeployment{})
	node := nodeClient(t, c, Principal{NodeID: "tail-device", Tags: []string{"tag:tailscale-private-funnel-main-tailnet-gateway", "tag:backend-access"}, Tagged: true})
	admin := managementClient(t, c, Principal{NodeID: "admin"})
	snapshot := &v1.PeerSnapshot{Sources: []string{"tailnet"}, Peers: []*v1.Peer{{Id: "backend", Hostname: "backend", Addresses: []string{"100.70.0.1"}}}, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	message := typedMessage(snapshot)
	if _, err := node.SubmitPeers(t.Context(), connect.NewRequest(message)); err != nil {
		t.Fatal(err)
	}
	if _, err := node.SubmitPeers(t.Context(), connect.NewRequest(message)); err != nil {
		t.Fatal("peer snapshot refresh", err)
	}
	peers, err := admin.GetPeers(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "tail-gateway"}))
	if err != nil || !contains(peers.Msg.Tags, "tag:backend-access") {
		t.Fatal("effective tags", peers, err)
	}
	probe, err := admin.ProbeBackend(t.Context(), connect.NewRequest(&v1.ProbeRequest{NodeId: "tail-gateway", DeviceId: "backend", Port: 8080}))
	if err != nil {
		t.Fatal(err)
	}
	input := &v1.TailnetExposureRequest{NodeId: "tail-gateway", BackendDeviceId: "backend", BackendPort: 8080, PublicHostname: "app.example.com", Name: "App", Enabled: true, ProbeId: probe.Msg.Id}
	if _, err = admin.SaveTailnetExposure(t.Context(), connect.NewRequest(input)); err == nil {
		t.Fatal("untested accessibility accepted")
	}
	snapshot.Results = []*v1.ProbeResult{{Id: probe.Msg.Id, DeviceId: "backend", Port: 8080, Reachable: true, CheckedAt: time.Now().Unix()}}
	if _, err = node.SubmitPeers(t.Context(), connect.NewRequest(typedMessage(snapshot))); err != nil {
		t.Fatal(err)
	}
	input.PublicHostname = "denied.example.com"
	if _, err = admin.SaveTailnetExposure(t.Context(), connect.NewRequest(input)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("policy bypass", err)
	}
	input.PublicHostname = "app.example.com"
	app, err := admin.SaveTailnetExposure(t.Context(), connect.NewRequest(input))
	if err != nil {
		t.Fatal(err)
	}
	input.Id = app.Msg.Id
	input.Revision = app.Msg.Revision
	input.Name = "New display name"
	edited, err := admin.SaveTailnetExposure(t.Context(), connect.NewRequest(input))
	if err != nil || edited.Msg.Id != app.Msg.Id {
		t.Fatal("edit changed identity", err)
	}
	if _, err = admin.SaveTailnetExposure(t.Context(), connect.NewRequest(input)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("stale exposure edit", err)
	}
	input.Revision = edited.Msg.Revision
	input.Enabled = false
	disabled, err := admin.SaveTailnetExposure(t.Context(), connect.NewRequest(input))
	if err != nil || disabled.Msg.Enabled {
		t.Fatal("disable", err)
	}
	if _, err = admin.DeleteTailnetExposure(t.Context(), connect.NewRequest(&v1.ResourceRevision{Id: input.Id, Revision: disabled.Msg.Revision})); err != nil {
		t.Fatal(err)
	}
	snapshot.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	if _, err = node.SubmitPeers(t.Context(), connect.NewRequest(typedMessage(snapshot))); err == nil {
		t.Fatal("expired snapshot accepted")
	}
}

// Fixture vendor servers use ordinary JSON; the application API is generated RPC.
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestRPCDefaultOwnerAndCapabilityAuthorization(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("access_roles", AccessSettings{})
	c.rolesExpire = time.Now().Add(time.Hour)
	c.roleUsers = []tailscale.User{{LoginName: "owner@example.com", Role: "owner", Type: "member", Status: "active"}, {LoginName: "admin@example.com", Role: "admin", Type: "member", Status: "active"}}
	for _, tc := range []struct {
		principal Principal
		allowed   bool
	}{
		{Principal{NodeID: "owner", UserID: "1", Login: "owner@example.com"}, true},
		{Principal{NodeID: "admin", UserID: "2", Login: "admin@example.com"}, false},
		{Principal{NodeID: "tagged", Login: "owner@example.com", Tagged: true}, false},
		{Principal{NodeID: "spoof", Groups: []string{"autogroup:owner"}}, false},
		{Principal{NodeID: "capability", Tagged: true, Capabilities: map[string][]json.RawMessage{DefaultCapability: {json.RawMessage(`{"roles":["viewer"]}`)}}}, true},
	} {
		client := managementClient(t, c, tc.principal)
		_, err := client.WhoAmI(t.Context(), connect.NewRequest(&v1.Empty{}))
		if (err == nil) != tc.allowed {
			t.Fatalf("role authorization %s: %v", tc.principal.NodeID, err)
		}
	}
	settings, err := c.loadAccessSettings()
	if err != nil || strings.Join(settings.Roles["admin"], ",") != "autogroup:owner" || len(settings.Admins) > 0 {
		t.Fatal("default role changed or visitor claimed ownership", settings, err)
	}
}
func TestRPCOAuthRotationTestingAndDefaultsPreserveSecrets(t *testing.T) {
	c, s := testControl(t)
	_ = s.SetJSON("tailnet", map[string]any{"name": "example.ts.net", "management_url": "http://old-control", "release_version": "v2.0.0"})
	_ = s.SetSecrets(map[string]string{"tailscale_users_client_id": "old-id", "tailscale_users_client_secret": "old-secret"})
	fail := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/oauth/token") {
			writeJSON(w, map[string]string{"access_token": "test-token"})
			return
		}
		if fail {
			http.Error(w, "denied", 403)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/devices") {
			writeJSON(w, map[string]any{"devices": []any{}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/users") {
			writeJSON(w, map[string]any{"users": []any{}})
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()
	c.TailscaleClient = &tailscale.Client{HTTP: api.Client(), APIBase: api.URL, OAuthBase: api.URL}
	c.ReleaseCheck = func(context.Context, string) error { return nil }
	client := managementClient(t, c, Principal{NodeID: "admin"})
	if _, err := client.TestOAuth(t.Context(), connect.NewRequest(&v1.OAuthRequest{Purpose: "users", ClientId: "new-id", ClientSecret: "new-secret"})); err != nil {
		t.Fatal(err)
	}
	secret, _ := s.GetSecret("tailscale_users_client_secret")
	if secret != "old-secret" {
		t.Fatal("test mutated credentials")
	}
	if _, err := client.SaveOAuth(t.Context(), connect.NewRequest(&v1.OAuthRequest{Purpose: "users", ClientId: "new-id", ClientSecret: "new-secret"})); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SaveOAuth(t.Context(), connect.NewRequest(&v1.OAuthRequest{Purpose: "users"})); err != nil {
		t.Fatal("blank replacement failed", err)
	}
	fail = true
	if _, err := client.SaveOAuth(t.Context(), connect.NewRequest(&v1.OAuthRequest{Purpose: "users", ClientSecret: "bad-secret"})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
	secret, _ = s.GetSecret("tailscale_users_client_secret")
	if secret != "new-secret" {
		t.Fatal("failed rotation damaged credentials")
	}
	if _, err := client.SaveDefaults(t.Context(), connect.NewRequest(&v1.DeploymentDefaults{ManagementUrl: "http://control:8080", ReleaseVersion: "v2.0.0"})); err != nil {
		t.Fatal(err)
	}
	secret, _ = s.GetSecret("tailscale_users_client_secret")
	if secret != "new-secret" {
		t.Fatal("defaults cleared OAuth")
	}
	settings, err := client.GetSettings(t.Context(), connect.NewRequest(&v1.Empty{}))
	if err != nil || strings.Contains(settings.Msg.String(), "new-secret") {
		t.Fatal("secret rendered", err)
	}
}

func TestRPCKubernetesDesiredStateBindingReplayAndRecovery(t *testing.T) {
	_, s := testControl(t)
	c := registrationTestControl(t, s)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Kubernetes", HostnameRegexes: []string{`(?:app|alias)[.]example[.]com`}, Enabled: true}}})

	_ = s.UpsertNode("kube", "k8s-gateway", "kube")
	_ = bindTestNode(s, "kube", "kube-device", nil)
	backend := &v1.HTTPBackend{Key: "service:8080", Address: "10.43.0.1:8080", Port: 8080, Weight: 1, Endpoints: []*v1.PodEndpoint{{Address: "10.42.0.5:8080", PodUid: "pod-uid", NodeName: "node", NodeUid: "node-uid"}}}
	snapshot := &v1.KubernetesPayload{ClusterId: "cluster", ExpiresAt: time.Now().Add(time.Minute).Unix(), Hosts: []*v1.KubernetesHost{{Hostname: "app.example.com", Rules: []*v1.HTTPRule{{Matches: []*v1.HTTPMatch{{Path: "/"}}, Backends: []*v1.HTTPBackend{backend}}}}, {Hostname: "alias.example.com", Rules: []*v1.HTTPRule{{Matches: []*v1.HTTPMatch{{Path: "/"}}, Backends: []*v1.HTTPBackend{backend}}}}}}
	client := nodeClient(t, c, Principal{NodeID: "kube-device", Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
	message := typedMessage(snapshot)
	if _, err := client.SubmitKubernetes(t.Context(), connect.NewRequest(message)); err != nil {
		t.Fatal(err)
	}
	apps, err := s.Applications()
	if err != nil || len(apps) != 2 || apps[0].ServiceName != apps[1].ServiceName || len(apps[0].HTTPBackends) != 1 || len(apps[0].HTTPBackends[0].Endpoints) != 1 || apps[0].AllowedGatewayNodes[0] != "kube" {
		t.Fatal("translation changed routing/ownership", apps, err)
	}
	if _, err = client.SubmitKubernetes(t.Context(), connect.NewRequest(message)); err != nil {
		t.Fatal("idempotent report repeat", err)
	}
	// Routine snapshots reuse control-owned Service bindings without REST calls.
	// A supplied target must never replace that trusted binding.
	backend.Service = "attacker.invalid"
	called := make(chan struct{}, 1)
	c.TailscaleClient.HTTP = &http.Client{Transport: reportStallTransport{called}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := client.SubmitKubernetes(ctx, connect.NewRequest(typedMessage(snapshot))); err != nil {
		t.Fatal("unchanged snapshot depended on external API", err)
	}
	select {
	case <-called:
		t.Fatal("unchanged snapshot called external API")
	default:
	}
	cached, _ := s.Applications()
	if cached[0].HTTPBackends[0].Service != apps[0].HTTPBackends[0].Service {
		t.Fatal("untrusted target replaced binding")
	}
	apps[0].Enabled = false
	_ = s.UpsertApplication(apps[0])
	if _, err = client.SubmitKubernetes(t.Context(), connect.NewRequest(typedMessage(snapshot))); err != nil {
		t.Fatal("recovery", err)
	}
	recovered, _ := s.Applications()
	if len(recovered) != 2 || recovered[0].ID != apps[0].ID || !recovered[0].Enabled {
		t.Fatal("recovery lost application identity")
	}
}

func TestRPCPublicReadinessDistinguishesFreshAndRetainedAddresses(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	client := managementClient(t, c, Principal{NodeID: "admin"})
	node, err := client.GetNode(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "edge-id"}))
	if err != nil || !node.Msg.PublicReady {
		t.Fatal("fresh valid public endpoint reported unready", node, err)
	}
	_ = s.ReplaceLedger("fixture", "example.com", "loadbalancer:edge-id", []libdns.Record{libdns.RR{Name: "edge.example.com", Type: "A", Data: "203.0.113.5"}})
	_ = s.SetJSON("node_endpoints:edge-id", map[string]any{"public_ipv4": []string{}, "checked_at": time.Now().Unix()})
	node, err = client.GetNode(t.Context(), connect.NewRequest(&v1.ResourceID{Id: "edge-id"}))
	if err != nil || node.Msg.PublicReady || len(node.Msg.PublicIpv4) != 1 || len(node.Msg.Diagnostics) == 0 {
		t.Fatal("retained address claimed fresh readiness", node, err)
	}
}

func TestRPCReportAndServiceInspectionRetainFreshnessAndOwnership(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	zoneReportFixture(t, c, s)
	_ = s.UpsertServiceHost(store.ServiceHostRecord{ServiceName: "svc:app", DeviceID: "tail-gateway", Approved: true, ApprovalState: "approved"})
	client := managementClient(t, c, Principal{NodeID: "admin"})
	reports, err := client.ListReports(t.Context(), connect.NewRequest(&v1.Empty{}))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, report := range reports.Msg.Items {
		if report.NodeId == "docker-gateway" {
			found = report.Fresh && len(report.Exposures) == 1 && report.Exposures[0].PublicHostname == "app.example.com"
		}
	}
	if !found {
		t.Fatal("accepted report inspection lost exposure or freshness", reports.Msg)
	}
	hosts, err := client.ListServiceHosts(t.Context(), connect.NewRequest(&v1.Empty{}))
	if err != nil || len(hosts.Msg.Items) != 1 || !hosts.Msg.Items[0].Approved || hosts.Msg.Items[0].ServiceName != "svc:app" {
		t.Fatal(hosts, err)
	}
	_ = s.RevokeNode("docker-gateway")
	reports, err = client.ListReports(t.Context(), connect.NewRequest(&v1.Empty{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, report := range reports.Msg.Items {
		if report.NodeId == "docker-gateway" && report.Fresh {
			t.Fatal("revoked report displayed fresh")
		}
	}
}

func TestNodeStorageFailureIsUnavailableRatherThanRevocation(t *testing.T) {
	c, s := testControl(t)
	client := nodeClient(t, c, Principal{NodeID: "enrolled"})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := client.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("storage failure classified as identity rejection: %v", err)
	}
}
