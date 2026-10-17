package web

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
)

func TestKubernetesServiceHostnameAndStaleDiscovery(t *testing.T) {
	c, s := testControl(t)
	n := store.NodeRecord{ID: "lb"}
	d := nodeDeployment{KubernetesNamespace: "system", PublicService: "public"}
	p := publicEndpoint{Namespace: "system", Service: "public", UID: "uid", ObservedAt: time.Now().Unix(), Ingress: []core.LoadBalancerIngress{{Hostname: "lb.example.net"}}}
	_ = s.SetJSON("node_public_endpoint:lb", p)
	c.PublicLookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("10.1.2.3")}, {IP: net.ParseIP("8.8.8.8")}}, nil
	}
	ips, e := c.kubernetesAddresses(context.Background(), n, d)
	if e != nil || len(ips) != 1 || ips[0] != "8.8.8.8" {
		t.Fatal(ips, e)
	}
	c.PublicLookup = func(context.Context, string) ([]net.IPAddr, error) { return nil, fmt.Errorf("DNS unavailable") }
	if _, e = c.kubernetesAddresses(context.Background(), n, d); e == nil {
		t.Fatal("failed hostname resolved through fallback")
	}
	p.ObservedAt = time.Now().Add(-2 * time.Minute).Unix()
	_ = s.SetJSON("node_public_endpoint:lb", p)
	if _, e = c.kubernetesAddresses(context.Background(), n, d); e == nil {
		t.Fatal("stale status accepted")
	}
}
