package app

import (
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"testing"
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
