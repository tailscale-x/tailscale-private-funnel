package app

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tsconnection"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"

	"github.com/tailscale-x/tailscale-private-funnel/internal/gateway"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
)

func runTailnetGateway(ctx context.Context, o Options, s tsconnection.Connection) error {
	session := gateway.Session{Resolver: gateway.RPCResolver{BaseURL: o.ManagementURL, NodeID: loadBalancerNodeID(o.DataDir), Client: tsnetHTTPClient(s)}, Listeners: gateway.NewListenerManager(s), Agent: report.Agent{NodeID: loadBalancerNodeID(o.DataDir), ManagementURL: o.ManagementURL, DataDir: o.DataDir, HTTPClient: tsnetHTTPClient(s)}}
	defer drainGatewayListeners(session.Listeners)
	reconcile := func() {
		peers, err := gatewayPeers(ctx, s)
		if err != nil {
			slog.Warn("tailnet discovery failed", "error", err)
			return
		}
		peers.Sources = []string{"tailnet"}
		config, err := session.Resolver.Configuration(ctx)
		if err != nil {
			slog.Warn("gateway configuration unavailable", "error", err)
			return
		}
		peers.Results = runProbes(ctx, s, peers.Peers, config.Probes)
		payload, convertErr := report.Payload(peers, &v1.PeerSnapshot{})
		if convertErr != nil {
			slog.Warn("peer payload conversion failed", "error", convertErr)
			return
		}
		if err = session.Agent.SendPeers(ctx, payload); err != nil {
			slog.Warn("peer report failed", "error", err)
		}
		runtimes := []gateway.Runtime{}
		for _, app := range config.Applications {
			if app.BackendSource != "tailnet" {
				continue
			}
			target, err := peerTarget(peers.Peers, app.BackendDeviceID, app.BackendPort)
			healthy := false
			if err == nil {
				probe, cancel := context.WithTimeout(ctx, 5*time.Second)
				conn, e := s.Dial(probe, "tcp", target)
				cancel()
				if conn != nil {
					conn.Close()
				}
				if e == nil {
					healthy = session.Listeners.ListenDial(ctx, app.ServiceName, app.ServicePort, target, s.Dial) == nil
				}
			}
			ip, _, _ := net.SplitHostPort(target)
			for _, host := range app.PublicHostnames {
				runtimes = append(runtimes, gateway.Runtime{Spec: gateway.Spec{Hostname: host, Port: app.BackendPort, Protocol: "http"}, Assignment: gateway.Assignment{ApplicationID: app.ID, ServiceName: app.ServiceName, ServicePort: app.ServicePort, Enabled: true}, BackendDeviceID: app.BackendDeviceID, BackendHostname: app.BackendDeviceID, BackendIP: ip, Reachable: healthy})
			}
		}
		if err = session.Publish(ctx, config, runtimes, nil); err != nil {
			slog.Warn("gateway report failed", "error", err)
		} else {
			markRuntimeReady(o.DataDir)
		}
	}
	reconcile()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			reconcile()
		}
	}
}
