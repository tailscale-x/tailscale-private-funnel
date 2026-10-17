package app

import (
	"testing"

	"github.com/tailscale-x/tailscale-private-funnel/internal/store"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
)

func TestLoadBalancerPeerTargetBindsDeviceIDAndIPv6(t *testing.T) {
	peers := []nodeconfig.Peer{{ID: "stable", Addresses: []string{"fd7a:115c:a1e0::1"}}}
	target, err := peerTarget(peers, "stable", 8080)
	if err != nil || target != "[fd7a:115c:a1e0::1]:8080" {
		t.Fatal(target, err)
	}
	if _, err = peerTarget(peers, "replacement", 8080); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	if _, err = peerTarget(nil, "stable", 8080); err == nil {
		t.Fatal("missing peer fallback")
	}
}

func TestLoadBalancerTailnetReportRetainsServiceIdentity(t *testing.T) {
	app := store.ApplicationRecord{ID: "remote", BackendSource: "tailnet", ServiceName: "svc:remote", BackendPort: 8765}
	exposure := loadBalancerExposure(app, "voice.example.com", "svc:remote", "100.70.0.1", true)
	if exposure.ServiceName != app.ServiceName || exposure.ApplicationID != app.ID || !exposure.Reachable || exposure.Port != 8765 {
		t.Fatalf("report cannot authorize the application Service: %+v", exposure)
	}
}
