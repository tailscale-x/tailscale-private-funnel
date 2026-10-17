package web

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"

	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// An alias shares a Docker application's Service, backend and freshness lease.
// It is derived from a validated signed backend report, never a second backend.
func (c *Control) expandDockerAliases(exposures []dns.GatewayExposure, apps []store.ApplicationRecord, node store.NodeRecord, roles []string) []dns.GatewayExposure {
	return c.expandDockerAliasesForPolicy(exposures, apps, node, roles, c.loadExposurePolicy())
}
func (c *Control) expandDockerAliasesForPolicy(exposures []dns.GatewayExposure, apps []store.ApplicationRecord, node store.NodeRecord, roles []string, d policy.Document) []dns.GatewayExposure {
	out := append([]dns.GatewayExposure(nil), exposures...)
	seen := map[string]bool{}
	for _, e := range exposures {
		seen[e.ApplicationID+":"+e.PublicHostname] = true
	}
	for _, e := range exposures {
		app, ok := approvedApplication(apps, e)
		if !ok || app.BackendSource == "tailnet" || !applicationAllows(app, node) {
			continue
		}
		for _, host := range app.PublicHostnames {
			key := app.ID + ":" + host
			alias := e
			alias.PublicHostname = host
			alias.ApplicationID = app.ID
			alias.ServiceName = app.ServiceName
			if seen[key] || !c.anyEdgeAllows(d, alias.PublicHostname, []string{node.ID, node.TailnetID}, roles) {
				continue
			}
			if _, err := c.managedZone(host); err != nil {
				continue
			}
			seen[key] = true
			out = append(out, alias)
		}
	}
	return out
}

func (c *Control) addAliasOperation(ctx context.Context, id, hostname, actor string) (store.ApplicationRecord, error) {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if err := policy.ValidateHostname(hostname); err != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New(err.Error()))
	}
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, err := c.Store.Applications()
	if err != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeUnavailable, errors.New("Unable to read applications"))
	}
	var app store.ApplicationRecord
	for _, candidate := range apps {
		if candidate.ID == id {
			app = candidate
		}
		for _, host := range candidate.PublicHostnames {
			if host == hostname {
				return store.ApplicationRecord{}, rpcFailure(connect.CodeAborted, errors.New("Hostname already assigned"))
			}
		}
	}
	if app.ID == "" {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeNotFound, errors.New("Resource not found"))
	}
	if !app.Enabled || app.BackendSource == "tailnet" {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New("Select an enabled Docker application"))
	}
	if _, err = c.managedZone(hostname); err != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeInvalidArgument, errors.New(err.Error()))
	}
	allowed := false
	for _, id := range app.AllowedGatewayNodes {
		n, err := c.Store.Node(id)
		if err != nil || n.Revoked {
			continue
		}
		var roles []string
		_ = c.Store.GetJSON("node_policy_roles:"+id, &roles)
		if c.anyEdgeAllows(c.loadExposurePolicy(), hostname, []string{id, n.TailnetID}, roles) {
			allowed = true
			break
		}
	}
	if !allowed {
		return store.ApplicationRecord{}, rpcFailure(connect.CodePermissionDenied, errors.New("Alias denied by exposure policy"))
	}
	app.PublicHostnames = append(app.PublicHostnames, hostname)
	if err = c.Store.UpsertApplication(app); err != nil {
		return store.ApplicationRecord{}, rpcFailure(connect.CodeInternal, errors.New("Unable to save alias"))
	}
	_ = c.Store.Audit("application.alias:"+app.ID+":"+hostname, actor)
	return app, nil
}
