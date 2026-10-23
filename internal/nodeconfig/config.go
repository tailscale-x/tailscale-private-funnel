// Package nodeconfig defines the control-to-node contract.
package nodeconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	v1 "github.com/tailscale-x/tailscale-private-funnel/internal/gen/funnel/v1"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type Configuration struct {
	PublishedResources map[string]string         `json:"published_resources,omitempty"`
	KubernetesErrors   map[string]string         `json:"kubernetes_errors,omitempty"`
	PublishedAddresses []string                  `json:"published_addresses,omitempty"`
	PublishedHostnames []string                  `json:"published_hostnames,omitempty"`
	Routes             []proxy.Route             `json:"routes,omitempty"`
	Probes             []Probe                   `json:"probes,omitempty"`
	NodeID             string                    `json:"node_id"`
	Revision           string                    `json:"revision"`
	Applications       []store.ApplicationRecord `json:"applications"`
	LabelAliases       []string                  `json:"label_aliases"`
	IssuedAt           int64                     `json:"issued_at"`
}

func Revision(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func Read(e report.Snapshot, nodeID string, now time.Time) (Configuration, error) {
	var config Configuration
	if e.NodeID != nodeID {
		return config, fmt.Errorf("control configuration identity mismatch")
	}
	if err := e.Validate(now, 5*time.Minute); err != nil {
		return config, err
	}
	if err := json.Unmarshal(e.Payload, &config); err != nil {
		return config, err
	}
	if config.NodeID != nodeID || config.Revision == "" {
		return config, fmt.Errorf("invalid configuration target or revision")
	}
	return config, nil
}

func FromPayload(p *v1.ConfigurationPayload, nodeID string, now time.Time) (Configuration, error) {
	s, e := report.NewSnapshot("configuration", nodeID, p, time.Unix(p.IssuedAt, 0))
	if e != nil {
		return Configuration{}, e
	}
	return Read(s, nodeID, now)
}
