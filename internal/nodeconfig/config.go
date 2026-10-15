// Package nodeconfig defines the signed control-to-node contract.
package nodeconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type Configuration struct {
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
func Verify(e report.Envelope, pinnedKey, nodeID string, now time.Time) (Configuration, error) {
	var config Configuration
	if pinnedKey == "" || e.PublicKey != pinnedKey || e.NodeID != nodeID {
		return config, fmt.Errorf("control configuration identity mismatch")
	}
	if _, _, err := report.Verify(e, now, 5*time.Minute); err != nil {
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
