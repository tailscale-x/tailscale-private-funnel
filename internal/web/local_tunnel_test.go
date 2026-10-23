package web

import (
	"connectrpc.com/connect"
	"context"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1/funnelv1connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
	"strings"
	"testing"
	"time"
)

func localTunnelFixture(t *testing.T) (*Control, *store.Store, funnelv1connect.NodeServiceClient) {
	t.Helper()
	_, s := testControl(t)
	c := registrationTestControl(t, s)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net", "management_url": "http://control.example.ts.net:8080"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Local apps", HostnameRegexes: []string{`app[.]example[.]com`}, Enabled: true}}})
	_, err := s.RegisterComponent(store.Registration{Runtime: tunnel.Runtime, ComponentID: "local", InstanceID: "test-local", DeviceID: "local-device", Installation: "main", Mode: "tailnet-gateway", Name: "Local tunnel", Hostname: "mac", Connection: "host"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := Principal{NodeID: "local-device", Hostname: "mac", Tagged: true, Tags: []string{identity.Tag("main", identity.TailnetGateway)}}
	return c, s, nodeClient(t, c, p)
}

func TestLocalTunnelAdvertiseReportWithdrawAndConflict(t *testing.T) {
	c, s, client := localTunnelFixture(t)
	req := &v1.LocalTunnelRequest{Hostname: "app.example.com", Port: 8080, LocalAddress: "127.0.0.1"}
	r, err := client.AdvertiseTunnel(t.Context(), connect.NewRequest(req))
	if err != nil {
		t.Fatal(err)
	}
	a := r.Msg.Application
	if a == nil || a.BackendSource != "local" || r.Msg.Ready {
		t.Fatal(r.Msg)
	}
	if a.ServiceFqdn != strings.TrimPrefix(a.ServiceName, "svc:")+".example.ts.net" {
		t.Fatal("local tunnel must use MagicDNS for discovery", a.ServiceFqdn)
	}
	if _, err = client.SubmitReport(t.Context(), connect.NewRequest(&v1.ExposureReport{Exposures: []*v1.ReportedExposure{{ApplicationId: a.Id, ServiceName: a.ServiceName, PublicHostname: req.Hostname, Port: req.Port, Protocol: "http", BackendIp: req.LocalAddress, Reachable: true}}, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()})); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Acknowledge(t.Context(), connect.NewRequest(&v1.ConfigAck{Revision: "revision", Services: []string{a.ServiceName}})); err != nil {
		t.Fatal(err)
	}
	seedEdgeReport(t, s, "edge-id")
	if err = c.reconcileExposures(t.Context()); err != nil {
		t.Fatal(err)
	}
	r, err = client.AdvertiseTunnel(t.Context(), connect.NewRequest(req))
	if err != nil || !r.Msg.Ready {
		t.Fatal("ready state missing", r, err)
	}
	ledger, err := exposureLedger(t, s, "fixture", "example.com", req.Hostname)
	if err != nil || len(ledger) != 1 {
		t.Fatal(ledger, err)
	}
	// Repeated advertisement does not change application or ownership IDs.
	if r.Msg.Application.Id != a.Id {
		t.Fatal("advertisement duplicated application")
	}
	if _, err = client.AdvertiseTunnel(t.Context(), connect.NewRequest(&v1.LocalTunnelRequest{Hostname: req.Hostname, Port: 9090})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("scope changed in place", err)
	}
	if _, err = client.AdvertiseTunnel(t.Context(), connect.NewRequest(&v1.LocalTunnelRequest{Hostname: "denied.example.com", Port: 8080})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("policy bypass", err)
	}
	for _, addr := range []string{"0.0.0.0", "100.64.0.2", "192.168.1.1"} {
		if _, err = client.AdvertiseTunnel(t.Context(), connect.NewRequest(&v1.LocalTunnelRequest{Hostname: req.Hostname, Port: 8080, LocalAddress: addr})); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatal("nonloopback backend accepted", addr, err)
		}
	}
	_, err = s.RegisterComponent(store.Registration{Runtime: tunnel.Runtime, ComponentID: "other", InstanceID: "test-other", DeviceID: "other-device", Installation: "main", Mode: "tailnet-gateway", Name: "Other", Connection: "host"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	other := nodeClient(t, c, Principal{NodeID: "other-device", Tagged: true, Tags: []string{identity.Tag("main", identity.TailnetGateway)}})
	if _, err = other.AdvertiseTunnel(t.Context(), connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatal("competing owner accepted", err)
	}
	if _, err = client.WithdrawTunnel(t.Context(), connect.NewRequest(&v1.Empty{})); err != nil {
		t.Fatal(err)
	}
	if err = c.reconcileExposures(t.Context()); err != nil {
		t.Fatal(err)
	}
	ledger, err = exposureLedger(t, s, "fixture", "example.com", req.Hostname)
	if err != nil || len(ledger) != 0 {
		t.Fatal("owned cleanup incomplete", ledger, err)
	}
	if n, err := s.Node("local"); err != nil || n.TailnetID != "local-device" || n.Revoked {
		t.Fatal("withdrawal changed borrowed identity or revoked role", n, err)
	}
}

func TestLocalTunnelRoleScopeAndReadOnlyInstructions(t *testing.T) {
	c, s, _ := localTunnelFixture(t)
	for _, tag := range []string{identity.Tag("main", identity.DockerGateway), identity.Tag("other", identity.TailnetGateway)} {
		bad := nodeClient(t, c, Principal{NodeID: "local-device", Tagged: true, Tags: []string{tag}})
		if _, err := bad.AdvertiseTunnel(t.Context(), connect.NewRequest(&v1.LocalTunnelRequest{Hostname: "app.example.com", Port: 8080})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("incorrect role accepted", tag, err)
		}
	}
	_ = s.SetJSON("access_roles", AccessSettings{Roles: map[string][]string{"viewer": {"node:viewer"}, "operator": {"node:operator"}, "admin": {"node:admin"}}})
	before, _ := s.Nodes("")
	apps, _ := s.Applications()
	for _, role := range []string{"viewer", "operator", "admin"} {
		server := rpcServer(t, c, Principal{NodeID: role})
		rpc := funnelv1connect.NewDeploymentServiceClient(server.Client(), server.URL)
		r, err := rpc.TunnelInstructions(t.Context(), connect.NewRequest(&v1.TunnelInstructionsRequest{Hostname: "app.example.com", Port: 8080}))
		if err != nil {
			t.Fatal(role, err)
		}
		if !strings.Contains(r.Msg.Commands["run"], "tailscale-private-funnel tunnel 8080") || r.Msg.Credentials != nil || strings.Contains(r.Msg.String(), "test-secret") {
			t.Fatal(r.Msg)
		}
	}
	after, _ := s.Nodes("")
	current, _ := s.Applications()
	if len(before) != len(after) || len(apps) != len(current) {
		t.Fatal("read-only instruction generation registered resources")
	}
	// A regular installed Tailnet gateway cannot turn itself into a local tunnel.
	_, err := s.RegisterComponent(store.Registration{ComponentID: "regular", InstanceID: "test-regular", DeviceID: "regular-device", Installation: "main", Mode: "tailnet-gateway", Name: "Regular", Connection: "host"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rpc := nodeClient(t, c, Principal{NodeID: "regular-device", Tagged: true, Tags: []string{identity.Tag("main", identity.TailnetGateway)}})
	if _, err = rpc.AdvertiseTunnel(context.Background(), connect.NewRequest(&v1.LocalTunnelRequest{Hostname: "app.example.com", Port: 8080})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("installed gateway scope changed", err)
	}
}

func TestLocalTunnelServiceCleanupRequiresExactOwnership(t *testing.T) {
	c, s, client := localTunnelFixture(t)
	r, err := client.AdvertiseTunnel(t.Context(), connect.NewRequest(&v1.LocalTunnelRequest{Hostname: "app.example.com", Port: 8080}))
	if err != nil {
		t.Fatal(err)
	}
	apps, err := s.Applications()
	if err != nil {
		t.Fatal(err)
	}
	var owned store.ApplicationRecord
	for _, app := range apps {
		if app.ID == r.Msg.Application.Id {
			owned = app
		}
	}
	if !c.ownsLocalTunnelService(owned) {
		t.Fatal("owned Service not recognized")
	}
	for _, mutate := range []func(*store.ApplicationRecord){
		func(a *store.ApplicationRecord) { a.ServiceName = "svc:foreign" },
		func(a *store.ApplicationRecord) { a.ID = "foreign" },
		func(a *store.ApplicationRecord) { a.AllowedGatewayNodes = []string{"other"} },
		func(a *store.ApplicationRecord) { a.AllowedGatewayNodes = []string{"local", "other"} },
		func(a *store.ApplicationRecord) { a.BackendSource = "tailnet" },
	} {
		changed := owned
		mutate(&changed)
		if c.ownsLocalTunnelService(changed) {
			t.Fatal("foreign Service accepted", changed)
		}
	}
}
