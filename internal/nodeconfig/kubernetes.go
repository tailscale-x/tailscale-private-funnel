package nodeconfig

import "github.com/tailscale-x/tailscale-private-funnel/internal/proxy"

type KubernetesHost struct {
	Hostname    string           `json:"hostname"`
	Rules       []proxy.HTTPRule `json:"rules"`
	Backends    []proxy.Backend  `json:"backends"`
	ResourceIDs []string         `json:"resource_ids"`
}
type KubernetesSnapshot struct {
	Errors    map[string]string `json:"errors,omitempty"`
	ClusterID string            `json:"cluster_id"`
	Hosts     []KubernetesHost  `json:"hosts"`
	ExpiresAt int64             `json:"expires_at"`
}

// KubernetesResourceRevisions binds publication to each resource's effective rules
// across all hostnames. Control-assigned Service addresses and Pod discovery are
// excluded; the Kubernetes Service UID/port and HTTP semantics remain included.
func KubernetesResourceRevisions(hosts []KubernetesHost) map[string]string {
	type entry struct {
		Host string
		Rule proxy.HTTPRule
	}
	resources := map[string][]entry{}
	for _, host := range hosts {
		for i, rule := range host.Rules {
			if i >= len(host.ResourceIDs) {
				continue
			}
			rule.Backends = append([]proxy.Backend(nil), rule.Backends...)
			for j := range rule.Backends {
				rule.Backends[j].Endpoints = nil
				rule.Backends[j].Service = ""
				rule.Backends[j].ServiceName = ""
			}
			id := host.ResourceIDs[i]
			resources[id] = append(resources[id], entry{host.Hostname, rule})
		}
	}
	out := map[string]string{}
	for id, entries := range resources {
		out[id] = Revision(entries)
	}
	return out
}
