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
