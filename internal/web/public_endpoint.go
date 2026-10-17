package web

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
	core "k8s.io/api/core/v1"
)

type publicEndpoint struct {
	Namespace  string                     `json:"namespace"`
	Service    string                     `json:"service"`
	UID        string                     `json:"uid"`
	ObservedAt int64                      `json:"observed_at"`
	Ingress    []core.LoadBalancerIngress `json:"ingress"`
}

func (c *Control) kubernetesAddresses(ctx context.Context, n store.NodeRecord, d nodeDeployment) ([]string, error) {
	var p publicEndpoint
	if err := c.Store.GetJSON("node_public_endpoint:"+n.ID, &p); err != nil {
		return nil, fmt.Errorf("Kubernetes LoadBalancer allocation pending; retaining owned DNS")
	}
	if p.Namespace != d.KubernetesNamespace || p.Service != d.PublicService || p.ObservedAt < time.Now().Add(-time.Minute).Unix() {
		return nil, fmt.Errorf("Kubernetes Service observation stale or mismatched; retaining owned DNS")
	}
	ips := []string{}
	for _, i := range p.Ingress {
		if i.IP != "" {
			ips = append(ips, i.IP)
		}
		if i.Hostname != "" {
			host := strings.TrimSuffix(i.Hostname, ".")
			if policy.ValidateHostname(host) != nil {
				return nil, fmt.Errorf("invalid Service hostname; retaining owned DNS")
			}
			lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
			resolve := c.PublicLookup
			if resolve == nil {
				resolve = net.DefaultResolver.LookupIPAddr
			}
			addresses, e := resolve(lookup, host)
			cancel()
			if e != nil {
				return nil, fmt.Errorf("Service hostname resolution failed; retaining owned DNS")
			}
			for _, a := range addresses {
				ips = append(ips, a.IP.String())
			}
		}
	}
	ips = dns.PublicIPv4Endpoints(ips)
	_ = c.Store.SetJSON("node_endpoints:"+n.ID, map[string]any{"public_ipv4": ips, "checked_at": time.Now().Unix(), "source": "kubernetes", "namespace": p.Namespace, "service": p.Service, "uid": p.UID})
	if len(ips) == 0 {
		return nil, fmt.Errorf("Kubernetes LoadBalancer has no usable public IPv4 address; retaining owned DNS")
	}
	return ips, nil
}
