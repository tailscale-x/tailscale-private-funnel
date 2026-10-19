package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"

	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func TestForwarderHeartbeatDoesNotWaitForProviderReconciliation(t *testing.T) {
	c, s := testControl(t)
	if e := s.UpsertNode("parent", "k8s-gateway", "parent"); e != nil {
		t.Fatal(e)
	}

	child, e := reserveTestForwarder(s, "parent", "kube-node", "node")
	if e != nil {
		t.Fatal(e)
	}
	if e = bindTestNode(s, child.ID, "tail-child", nil); e != nil {
		t.Fatal(e)
	}
	env, _ := report.NewSnapshot("report", child.ID, map[string]any{"exposures": []any{}, "expires_at": time.Now().Add(time.Minute).Unix()}, time.Now())
	ctx := WithPrincipal(httptest.NewRequest("POST", "/", nil), Principal{NodeID: "tail-child", Tagged: true}).Context()
	c.cleanupMu.Lock()
	done := make(chan error, 1)
	go func() { done <- c.acceptExposureReport(ctx, env) }()
	select {
	case e = <-done:
		c.cleanupMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(2 * time.Second):
		c.cleanupMu.Unlock()
		<-done
		t.Fatal("external reconciliation blocked forwarder heartbeat")
	}
	if e = s.RevokeNode("parent"); e != nil {
		t.Fatal(e)
	}
	env, _ = report.NewSnapshot("report", child.ID, map[string]any{"exposures": []any{}, "expires_at": time.Now().Add(time.Minute).Unix()}, time.Now())
	if e = c.acceptExposureReport(ctx, env); e == nil {
		t.Fatal("revoked parent bypassed fast path")
	}
}

func TestAllNodeReportsRemainAvailableDuringExternalReconciliation(t *testing.T) {
	for _, mode := range []string{"docker-gateway", "tailnet-gateway", "k8s-gateway", "loadbalancer"} {
		t.Run(mode, func(t *testing.T) {
			c, s := testControl(t)

			if err := s.UpsertNode("reporter", mode, "reporter"); err != nil {
				t.Fatal(err)
			}
			if err := bindTestNode(s, "reporter", "tail-reporter", nil); err != nil {
				t.Fatal(err)
			}
			client := nodeClient(t, c, Principal{NodeID: "tail-reporter", Tagged: true})
			// External workers may retain this lock for an entire provider timeout.
			c.cleanupMu.Lock()
			defer c.cleanupMu.Unlock()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			for i := 0; i < 3; i++ {
				wire := typedMessage(&v1.ExposureReport{ExpiresAt: time.Now().Add(time.Minute).Unix()})
				if _, err := client.SubmitReport(ctx, connect.NewRequest(wire)); err != nil {
					t.Fatalf("heartbeat blocked by provider/cleanup: %v", err)
				}
			}
			if _, err := client.ReportHealth(ctx, connect.NewRequest(&v1.NodeHealth{Online: true})); err != nil {
				t.Fatalf("health performed external work: %v", err)
			}
			if err := s.RevokeNode("reporter"); err != nil {
				t.Fatal(err)
			}
			wire := typedMessage(&v1.ExposureReport{ExpiresAt: time.Now().Add(time.Minute).Unix()})
			if _, err := client.SubmitReport(ctx, connect.NewRequest(wire)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("revocation bypass: %v", err)
			}
		})
	}
}

type reportStallTransport struct{ called chan struct{} }

func (s reportStallTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	select {
	case s.called <- struct{}{}:
	default:
	}
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestLoadBalancerHealthDoesNotCallStalledTailscaleAPI(t *testing.T) {
	c, s := testControl(t)
	installDNSFixture(t, s, "example.com")
	_ = s.SetJSON("tailnet", map[string]string{"name": "example.ts.net"})
	_ = s.SetSecret("tailscale_services_client_id", "fixture-client")
	_ = s.SetSecret("tailscale_services_client_secret", "fixture-secret")
	called := make(chan struct{}, 1)
	c.TailscaleClient = &tailscale.Client{ClientID: "fixture-client", ClientSecret: "fixture-secret", HTTP: &http.Client{Transport: reportStallTransport{called}}}
	_ = s.SetJSON("node_deployment:edge-id", nodeDeployment{LoadBalancerHostname: "edge.example.com"})
	client := nodeClient(t, c, Principal{NodeID: "edge-tailnet", Tagged: true})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := client.ReportHealth(ctx, connect.NewRequest(&v1.NodeHealth{Online: true})); err != nil {
		t.Fatal(err)
	}
	select {
	case <-called:
		t.Fatal("health request reached external API")
	default:
	}
}
