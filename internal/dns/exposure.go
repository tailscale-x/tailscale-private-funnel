package dns

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

type GatewayExposure struct {
	BackendDeviceID string   `json:"backend_device_id,omitempty"`
	ApplicationID   string   `json:"application_id,omitempty"`
	ServiceName     string   `json:"service_name,omitempty"`
	ContainerID     string   `json:"container_id,omitempty"`
	PublicHostname  string   `json:"public_hostname"`
	Port            uint16   `json:"port"`
	BackendHostname string   `json:"backend_hostname,omitempty"`
	BackendIP       string   `json:"backend_ip,omitempty"`
	Networks        []string `json:"networks,omitempty"`
	Reachable       bool     `json:"reachable"`
}

// GatewayRecords contains only the public records that control owns for an
// application Service. Tailscale Service discovery is private; no public SRV
// record or backend A record is emitted.
type LoadBalancerEndpoint struct {
	Hostname   string   `json:"hostname"`
	PublicIPv4 []string `json:"public_ipv4"`
}

func LoadBalancerRecord(e LoadBalancerEndpoint) (libdns.Record, bool, error) {
	if e.Hostname == "" {
		return nil, false, fmt.Errorf("load balancer hostname is required")
	}
	endpoints := PublicIPv4Endpoints(e.PublicIPv4)
	if len(endpoints) != 1 {
		return nil, true, fmt.Errorf("load balancer requires one unambiguous public IPv4 endpoint")
	}
	ip, err := netip.ParseAddr(endpoints[0])
	if err != nil || !ip.Is4() {
		return nil, true, fmt.Errorf("load balancer endpoint must be IPv4")
	}
	return libdns.RR{Name: strings.TrimSuffix(e.Hostname, "."), Type: "A", Data: ip.String(), TTL: 60 * time.Second}, false, nil
}

// PublicIPv4Endpoints accepts bare addresses or Tailscale endpoint IP:port
// values, deduplicating multiple UDP ports on the same public address.
func PublicIPv4Endpoints(values []string) []string {
	var out []string
	seen := map[netip.Addr]bool{}
	for _, value := range values {
		ip, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			endpoint, err := netip.ParseAddrPort(strings.TrimSpace(value))
			if err != nil {
				continue
			}
			ip = endpoint.Addr()
		}
		if !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip.String())
	}
	return out
}
