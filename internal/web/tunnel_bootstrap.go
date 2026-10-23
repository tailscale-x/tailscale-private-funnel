package web

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
)

func (s *deploymentRPC) BootstrapTunnel(ctx context.Context, r *connect.Request[v1.TunnelBootstrapRequest]) (*connect.Response[v1.TunnelBootstrap], error) {
	p := rpcPrincipal(ctx)
	if p.NodeID == "" {
		return nil, rpcFailure(connect.CodeUnauthenticated, errors.New("verified tailnet peer required"))
	}
	if r.Msg.Installation != s.c.installation() {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("installation does not match this control"))
	}
	if r.Msg.Port == 0 || r.Msg.Port > 65535 {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("valid local port required"))
	}
	settings, err := (tunnel.Settings{Name: r.Msg.Name, Hostname: r.Msg.Hostname, Port: uint16(r.Msg.Port), LocalAddress: r.Msg.LocalAddress, Installation: r.Msg.Installation}).Resolve()
	if err != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, err)
	}
	if _, err = s.c.managedZone(settings.Hostname); err != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st := s.c.Store.WithContext(ctx)
	if err = st.ReserveTunnelBootstrap(p.NodeID, time.Now()); err != nil {
		code := connect.CodeUnavailable
		if errors.Is(err, store.ErrTunnelBootstrapRate) {
			code = connect.CodeResourceExhausted
		}
		return nil, rpcFailure(code, err)
	}
	id, err := st.GetSecret("tailscale_issuer_client_id")
	if err != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("configure installation auth-key issuer"))
	}
	secret, err := st.GetSecret("tailscale_issuer_client_secret")
	if err != nil {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("configure installation auth-key issuer"))
	}
	var tailnet map[string]any
	if err = st.GetJSON("tailnet", &tailnet); err != nil {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	key, err := s.c.tsClient(id, secret, stringValue(tailnet, "name", "")).CreateWorkloadAuthKey(ctx, "tunnel", settings.Name, []string{identity.Tag(settings.Installation, identity.TailnetGateway)}, true)
	if err != nil {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&v1.TunnelBootstrap{AuthKey: key, ExpiresAt: time.Now().Add(15 * time.Minute).Unix(), Installation: settings.Installation}), nil
}
