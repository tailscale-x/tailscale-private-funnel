package web

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"fmt"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"net/http"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func configurationFanoutFixture(t *testing.T, count int) (*Control, context.Context) {
	t.Helper()
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	if err := s.SetJSON("exposure_policy", policy.Document{Revision: 1, Rules: []policy.Rule{{ID: "all", Name: "Apps", HostnameRegexes: []string{`^.+[.]example[.]com$`}, Enabled: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertNode("gateway", "k8s-gateway", "gateway"); err != nil {
		t.Fatal(err)
	}
	if err := bindTestNode(s, "gateway", "gateway-device", []string{"100.64.0.2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetJSON("node_policy_roles:gateway", []string{identity.Tag("main", identity.KubernetesGateway)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		host := fmt.Sprintf("app%d.example.com", i)
		a := store.ApplicationRecord{ID: fmt.Sprintf("app%d", i), Name: fmt.Sprintf("App %d", i), ServiceName: fmt.Sprintf("svc:app%d", i), ServiceFQDN: fmt.Sprintf("app%d.example.ts.net", i), ServicePort: 8080, BackendPort: 8080, BackendSource: "kubernetes", PublicHostnames: []string{host}, Enabled: true}
		if err := s.UpsertApplication(a); err != nil {
			t.Fatal(err)
		}
	}
	for group := 0; group < 4; group++ {
		parent := "gateway"
		if group > 0 {
			parent = fmt.Sprintf("gateway%d", group)
			if err := s.UpsertNode(parent, "k8s-gateway", parent); err != nil {
				t.Fatal(err)
			}
			if err := bindTestNode(s, parent, "device-"+parent, nil); err != nil {
				t.Fatal(err)
			}
		}
		for node := 0; node < 3; node++ {
			child, err := reserveTestForwarder(s, parent, fmt.Sprintf("node%d", node), fmt.Sprintf("node%d", node))
			if err != nil {
				t.Fatal(err)
			}
			seedEdgeReport(t, s, child.ID)
		}
	}
	seedEdgeReport(t, s, "edge-id")
	ctx := context.WithValue(t.Context(), principalKey{}, Principal{NodeID: "edge-tailnet", Tags: []string{identity.Tag("main", identity.LoadBalancer)}})
	ctx = context.WithValue(ctx, componentContext{}, componentContext{ID: "edge-id", Instance: "test-edge-id"})
	return c, ctx
}

func TestConfigurationFanout(t *testing.T) {
	c, ctx := configurationFanoutFixture(t, 48)
	t.Log("configuration render starting")
	start := time.Now()
	env, err := c.nodeConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Routes []struct {
			Hostname string `json:"hostname"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(env.Payload, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 48 {
		t.Fatalf("routes=%d want 48", len(cfg.Routes))
	}
	t.Logf("48 application configuration: %s", time.Since(start))
}

func TestConfigurationCancellationDoesNotCommitEmptyRoutes(t *testing.T) {
	c, ctx := configurationFanoutFixture(t, 3)
	if _, err := c.nodeConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	var before any
	if err := c.Store.GetJSON("node_sent_config:edge-id", &before); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.nodeConfiguration(cancelled); err == nil {
		t.Fatal("canceled configuration returned success")
	}
	var after any
	if err := c.Store.GetJSON("node_sent_config:edge-id", &after); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatal("canceled request replaced sent configuration")
	}

}

func TestConfigurationDoesNotCacheAuthorizationAcrossRequests(t *testing.T) {
	c, ctx := configurationFanoutFixture(t, 3)
	first, err := c.nodeConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Routes []any `json:"routes"`
	}
	if err = json.Unmarshal(first.Payload, &a); err != nil || len(a.Routes) != 3 {
		t.Fatal(err, a)
	}
	if err = c.Store.SetJSON("exposure_policy", policy.Document{Revision: 2, Rules: []policy.Rule{}}); err != nil {
		t.Fatal(err)
	}
	second, err := c.nodeConfiguration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var b struct {
		Routes []any `json:"routes"`
	}
	if err = json.Unmarshal(second.Payload, &b); err != nil || len(b.Routes) != 0 {
		t.Fatal("policy withdrawal was cached", err, b)
	}
}

func TestConfigurationAndHealthProgressWhileExternalAPIStalls(t *testing.T) {
	c, ctx := configurationFanoutFixture(t, 48)
	_ = c.Store.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = c.Store.SetSecret("tailscale_services_client_id", "fixture-client")
	_ = c.Store.SetSecret("tailscale_services_client_secret", "fixture-secret")
	called := make(chan struct{}, 1)
	c.TailscaleClient = &tailscale.Client{ClientID: "fixture-client", ClientSecret: "fixture-secret", HTTP: &http.Client{Transport: reportStallTransport{called}}}
	stalled, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.ReconcileServiceApprovals(stalled) }()
	defer func() { cancel(); <-done }()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("external API did not start")
	}
	fast, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	for i := 0; i < 3; i++ {
		env, err := c.nodeConfiguration(fast)
		if err != nil {
			t.Fatal("configuration starved behind external API", err)
		}
		var cfg struct {
			Routes []any `json:"routes"`
		}
		if err = json.Unmarshal(env.Payload, &cfg); err != nil || len(cfg.Routes) != 48 {
			t.Fatal("routes changed during API stall", err, len(cfg.Routes))
		}
		if _, err = (&nodeRPC{c: c}).ReportHealth(fast, connect.NewRequest(&v1.NodeHealth{Online: true})); err != nil {
			t.Fatal("health starved", err)
		}
	}
}

func TestConfigurationMalformedPolicyDoesNotWithdrawRoutes(t *testing.T) {
	c, ctx := configurationFanoutFixture(t, 3)
	if _, err := c.nodeConfiguration(ctx); err != nil {
		t.Fatal(err)
	}
	var before json.RawMessage
	if err := c.Store.GetJSON("node_sent_config:edge-id", &before); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.SetJSON("exposure_policy", "invalid policy document"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.nodeConfiguration(ctx); err == nil {
		t.Fatal("malformed policy was accepted as an empty authorized route set")
	}
	var after json.RawMessage
	if err := c.Store.GetJSON("node_sent_config:edge-id", &after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed policy read withdrew the last sent routes")
	}
}
