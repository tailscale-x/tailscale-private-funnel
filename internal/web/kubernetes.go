package web

import (
	"sort"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

func (c *Control) publishedGatewayHosts() []string {
	set := map[string]bool{}
	apps, err := c.Store.Applications()
	if err != nil {
		return nil
	}
	for _, n := range c.loadBalancers() {
		var sent struct {
			Revision string        `json:"revision"`
			Routes   []proxy.Route `json:"routes"`
		}
		var ack struct {
			Revision string `json:"revision"`
			At       int64  `json:"at"`
		}
		if c.Store.GetJSON("node_sent_config:"+n.ID, &sent) != nil || c.Store.GetJSON("node_config_ack:"+n.ID, &ack) != nil || sent.Revision != ack.Revision || ack.At < time.Now().Add(-5*time.Minute).Unix() {
			continue
		}
		for _, r := range sent.Routes {
			for _, app := range apps {
				if app.Enabled && contains(app.PublicHostnames, r.Hostname) && sameRoute(proxy.Route{Hostname: r.Hostname, Service: app.ServiceFQDN, Port: app.ServicePort, Rules: app.HTTPRules}, r) {
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
