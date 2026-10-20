package web

import (
	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"strings"
	"testing"
	"time"
)

func TestKubernetesConflictDoesNotStarveOtherHosts(t *testing.T) {
	_, s := testControl(t)
	c := registrationTestControl(t, s)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetJSON("exposure_policy", policy.Document{Rules: []policy.Rule{{Name: "test", HostnameRegexes: []string{`.*[.]example[.]com`}, Enabled: true}}})
	_ = s.UpsertNode("kube", "k8s-gateway", "kube")
	_ = bindTestNode(s, "kube", "kube-device", nil)
	foreign := store.ApplicationRecord{ID: "foreign", BackendSource: "kubernetes", PublicHostnames: []string{"taken.example.com"}, AllowedGatewayNodes: []string{"old"}, Enabled: true}
	_ = s.UpsertApplication(foreign)
	api := nodeClient(t, c, Principal{NodeID: "kube-device"})
	backend := &v1.HTTPBackend{Key: "backend:8080", Address: "10.43.0.1:8080", Port: 8080, Weight: 1}
	host := func(name, id string) *v1.KubernetesHost {
		return &v1.KubernetesHost{Hostname: name, ResourceIds: []string{id}, Rules: []*v1.HTTPRule{{Matches: []*v1.HTTPMatch{{Path: "/"}}, Backends: []*v1.HTTPBackend{backend}}}}
	}
	input := &v1.KubernetesPayload{ClusterId: "cluster", ExpiresAt: time.Now().Add(time.Minute).Unix(), Hosts: []*v1.KubernetesHost{host("taken.example.com", "HTTPRoute/apps/blocked"), host("good.example.com", "HTTPRoute/apps/good")}}
	for i := 0; i < 2; i++ {
		if _, e := api.SubmitKubernetes(t.Context(), connect.NewRequest(input)); e != nil {
			t.Fatal("conflict starved good hostname", e)
		}
		config, e := api.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{}))
		if e != nil {
			t.Fatal(e)
		}
		if !strings.Contains(config.Msg.KubernetesErrors["HTTPRoute/apps/blocked"], "owned by another") || config.Msg.KubernetesErrors["HTTPRoute/apps/good"] != "" {
			t.Fatal(config.Msg.KubernetesErrors)
		}
	}
	apps, _ := s.Applications()
	if len(apps) != 2 {
		t.Fatalf("conflict provisioned or lost registrations: %v", apps)
	}
	var snapshot nodeconfig.KubernetesSnapshot
	if e := s.GetJSON("node_kubernetes:kube", &snapshot); e != nil {
		t.Fatal(e)
	}
	if len(snapshot.Hosts) != 1 || snapshot.Hosts[0].Hostname != "good.example.com" {
		t.Fatal("foreign host entered forwarding snapshot", snapshot)
	}
	for _, a := range apps {
		if a.ID == foreign.ID && (!a.Enabled || len(a.PublicHostnames) != 1 || a.AllowedGatewayNodes[0] != "old") {
			t.Fatal("foreign registration changed", a)
		}
	}
	// Resolving the conflict clears the per-resource error on the next snapshot.
	foreign.PublicHostnames = nil
	foreign.Enabled = false
	_ = s.UpsertApplication(foreign)
	if _, e := api.SubmitKubernetes(t.Context(), connect.NewRequest(input)); e != nil {
		t.Fatal(e)
	}
	config, e := api.GetConfig(t.Context(), connect.NewRequest(&v1.Empty{}))
	if e != nil || len(config.Msg.KubernetesErrors) != 0 {
		t.Fatal(config, e)
	}
}
