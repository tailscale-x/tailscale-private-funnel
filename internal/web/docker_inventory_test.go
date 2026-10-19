package web

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
)

func TestRPCDockerRetirementAllowsKubernetesWithoutBreakingSharedReports(t *testing.T) {
	_, s := testControl(t)
	c := registrationTestControl(t, s)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "Apps", HostnameRegexes: []string{`(?:app|other)[.]example[.]com`}, Enabled: true}}})

	_ = s.UpsertNode("docker", "docker-gateway", "docker")
	_ = bindTestNode(s, "docker", "docker-device", nil)
	makeApp := func(id, host string) store.ApplicationRecord {
		a := store.ApplicationRecord{ID: id, BackendSource: "docker", Enabled: true, PublicHostnames: []string{host}, ServiceName: "svc:tailscale-private-funnel-app-" + id, ServiceFQDN: "100.70.0.1", ServicePort: 8080, BackendPort: 8080, AllowedGatewayNodes: []string{"docker"}}
		if err := s.UpsertApplication(a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	old := makeApp("old", "app.example.com")
	other := makeApp("other", "other.example.com")
	before, _ := c.mapping("app.example.com")
	source := nodeClient(t, c, Principal{NodeID: "docker-device", Tagged: true})
	inv := &v1.DockerInventoryPayload{Revision: 1, Complete: true, Containers: []*v1.DockerInventoryContainer{{Id: "old-container", Running: true, Labels: map[string]string{"tailscale-private-funnel.expose": "8080:app.example.com:http"}}, {Id: "other-container", Running: true, Labels: map[string]string{"tailscale-private-funnel.expose": "8080:other.example.com:http"}}}}
	sendInventory := func() error {
		_, err := source.SubmitDockerInventory(t.Context(), connect.NewRequest(typedMessage(inv)))
		return err
	}
	if err := sendInventory(); err != nil {
		t.Fatal(err)
	}
	payload := &v1.ExposureReport{ExpiresAt: time.Now().Add(time.Minute).Unix(), Exposures: []*v1.ReportedExposure{{ApplicationId: old.ID, ServiceName: old.ServiceName, PublicHostname: "app.example.com", ContainerId: "old-container", Port: 8080, Protocol: "http", Reachable: true}, {ApplicationId: other.ID, ServiceName: other.ServiceName, PublicHostname: "other.example.com", ContainerId: "other-container", Port: 8080, Protocol: "http", Reachable: true}}}
	sendReport := func() error {
		_, err := source.SubmitReport(t.Context(), connect.NewRequest(typedMessage(payload)))
		return err
	}
	if err := sendReport(); err != nil {
		t.Fatal(err)
	}
	// A failed/incomplete scan cannot release either application.
	inv.Revision = 2
	inv.Complete = false
	inv.Containers = nil
	if err := sendInventory(); err == nil {
		t.Fatal("incomplete scan accepted")
	}
	if got := c.authorizedReports(c.loadExposurePolicy())["docker"]; len(got) != 2 {
		t.Fatal("incomplete scan withdrew live registration", got)
	}
	inv.Complete = true
	inv.Containers = []*v1.DockerInventoryContainer{{Id: "other-container", Running: true, Labels: map[string]string{"tailscale-private-funnel.expose": "8080:other.example.com:http"}}}
	// Local acceptance must also progress while external DNS is blocked.
	c.cleanupMu.Lock()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	_, err := source.SubmitDockerInventory(ctx, connect.NewRequest(typedMessage(inv)))
	cancel()
	c.cleanupMu.Unlock()
	if err != nil {
		t.Fatal("inventory waited for external reconciliation", err)
	}
	if err = sendReport(); err != nil {
		t.Fatal("old retired entry starved unrelated report", err)
	}
	got := c.authorizedReports(c.loadExposurePolicy())["docker"]
	if len(got) != 1 || got[0].ApplicationID != other.ID {
		t.Fatal("retired report remained authorized", got)
	}
	if _, err = source.ResolveApplication(t.Context(), connect.NewRequest(&v1.ResolveApplicationRequest{Hostname: "app.example.com", BackendPort: 8080})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("stale Docker declaration reprovisioned", err)
	}
	if err = sendInventory(); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("inventory revision replay accepted", err)
	}
	// Existing Kubernetes enrollment claims the released host normally.

	_ = s.UpsertNode("kube", "k8s-gateway", "kube")
	_ = bindTestNode(s, "kube", "kube-device", nil)
	destination := nodeClient(t, c, Principal{NodeID: "kube-device", Tagged: true})
	snapshot := &v1.KubernetesPayload{ClusterId: "cluster", ExpiresAt: time.Now().Add(time.Minute).Unix(), Hosts: []*v1.KubernetesHost{{Hostname: "app.example.com", Rules: []*v1.HTTPRule{{Matches: []*v1.HTTPMatch{{Path: "/"}}, Backends: []*v1.HTTPBackend{{Key: "service:8080", Address: "10.43.0.1:8080", Port: 8080, Weight: 1, Endpoints: []*v1.PodEndpoint{{Address: "10.42.0.5:8080", PodUid: "pod", NodeName: "node", NodeUid: "uid"}}}}}}}}}
	if _, err = destination.SubmitKubernetes(t.Context(), connect.NewRequest(typedMessage(snapshot))); err != nil {
		t.Fatal("normal Kubernetes claim rejected", err)
	}
	after, _ := c.mapping("app.example.com")
	if after.ID != before.ID || after.ApplicationID == old.ID || !after.Active {
		t.Fatal("exposure identity was replaced", before, after)
	}
	if err = sendReport(); err != nil {
		t.Fatal("Kubernetes takeover broke Docker heartbeat", err)
	}
	foreign := nodeClient(t, c, Principal{NodeID: "kube-device", Tagged: true})
	if _, err = foreign.SubmitDockerInventory(t.Context(), connect.NewRequest(typedMessage(inv))); err == nil {
		t.Fatal("foreign caller accepted")
	}
	// A stalled external transport is unrelated to local report acceptance.
	called := make(chan struct{}, 1)
	c.TailscaleClient = &tailscale.Client{HTTP: &http.Client{Transport: reportStallTransport{called}}}
	inv.Revision = 3
	inv.LabelKeys = []string{"unexpected.expose"}
	if err = sendInventory(); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("mismatched label configuration accepted", err)
	}
	inv.LabelKeys = nil
	inv.Containers[0].Labels["tailscale-private-funnel.expose"] = "invalid"
	if err = sendInventory(); err == nil {
		t.Fatal("invalid labels permitted retirement")
	}
	inv.Containers[0].Labels["tailscale-private-funnel.expose"] = "8080:other.example.com:http"
	if err = sendInventory(); err != nil {
		t.Fatal(err)
	}
	if err = sendReport(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("inventory/report reached external API")
	default:
	}
	admin := managementClient(t, c, Principal{NodeID: "admin"})
	listed, e := admin.ListApplications(t.Context(), connect.NewRequest(&v1.Empty{}))
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range listed.Msg.Items {
		if a.Id == old.ID {
			t.Fatal("retired application displayed as active")
		}
	}
	health, e := admin.Health(t.Context(), connect.NewRequest(&v1.Empty{}))
	if e != nil {
		t.Fatal(e)
	}
	if health.Msg.Applications != uint32(len(listed.Msg.Items)) {
		t.Fatal("dashboard counts retired inventory", health.Msg.Applications)
	}
	n, _ := s.Node("docker")
	if n.Revoked || n.TailnetID != "docker-device" {
		t.Fatal("shared gateway identity changed")
	}
}
