package web

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
)

func (s *deploymentRPC) TunnelInstructions(ctx context.Context, r *connect.Request[v1.TunnelInstructionsRequest]) (*connect.Response[v1.DeploymentInstructions], error) {
	if r.Msg.Port == 0 || r.Msg.Port > 65535 {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("valid local port required"))
	}
	var settings map[string]any
	if err := s.c.Store.WithContext(ctx).GetJSON("tailnet", &settings); err != nil {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	in := tunnel.Settings{Hostname: r.Msg.Hostname, Port: uint16(r.Msg.Port), LocalAddress: r.Msg.LocalAddress, Name: r.Msg.Name, Connection: r.Msg.TailscaleConnection, Installation: s.c.installation(), ControlURL: stringValue(settings, "management_url", "")}
	if in.ControlURL == "" {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("control Service URL is not configured"))
	}
	if _, err := s.c.managedZone(in.Hostname); err != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, err)
	}
	command, err := tunnel.Command(in)
	if err != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, err)
	}

	in, err = in.Resolve()
	if err != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, err)
	}
	prerequisites := []string{"Linux or Apple Silicon macOS with a matching Funnel CLI; no Docker, systemd or launchd", "Control, load balancers and CLI require v4.1.0 or newer, with identity-scoped Service DNS resolution", "An already-connected host Tailscale daemon can reach this private control; LocalAPI outbound access is required", "The HTTP backend listens on loopback; managed-zone and exposure policy checks still apply"}
	run := "Phone home using the host’s existing identity, obtain a 15-minute single-use key, and start a separate ephemeral tsnet identity. No host tag or Funnel administrator role is required. Keep the terminal open."
	stopDescription := "Withdraw the tunnel and log out its embedded identity. Owned DNS cleanup is asynchronous. The borrowed host identity and configuration remain intact."
	if in.Connection == "host" {
		stopDescription = "Withdraw only the owned host Service entry; never log out or change the borrowed device. Owned DNS cleanup is asynchronous."
		prerequisites = append(prerequisites, "Host mode requires "+identity.Tag(in.Installation, identity.TailnetGateway)+" and LocalAPI Service forwarding permissions")
		run = "Reuse the already role-tagged host daemon. No key is requested; only exact owned Service entries are changed."
	}
	return connect.NewResponse(&v1.DeploymentInstructions{ManagementUrl: in.ControlURL, Version: s.c.BuildVersion, Commands: map[string]string{"run": command}, Example: command, Prerequisites: prerequisites, Steps: []*v1.DeploymentStep{{Id: "run", Title: "Run on the backend host", Description: run}, {Id: "verify", Title: "Wait for Ready", Description: "Control validates exposure policy, backend reports, Service readiness, load-balancer acknowledgment and owned DNS. First HTTPS visit may obtain its certificate."}, {Id: "stop", Title: "Press Ctrl+C", Description: stopDescription}}}), nil
}
