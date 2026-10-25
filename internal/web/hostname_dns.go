package web

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func (c *Control) cloudflareHostname(host string) bool {
	z, err := c.managedZone(host)
	if err != nil {
		return false
	}
	p, _, err := c.Store.ProviderConfig(z.ProviderID)
	return err == nil && p.Provider == "cloudflare" && p.Enabled
}
func hostnameDNSMessage(e store.ExposureRecord) *v1.HostnameDNSSettings {
	return &v1.HostnameDNSSettings{CloudflareProxied: e.CloudflareProxied, Revision: e.DNSRevision}
}
func (s *managementRPC) SaveHostnameDNS(ctx context.Context, r *connect.Request[v1.HostnameDNSRequest]) (*connect.Response[v1.HostnameDNSSettings], error) {
	if err := s.c.syncMu.Lock(ctx); err != nil {
		return nil, err
	}
	defer s.c.syncMu.Unlock()
	e, err := s.c.Store.Exposure(r.Msg.Id)
	if err != nil {
		return nil, rpcFailure(connect.CodeNotFound, errors.New("hostname not found"))
	}
	if r.Msg.CloudflareProxied && !s.c.cloudflareHostname(e.Hostname) {
		return nil, rpcFailure(connect.CodeFailedPrecondition, errors.New("hostname requires an enabled Cloudflare managed zone"))
	}
	if err = s.c.Store.WithContext(ctx).SaveHostnameDNS(e.Hostname, r.Msg.CloudflareProxied, r.Msg.Revision); err != nil {
		if errors.Is(err, store.ErrHostnameDNSConflict) {
			return nil, rpcFailure(connect.CodeAborted, err)
		}
		return nil, rpcFailure(connect.CodeInternal, err)
	}
	_ = s.c.Store.Audit("hostname.dns:"+e.Hostname, rpcActor(ctx))
	return connect.NewResponse(&v1.HostnameDNSSettings{CloudflareProxied: r.Msg.CloudflareProxied, Revision: r.Msg.Revision + 1}), nil
}
