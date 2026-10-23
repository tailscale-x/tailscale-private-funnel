package web

import (
	"context"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/identity"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	"github.com/tailscale-x/tailscale-private-funnel/internal/tailscale"
	"strings"
)

// The caller serializes provisioning and validates policy/hostname ownership.
func (c *Control) provisionApplicationService(ctx context.Context, app store.ApplicationRecord) (store.ApplicationRecord, error) {
	client, err := c.tailscaleRuntimeClient()
	if err != nil {
		return app, err
	}
	name := app.Name
	if len(app.PublicHostnames) > 0 {
		name = app.PublicHostnames[0]
	}
	service, err := client.CreateService(ctx, tailscale.Service{Name: app.ServiceName, DisplayName: name, Ports: []string{fmt.Sprintf("tcp:%d", app.ServicePort)}, Tags: []string{identity.ServiceTag(c.installation(), false)}})
	if err != nil {
		return app, err
	}
	app.ServiceFQDN = selectServiceTarget(service.Addresses, app.ServiceName, client.Tailnet)
	// Keep foreground Services address-family neutral. The selected embedded
	// transport resolves this canonical name through its own LocalAPI DNS.
	if app.BackendSource == "local" {
		suffix := strings.TrimSuffix(client.Tailnet, ".")
		parts := strings.SplitN(strings.TrimSuffix(rpcPrincipal(ctx).Hostname, "."), ".", 2)
		if len(parts) == 2 && strings.HasSuffix(parts[1], ".ts.net") {
			suffix = parts[1]
		}
		if strings.HasSuffix(suffix, ".ts.net") {
			app.ServiceFQDN = selectServiceTarget(nil, app.ServiceName, suffix)
		}
	}

	for i := range app.HTTPRules {
		for j := range app.HTTPRules[i].Backends {
			app.HTTPRules[i].Backends[j].Service = app.ServiceFQDN
		}
	}
	return app, nil
}
