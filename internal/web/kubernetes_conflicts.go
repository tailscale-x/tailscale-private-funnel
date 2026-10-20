package web

import (
	"github.com/tailscale-x/tailscale-private-funnel/internal/nodeconfig"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// Keep conflict diagnostics local to the resources that claimed that hostname.
// Foreign applications, their Services and their DNS ledger are never changed.
func partitionKubernetesHosts(hosts []nodeconfig.KubernetesHost, apps []store.ApplicationRecord, nodeID string) ([]nodeconfig.KubernetesHost, map[string]string) {
	accepted := make([]nodeconfig.KubernetesHost, 0, len(hosts))
	rejected := map[string]string{}
	for _, host := range hosts {
		conflict := false
		for _, app := range apps {
			if contains(app.PublicHostnames, host.Hostname) && (app.BackendSource != "kubernetes" || !contains(app.AllowedGatewayNodes, nodeID)) {
				conflict = true
				break
			}
		}
		if !conflict {
			accepted = append(accepted, host)
			continue
		}
		reason := "Hostname " + host.Hostname + " is owned by another gateway or source; clean up the former owner explicitly"
		for _, id := range host.ResourceIDs {
			rejected[id] = reason
		}
		// Host diagnostics remain visible even for clients with no Kubernetes resource IDs.
		rejected[host.Hostname] = reason
	}
	return accepted, rejected
}
