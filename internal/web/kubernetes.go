package web

import (
	"sort"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func (c *Control) publishedGatewayHosts() []string {
	set := map[string]bool{}
	apps, err := c.Store.Applications()
	if err != nil {
		return nil
	}
	for _, n := range c.loadBalancers() {
		for _, r := range c.acknowledgedRoutes(n.ID) {
			for _, app := range apps {
				matches := sameRoute(proxy.Route{Hostname: r.Hostname, Service: app.ServiceFQDN, Port: app.ServicePort, Rules: app.HTTPRules}, r)
				if app.BackendSource == "kubernetes" && len(app.HTTPRules) > 0 {
					matches = true
					for _, rule := range app.HTTPRules {
						matches = matches && ruleInstalled(r.Hostname, rule, []proxy.Route{r})
					}
				}
				if app.Enabled && contains(app.PublicHostnames, r.Hostname) && matches {
					set[r.Hostname] = true
				}
			}
		}
	}
	out := []string{}
	for host := range set {
		out = append(out, host)
	}
	sort.Strings(out)
	return out
}

// Read the last configuration the edge actually acknowledged, rather than the
// latest configuration sent by any of its polling/probing requests.
func (c *Control) acknowledgedRoutes(id string) []proxy.Route {
	var applied struct {
		Routes []proxy.Route `json:"routes"`
		At     int64         `json:"at"`
	}
	if c.Store.GetJSON("node_applied_config:"+id, &applied) != nil || applied.At < time.Now().Add(-5*time.Minute).Unix() {
		return nil
	}
	return applied.Routes
}

func ruleInstalled(host string, rule proxy.HTTPRule, installed []proxy.Route) bool {
	for _, route := range installed {
		if route.Hostname != host {
			continue
		}
		for _, current := range route.Rules {
			if publicRouteRevision([]proxy.HTTPRule{rule}) == publicRouteRevision([]proxy.HTTPRule{current}) {
				return true
			}
		}
	}
	return false
}

func (c *Control) publishedKubernetesResources(nodeID string, snapshot nodeconfig.KubernetesSnapshot, apps []store.ApplicationRecord) map[string]string {
	revisions := nodeconfig.KubernetesResourceRevisions(snapshot.Hosts)
	published := map[string]string{}
	// A resource can span hosts; one edge must have installed all of its rules.
	for _, edge := range c.loadBalancers() {
		installed := c.acknowledgedRoutes(edge.ID)
		ready := map[string]bool{}
		for id := range revisions {
			ready[id] = true
		}
		for _, host := range snapshot.Hosts {
			var desired []proxy.HTTPRule
			for _, app := range apps {
				if app.Enabled && app.BackendSource == "kubernetes" && contains(app.AllowedGatewayNodes, nodeID) && contains(app.PublicHostnames, host.Hostname) {
					desired = app.HTTPRules
					break
				}
			}
			for i, id := range host.ResourceIDs {
				if snapshot.Errors[id] != "" || i >= len(desired) || i >= len(host.Rules) || kubernetesRuleRevision(host.Hostname, desired[i]) != kubernetesRuleRevision(host.Hostname, host.Rules[i]) || !ruleInstalled(host.Hostname, desired[i], installed) {
					ready[id] = false
				}
			}
		}
		for id, ok := range ready {
			if ok {
				published[id] = revisions[id]
			}
		}
	}
	return published
}

func kubernetesRuleRevision(host string, rule proxy.HTTPRule) string {
	return nodeconfig.KubernetesResourceRevisions([]nodeconfig.KubernetesHost{{Hostname: host, Rules: []proxy.HTTPRule{rule}, ResourceIDs: []string{"rule"}}})["rule"]
}
