package app

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func TestLoadBalancerProbesEveryKubernetesBackendConcurrently(t *testing.T) {
	var concurrent, peak atomic.Int32
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	apps := []store.ApplicationRecord{{ID: "harbor", Enabled: true, BackendSource: "kubernetes", PublicHostnames: []string{"harbor.example.com"}, HTTPRules: []proxy.HTTPRule{{Matches: []proxy.Match{{Path: "/"}}}}, HTTPBackends: []proxy.Backend{{ServiceName: "svc:harbor", Service: "100.64.0.1", Port: 8080}, {ServiceName: "svc:solver", Service: "100.64.0.2", Port: 8089}}}}
	dial := func(ctx context.Context, _, address string) (net.Conn, error) {
		n := concurrent.Add(1)
		defer concurrent.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if n == 2 {
			close(release)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if address == "100.64.0.2:8089" {
			return nil, errors.New("solver not ready")
		}
		a, b := net.Pipe()
		_ = b.Close()
		return a, nil
	}
	exposures := probeLoadBalancerBackends(ctx, dial, apps)
	if len(exposures) != 2 || !exposures[0].Reachable || exposures[1].Reachable || exposures[0].ServiceName != "svc:harbor" || exposures[1].ServiceName != "svc:solver" || exposures[1].Port != 8089 {
		t.Fatalf("incorrect per-backend reports: %+v", exposures)
	}
	if peak.Load() != 2 {
		t.Fatal("pending solver serialized healthy backend probe", peak.Load())
	}
}
