package gateway

import (
	"fmt"
	"net"
	"path"
	"sort"
	"strings"
)

type Network struct {
	ID          string
	Name        string
	IPAddress   string
	UserDefined bool
	CIDR        string
}

// ValidateNoOverlap rejects ambiguous multi-network topologies before a backend
// is exposed. Overlapping user-defined CIDRs could otherwise send traffic to
// an unintended backend network.
func ValidateNoOverlap(networks []Network) error {
	for i := range networks {
		if networks[i].CIDR == "" {
			continue
		}
		_, left, err := net.ParseCIDR(networks[i].CIDR)
		if err != nil {
			return fmt.Errorf("network %s has invalid CIDR %q", networks[i].Name, networks[i].CIDR)
		}
		for j := i + 1; j < len(networks); j++ {
			if networks[j].CIDR == "" {
				continue
			}
			_, right, err := net.ParseCIDR(networks[j].CIDR)
			if err != nil {
				return fmt.Errorf("network %s has invalid CIDR %q", networks[j].Name, networks[j].CIDR)
			}
			if left.Contains(right.IP) || right.Contains(left.IP) {
				return fmt.Errorf("Docker networks %s and %s have overlapping CIDRs", networks[i].Name, networks[j].Name)
			}
		}
	}
	return nil
}

// SelectNetworks implements the local-only topology policy. Control never
// decides whether a Docker network name is safe; the host operator can deny
// names or IDs here before backend addresses are selected.
func SelectNetworks(networks []Network, denyPatterns []string) []Network {
	var selected []Network
	for _, network := range networks {
		name := strings.ToLower(strings.TrimSpace(network.Name))
		if name == "" || name == "host" || name == "none" || !network.UserDefined {
			continue
		}
		denied := false
		for _, pattern := range denyPatterns {
			pattern = strings.TrimSpace(strings.ToLower(pattern))
			if pattern == "" {
				continue
			}
			ok, _ := path.Match(pattern, name)
			okID, _ := path.Match(pattern, strings.ToLower(network.ID))
			if ok || okID {
				denied = true
				break
			}
		}
		if !denied {
			selected = append(selected, network)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID < selected[j].ID })
	return selected
}
