package app

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"tailscale.com/tsnet"
)

func gatewayPeers(ctx context.Context, s *tsnet.Server) (nodeconfig.PeerSnapshot, error) {
	var snapshot nodeconfig.PeerSnapshot
	lc, err := s.LocalClient()
	if err != nil {
		return snapshot, err
	}
	status, err := lc.Status(ctx)
	if err != nil {
		return snapshot, err
	}
	if status.Self != nil && status.Self.Tags != nil {
		snapshot.Tags = status.Self.Tags.AsSlice()
	}
	for _, peer := range status.Peer {
		if peer.ID == "" {
			continue
		}
		p := nodeconfig.Peer{ID: string(peer.ID), Hostname: peer.HostName, DNSName: strings.TrimSuffix(peer.DNSName, "."), Online: peer.Online}
		if peer.Tags != nil {
			p.Tags = peer.Tags.AsSlice()
		}
		for _, ip := range peer.TailscaleIPs {
			p.Addresses = append(p.Addresses, ip.String())
		}
		snapshot.Peers = append(snapshot.Peers, p)
	}
	snapshot.ExpiresAt = time.Now().Add(5 * time.Minute).Unix()
	return snapshot, nil
}
func peerTarget(peers []nodeconfig.Peer, id string, port uint16) (string, error) {
	for _, peer := range peers {
		if peer.ID == id && len(peer.Addresses) > 0 {
			return net.JoinHostPort(peer.Addresses[0], strconv.Itoa(int(port))), nil
		}
	}
	return "", fmt.Errorf("backend device is absent from load balancer peer information; check Tailscale tags and grants")
}
func runProbes(ctx context.Context, s *tsnet.Server, peers []nodeconfig.Peer, probes []nodeconfig.Probe) []nodeconfig.ProbeResult {
	var out []nodeconfig.ProbeResult
	for _, probe := range probes {
		if probe.ExpiresAt <= time.Now().Unix() {
			continue
		}
		result := nodeconfig.ProbeResult{ID: probe.ID, DeviceID: probe.DeviceID, Port: probe.Port, CheckedAt: time.Now().Unix()}
		target, err := peerTarget(peers, probe.DeviceID, probe.Port)
		if err == nil {
			probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			var conn net.Conn
			conn, err = s.Dial(probeCtx, "tcp", target)
			cancel()
			if conn != nil {
				conn.Close()
			}
		}
		result.Reachable = err == nil
		if err != nil {
			result.Error = "Connection failed. Check device visibility, Tailscale grants and backend TCP port."
		}
		out = append(out, result)
	}
	return out
}
