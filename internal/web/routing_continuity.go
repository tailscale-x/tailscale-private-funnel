package web

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// Reports describe health, not removal intent. Durable application membership,
// source inventory, policy and revocation remain authoritative during outages.
func (c *Control) configuredGatewayAllows(a store.ApplicationRecord, host string, d policy.Document) (bool, error) {
	if !a.Enabled || !contains(a.PublicHostnames, host) {
		return false, nil
	}
	nodes, err := c.Store.Nodes("gateways")
	if err != nil {
		return false, err
	}
	for _, item := range nodes {
		n, err := c.Store.Node(fmt.Sprint(item["id"]))
		if err != nil {
			return false, err
		}
		if !applicationAllows(a, n) || n.TailnetID == "" {
			continue
		}
		if n.ParentID != "" {
			p, err := c.Store.Node(n.ParentID)
			if err != nil {
				return false, err
			}
			if p.Revoked {
				continue
			}
		}
		if n.Mode == "docker-gateway" {
			allowed, err := c.Store.DockerDeclarationAllowed(n.ID, a, "", false)
			if err != nil {
				return false, err
			}
			if !allowed {
				continue
			}
		}
		var roles []string
		if err := c.Store.GetJSON("node_policy_roles:"+n.ID, &roles); err != nil {
			// Missing selectors may still match a rule with no role restriction.
			// Other read failures are captured by the request's ReadView.
			roles = nil
		}
		if ok, _ := d.Allows(host, []string{n.ID, n.TailnetID, n.ParentID}, roles); ok {
			return true, nil
		}
	}
	return false, nil
}

func sameRoute(a, b proxy.Route) bool {
	return a.Hostname == b.Hostname && a.Service == b.Service && a.Port == b.Port && publicRouteRevision(a.Rules) == publicRouteRevision(b.Rules)
}

// Pod discovery is gateway-side state, not a change to the public Caddy route.
// Keep backend identity, Service target, ports, weights and every HTTP operation
// in the comparison so continuity cannot authorize a different backend or rule.
func publicRouteRevision(rules []proxy.HTTPRule) string {
	normalized := append([]proxy.HTTPRule(nil), rules...)
	for i := range normalized {
		normalized[i].Backends = append([]proxy.Backend(nil), rules[i].Backends...)
		for j := range normalized[i].Backends {
			normalized[i].Backends[j].Endpoints = nil
		}
	}
	return nodeconfig.Revision(normalized)
}

// Only retain a route that previously passed readiness, and only while its
// exact operational definition and current authorization still match. This is
// not permission to publish new or changed backends from stale health.
func (c *Control) previousLoadBalancerRoutes(id string) ([]proxy.Route, error) {
	var sent struct {
		Routes []proxy.Route `json:"routes"`
	}
	err := c.Store.GetJSON("node_sent_config:"+id, &sent)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return sent.Routes, nil
}
