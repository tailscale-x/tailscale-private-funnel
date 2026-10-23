package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
)

func (c *Control) localTunnelCaller(ctx context.Context) (store.NodeRecord, error) {
	n, err := c.callerNodeContext(ctx, "tailnet-gateway")
	if err != nil {
		return n, err
	}
	r, err := c.Store.ComponentRegistration(n.ID)
	if err != nil || r.Runtime != tunnel.Runtime || (r.Connection != "host" && r.Connection != "embedded") {
		return n, errors.New("foreground tunnel component required")
	}
	return n, nil
}

func (c *Control) ownsLocalTunnelService(app store.ApplicationRecord) bool {
	if app.BackendSource != "local" || len(app.AllowedGatewayNodes) != 1 || app.ServiceName != "svc:tailscale-private-funnel-app-"+app.ID {
		return false
	}
	id := app.AllowedGatewayNodes[0]
	reg, err := c.Store.ComponentRegistration(id)
	if err != nil || reg.Runtime != tunnel.Runtime || reg.Mode != "tailnet-gateway" || (reg.Connection != "host" && reg.Connection != "embedded") {
		return false
	}
	var request v1.LocalTunnelRequest
	if c.Store.GetJSON("local_tunnel:"+id, &request) != nil {
		return false
	}
	expected := fmt.Sprintf("local-%x", sha256.Sum256([]byte(id+":"+request.Hostname)))[:26]
	return app.ID == expected
}

func (s *nodeRPC) AdvertiseTunnel(ctx context.Context, r *connect.Request[v1.LocalTunnelRequest]) (*connect.Response[v1.LocalTunnelStatus], error) {
	c := s.c
	n, err := c.localTunnelCaller(ctx)
	if err != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, err)
	}
	if r.Msg.Port == 0 || r.Msg.Port > 65535 {
		return nil, rpcFailure(connect.CodeInvalidArgument, errors.New("valid local port required"))
	}
	settings, err := (tunnel.Settings{Hostname: r.Msg.Hostname, Port: uint16(r.Msg.Port), LocalAddress: r.Msg.LocalAddress, Installation: c.installation()}).Resolve()
	if err != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, err)
	}
	if ok, _ := c.loadExposurePolicy().Allows(settings.Hostname, []string{n.ID, n.TailnetID}, rpcPrincipal(ctx).RoleSelectors()); !ok {
		return nil, rpcFailure(connect.CodePermissionDenied, errors.New("hostname denied by exposure policy"))
	}
	zone, err := c.managedZone(settings.Hostname)
	if err != nil {
		return nil, rpcFailure(connect.CodeInvalidArgument, err)
	}
	if !c.provisionMu.TryLock() {
		return nil, rpcFailure(connect.CodeUnavailable, errors.New("application provisioning busy; retry"))
	}
	defer c.provisionMu.Unlock()
	var previous v1.LocalTunnelRequest
	err = c.Store.GetJSON("local_tunnel:"+n.ID, &previous)
	if err == nil && (previous.Hostname != settings.Hostname || previous.Port != r.Msg.Port || previous.LocalAddress != settings.LocalAddress) {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("tunnel settings differ; use a different --name"))
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	id := fmt.Sprintf("local-%x", sha256.Sum256([]byte(n.ID+":"+settings.Hostname)))[:26]
	app := store.ApplicationRecord{ID: id, Name: settings.Hostname, BackendSource: "local", ServiceName: "svc:tailscale-private-funnel-app-" + id, ServicePort: settings.Port, BackendPort: settings.Port, BackendAddresses: []string{settings.LocalAddress}, PublicHostnames: []string{settings.Hostname}, AllowedGatewayNodes: []string{n.ID}, Enabled: true, PolicyRevision: int64(c.loadExposurePolicy().Revision)}
	found := false
	for _, a := range apps {
		if a.ID == id {
			if a.BackendSource != "local" || len(a.AllowedGatewayNodes) != 1 || a.AllowedGatewayNodes[0] != n.ID {
				return nil, rpcFailure(connect.CodeAlreadyExists, errors.New("local application ownership conflict"))
			}
			if a.Enabled && contains(a.PublicHostnames, settings.Hostname) && a.ServiceFQDN != "" {
				app = a
				found = true
			}
		}
		if a.ID != id && contains(a.PublicHostnames, settings.Hostname) {
			return nil, rpcFailure(connect.CodeAlreadyExists, errors.New("hostname already belongs to another application"))
		}
	}
	if !found {
		app, err = c.provisionApplicationService(ctx, app)
		if err != nil {
			return nil, rpcFailure(connect.CodeUnavailable, err)
		}
		if err = c.Store.UpsertApplication(app); err != nil {
			return nil, rpcFailure(connect.CodeAlreadyExists, err)
		}
		_ = c.Store.Audit("tunnel.advertise:"+n.ID+":"+settings.Hostname, "node:"+n.TailnetID)
	}
	if err = c.Store.SetJSON("local_tunnel:"+n.ID, &v1.LocalTunnelRequest{Hostname: settings.Hostname, Port: r.Msg.Port, LocalAddress: settings.LocalAddress}); err != nil {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	ready := false
	if e, ok := c.mapping(settings.Hostname); ok {
		reader := c.readControl(ctx)
		desired, _ := reader.exposureDesired(e, reader.loadExposurePolicy())
		if err = reader.Store.ReadError(); err != nil {
			return nil, rpcFailure(connect.CodeUnavailable, err)
		}
		ledger, e := c.Store.WithContext(ctx).Ledger(zone.ProviderID, zone.Zone, "exposure:"+e.ID)
		if e != nil {
			return nil, rpcFailure(connect.CodeUnavailable, e)
		}
		if len(desired) > 0 && len(ledger) == len(desired) {
			ready = true
			for _, record := range desired {
				matched := false
				for _, l := range ledger {
					matched = matched || dns.RecordKey(l.Record) == dns.RecordKey(record)
				}
				ready = ready && matched
			}
		}
	}
	message := "Waiting for backend validation, Service readiness, load-balancer acknowledgment and owned DNS"
	if ready {
		message = "Public route and owned DNS are ready; first HTTPS visit may issue its certificate"
	}
	if err = c.Store.WithContext(ctx).SetJSON("local_tunnel_state:"+n.ID, map[string]any{"ready": ready, "message": message}); err != nil {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&v1.LocalTunnelStatus{Application: &v1.RuntimeApplication{Id: app.ID, Name: settings.Hostname, BackendSource: "local", ServiceName: app.ServiceName, ServiceFqdn: app.ServiceFQDN, ServicePort: uint32(app.ServicePort), BackendPort: uint32(app.BackendPort), BackendAddresses: app.BackendAddresses, PublicHostnames: app.PublicHostnames, AllowedGatewayNodes: app.AllowedGatewayNodes, Enabled: true}, PublicUrl: "https://" + settings.Hostname, Ready: ready, Message: message}), nil
}

