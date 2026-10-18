package web

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestDockerPathApplicationsAndSignedReports(t *testing.T) {
	_, s := testControl(t)
	c := enrollmentTestControl(t, s)
	fixture := installDNSFixture(t, s, "example.com")
	fixture.records["example.com"] = []libdns.Record{libdns.RR{Name: "unrelated.example.com", Type: "A", Data: "203.0.113.99"}}
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Apps", HostnameRegexes: []string{`app[.]example[.]com`}, Enabled: true}}})
	pub, key, _ := report.GenerateKey()
	_ = s.UpsertNode("docker", "docker-gateway", "docker")
	_ = s.BindNodeIdentity("docker", encodePublicKey(pub), "docker-device", nil)
	_ = s.SetJSON("node_deployment:docker", nodeDeployment{})
	client := nodeClient(t, c, Principal{NodeID: "docker-device", Tags: []string{"tag:tailscale-private-funnel"}, Tagged: true})
	resolve := func(path string, port uint32, replacement *v1.PrefixRewrite) string {
		t.Helper()
		response, err := client.ResolveApplication(t.Context(), connect.NewRequest(&v1.ResolveApplicationRequest{Hostname: "app.example.com", BackendPort: port, PathPrefix: path, Rewrite: replacement}))
		if err != nil {
			t.Fatal(path, err)
		}
		env, err := report.FromWire(response.Msg, "configuration")
		if err != nil {
			t.Fatal(err)
		}
		var cfg nodeconfig.Configuration
		if err = json.Unmarshal(env.Payload, &cfg); err != nil {
			t.Fatal(err)
		}
		for _, a := range cfg.Applications {
			if a.BackendPort == uint16(port) && len(a.HTTPRules) == 1 && a.HTTPRules[0].Matches[0].Path == path {
				return a.ID
			}
		}
		t.Fatal("missing path assignment", path)
		return ""
	}
	api := resolve("/api", 8080, &v1.PrefixRewrite{ReplacePrefix: "/"})
	admin := resolve("/admin", 9090, nil)
	if api == admin {
		t.Fatal("assignments overwritten")
	}
	if again := resolve("/api", 8080, &v1.PrefixRewrite{ReplacePrefix: "/internal"}); again != api {
		t.Fatal("rewrite update changed identity")
	}
	apps, _ := s.Applications()
	apiApp, ok := pathTestApp(apps, api)
	if !ok {
		t.Fatal(api)
	}
	if apiApp.HTTPRules[0].Rewrite.ReplacePrefix != "/internal" {
		t.Fatal("rewrite not stored")
	}
	adminApp, _ := pathTestApp(apps, admin)
	view := applicationMessage(apiApp)
	if view.PathPrefix != "/api" || view.Rewrite == nil || view.Rewrite.ReplacePrefix != "/internal" {
		t.Fatal("management view dropped path configuration", view)
	}
	items := []*v1.ReportedExposure{
		{ApplicationId: api, ServiceName: apiApp.ServiceName, ContainerId: "api-container", PublicHostname: "app.example.com", BackendIp: "172.20.0.2", BackendHostname: "api", Port: 8080, Protocol: "http", Reachable: true, PathPrefix: "/api", Rewrite: &v1.PrefixRewrite{ReplacePrefix: "/internal"}},
		{ApplicationId: admin, ServiceName: adminApp.ServiceName, ContainerId: "admin-container", PublicHostname: "app.example.com", BackendIp: "172.20.0.3", BackendHostname: "admin", Port: 9090, Protocol: "http", Reachable: true, PathPrefix: "/admin"},
	}
	send := func(exposures []*v1.ReportedExposure) error {
		_, err := client.SubmitReport(t.Context(), connect.NewRequest(signedMessage(t, "report", "docker", &v1.ExposureReport{Exposures: exposures, ExpiresAt: time.Now().Add(time.Minute).Unix()}, key)))
		return err
	}
	if err := send(items); err != nil {
		t.Fatal(err)
	}
	if got := c.authorizedReports(c.loadExposurePolicy())["docker"]; len(got) != 2 {
		t.Fatalf("authorized=%+v", got)
	}
	mappings, _ := s.Exposures()
	var ownerID string
	for _, mapping := range mappings {
		if mapping.Hostname == "app.example.com" {
			ownerID = mapping.ID
		}
	}
	if ownerID == "" {
		t.Fatal("missing stable hostname ownership")
	}
	edgeKeyRaw, _ := s.GetSecret("test-edge-key")
	edgeKey, _ := base64.RawStdEncoding.DecodeString(edgeKeyRaw)
	edge := nodeClient(t, c, Principal{NodeID: "edge-tailnet", Tagged: true, Tags: []string{"tag:tailscale-private-funnel"}})
	edgeItems := []*v1.ReportedExposure{
		{ApplicationId: api, ServiceName: apiApp.ServiceName, PublicHostname: "app.example.com", Port: 8080, Reachable: true},
		{ApplicationId: admin, ServiceName: adminApp.ServiceName, PublicHostname: "app.example.com", Port: 9090, Reachable: true},
	}
	if _, err := edge.SubmitReport(t.Context(), connect.NewRequest(signedMessage(t, "report", "edge-id", &v1.ExposureReport{Exposures: edgeItems, ExpiresAt: time.Now().Add(time.Minute).Unix()}, edgeKey))); err != nil {
		t.Fatal(err)
	}
	ledger, err := s.Ledger("fixture", "example.com", "exposure:"+ownerID)
	if err != nil || len(ledger) == 0 {
		t.Fatal("expected owned DNS", ledger, err)
	}
	// A route removed from a desired-state snapshot cannot be restored by its persisted application.
	if err := send(items[1:]); err != nil {
		t.Fatal(err)
	}
	got := c.authorizedReports(c.loadExposurePolicy())["docker"]
	if len(got) != 1 || got[0].ApplicationID != admin {
		t.Fatalf("withdrawal=%+v", got)
	}
	mappings, _ = s.Exposures()
	for _, mapping := range mappings {
		if mapping.Hostname == "app.example.com" && mapping.ID != ownerID {
			t.Fatal("DNS ownership changed")
		}
	}
	ledger, err = s.Ledger("fixture", "example.com", "exposure:"+ownerID)
	if err != nil || len(ledger) == 0 {
		t.Fatal("sibling DNS removed", ledger, err)
	}
	// Same hostname remains managed while its other path is served.
	if !c.authorizedHostnames(c.loadExposurePolicy())["app.example.com"] {
		t.Fatal("sibling hostname withdrawn")
	}
	items[0].Rewrite = &v1.PrefixRewrite{ReplacePrefix: "/spoof"}
	if err := send(items); err == nil {
		t.Fatal("unassigned rewrite accepted")
	}
	items[0].Rewrite = &v1.PrefixRewrite{ReplacePrefix: "/internal"}
	if err := send([]*v1.ReportedExposure{items[0], items[0]}); err == nil {
		t.Fatal("duplicate path accepted")
	}
	_, err = client.ResolveApplication(t.Context(), connect.NewRequest(&v1.ResolveApplicationRequest{Hostname: "denied.example.com", BackendPort: 8080, PathPrefix: "/api"}))
	if err == nil {
		t.Fatal("policy denied host provisioned")
	}
	if err := send(nil); err != nil {
		t.Fatal(err)
	}
	if got := c.authorizedReports(c.loadExposurePolicy())["docker"]; len(got) != 0 {
		t.Fatal("empty snapshot retained routes")
	}
	ledger, err = s.Ledger("fixture", "example.com", "exposure:"+ownerID)
	if err != nil || len(ledger) != 0 {
		t.Fatal("empty snapshot failed owned cleanup", ledger, err)
	}
	kept := false
	for _, record := range fixture.records["example.com"] {
		if record.RR().Name == "unrelated.example.com" && record.RR().Data == "203.0.113.99" {
			kept = true
		}
	}
	if !kept {
		t.Fatal("unowned record deleted")
	}
}

func TestDockerReportPrefixBinding(t *testing.T) {
	app := store.ApplicationRecord{HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/api"}}, Rewrite: &proxy.PrefixRewrite{ReplacePrefix: "/"}}}}
	e := dns.GatewayExposure{PathPrefix: "/api", Rewrite: &proxy.PrefixRewrite{ReplacePrefix: "/"}}
	if !dockerReportRouteMatches(app, e) {
		t.Fatal("valid binding")
	}
	e.PathPrefix = "/other"
	if dockerReportRouteMatches(app, e) {
		t.Fatal("forged prefix")
	}
}

func pathTestApp(apps []store.ApplicationRecord, id string) (store.ApplicationRecord, bool) {
	for _, app := range apps {
		if app.ID == id {
			return app, true
		}
	}
	return store.ApplicationRecord{}, false
}
