package web

import (
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func kubernetesBackendMatches(a store.ApplicationRecord, service string, port uint16) bool {
	for _, b := range a.HTTPBackends {
		if b.ServiceName == service && b.Port == port {
			return true
		}
	}
	return a.ServiceName == service && a.BackendPort == port
}

// Retain only unchanged, still-authorized rules. A sibling's pending backend or
// removal never withdraws a working rule, and deleted/changed rules are not kept.
func (c *Control) kubernetesRoute(n store.NodeRecord, a store.ApplicationRecord, host string, previous []proxy.Route, allowed, eligible bool) proxy.Route {
	out := proxy.Route{Hostname: host}
	if !allowed {
		return out
	}
	for _, rule := range a.HTTPRules {
		retained := false
		for _, prior := range previous {
			if prior.Hostname != host {
				continue
			}
			for _, old := range prior.Rules {
				if publicRouteRevision([]proxy.HTTPRule{old}) == publicRouteRevision([]proxy.HTTPRule{rule}) {
					retained = true
					break
				}
			}
		}
		ready := c.edgeFresh(n)
		if len(rule.Backends) == 0 {
			ready = ready && eligible
		}
		for _, b := range rule.Backends {
			if b.Weight == 0 {
				continue
			}
			found := false
			for _, e := range c.freshReport(n) {
				if e.ApplicationID == a.ID && e.PublicHostname == host && e.Reachable && e.ServiceName == b.ServiceName && e.Port == b.Port {
					found = true
					break
				}
			}
			ready = ready && found
		}
		if retained || ready {
			out.Rules = append(out.Rules, rule)
		}
	}
	return out
}
