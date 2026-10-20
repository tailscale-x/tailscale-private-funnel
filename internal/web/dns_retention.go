package web

import (
	"fmt"
	"strings"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// Health is not deletion intent. Preserve existing owned DNS during a report or
// control-transport outage, without making stale backends eligible for routing.
// New DNS still requires full readiness; explicit withdrawal is never retained.
func (c *Control) retainedExposureDNS(e store.ExposureRecord, d policy.Document) ([]libdns.Record, error) {
	if !e.Active {
		return nil, nil
	}
	apps, err := c.Store.Applications()
	if err != nil {
		return nil, err
	}
	authorized := false
	for _, a := range apps {
		allowed, err := c.configuredGatewayAllows(a, e.Hostname, d)
		if err != nil {
			return nil, err
		}
		if allowed {
			authorized = true
			break
		}
	}
	if !authorized {
		return nil, nil
	}
	zone, err := c.managedZone(e.Hostname)
	if err != nil {
		return nil, err
	}
	addresses, targets := map[string]bool{}, map[string]bool{}
	edges := c.loadBalancers()
	for _, n := range edges {
		ips, _ := c.edgeIPs(n)
		for _, ip := range ips {
			addresses[ip] = true
		}
		deployment, err := c.nodeDeployment(n.ID)
		if err != nil {
			return nil, err
		}
		if deployment.LoadBalancerHostname != "" {
			targets[strings.TrimSuffix(strings.ToLower(deployment.LoadBalancerHostname), ".")] = true
		}
	}
	if len(edges) > 0 && len(addresses) == 0 && len(targets) == 0 {
		return nil, fmt.Errorf("load balancer address metadata unavailable; retaining owned DNS")
	}
	owner := "exposure:" + e.ID
	partitions, err := c.Store.LedgerPartitions()
	if err != nil {
		return nil, err
	}
	var records []libdns.Record
	for _, p := range partitions {
		if p.OwnerID != owner || p.ProviderID != zone.ProviderID || p.Zone != zone.Zone {
			continue
		}
		ledger, err := c.Store.Ledger(p.ProviderID, p.Zone, p.OwnerID)
		if err != nil {
			return nil, err
		}
		for _, entry := range ledger {
			rr := entry.Record.RR()
			if strings.TrimSuffix(strings.ToLower(rr.Name), ".") != e.Hostname {
				continue
			}
			if rr.Type == "A" && addresses[rr.Data] || rr.Type == "CNAME" && targets[strings.TrimSuffix(strings.ToLower(rr.Data), ".")] {
				records = append(records, entry.Record)
			}
		}
	}
	return records, nil
}