func (s *nodeRPC) WithdrawTunnel(ctx context.Context, _ *connect.Request[v1.Empty]) (*connect.Response[v1.TestResult], error) {
	n, err := s.c.localTunnelCaller(ctx)
	if err != nil {
		return nil, rpcFailure(connect.CodePermissionDenied, err)
	}
	if !s.c.provisionMu.TryLock() {
		return nil, rpcFailure(connect.CodeUnavailable, errors.New("provisioning busy; cleanup pending"))
	}
	defer s.c.provisionMu.Unlock()
	_, err = s.c.Store.WithContext(ctx).RetireLocalTunnel(n.ID, 0)
	if err != nil {
		return nil, rpcFailure(connect.CodeUnavailable, err)
	}
	return connect.NewResponse(&v1.TestResult{Ok: true, Message: "Tunnel withdrawn; owned DNS and Service cleanup queued. Host Tailscale configuration is preserved."}), nil
}

func (c *Control) retireExpiredLocalTunnels(ctx context.Context, before int64) error {
	nodes, err := c.Store.WithContext(ctx).Nodes("tailnet-gateway")
	if err != nil {
		return err
	}
	for _, item := range nodes {
		n, err := c.Store.WithContext(ctx).Node(fmt.Sprint(item["id"]))
		if err != nil {
			return err
		}
		if n.LastReport <= 0 || n.LastReport >= before {
			continue
		}
		reg, err := c.Store.WithContext(ctx).ComponentRegistration(n.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if reg.Runtime != tunnel.Runtime {
			continue
		}
		if _, err = c.Store.WithContext(ctx).RetireLocalTunnel(n.ID, before); err != nil {
			return err
		}
	}
	return nil
}
