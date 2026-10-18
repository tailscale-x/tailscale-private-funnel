package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/report"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func (c *Control) loadBalancers() []store.NodeRecord {
	items, err := c.Store.Nodes("loadbalancer")
	if err != nil {
		return nil
	}
	out := []store.NodeRecord{}
	for _, item := range items {
		n, e := c.Store.Node(fmt.Sprint(item["id"]))
		if e == nil && !n.Revoked && n.TailnetID != "" {
			out = append(out, n)
		}
	}
	return out
}
func (c *Control) mapping(host string) (store.ExposureRecord, bool) {
	es, err := c.Store.Exposures()
	if err != nil {
		return store.ExposureRecord{}, false
	}
	for _, e := range es {
		if e.Hostname == strings.ToLower(strings.TrimSuffix(host, ".")) {
			return e, true
		}
	}
	return store.ExposureRecord{}, false
}
func (c *Control) selected(host, id string) bool {
	e, ok := c.mapping(host)
	return ok && e.Active
}

func (c *Control) anyEdgeAllows(d policy.Document, host string, gatewayIDs, roles []string) bool {
	if e, exists := c.mapping(host); exists && !e.Active {
		return false
	}
	ok, _ := d.Allows(host, gatewayIDs, roles)
	return ok
}
func (c *Control) edgeFresh(n store.NodeRecord) bool {
	var h loadBalancerHealth
	if c.Store.GetJSON("node_health:"+n.ID, &h) != nil {
		return false
	}
	ttl := c.ReportTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return !n.Revoked && h.ReceivedAt > 0 && time.Since(time.Unix(h.ReceivedAt, 0)) < ttl
}

// Signed reports have already passed WhoIs, signature and nonce checks on write.
func (c *Control) freshReport(n store.NodeRecord) []dns.GatewayExposure {
	if n.Mode == "k8s-gateway" && !n.Revoked {
		children, e := c.Store.Forwarders(n.ID)
		if e != nil {
			return nil
		}
		if len(children) > 0 {
			out := []dns.GatewayExposure{}
			for _, child := range children {
				out = append(out, c.freshReport(child)...)
			}
			return out
		}
	}

	reports, err := c.Store.Reports()
	if err != nil {
		return nil
	}
	raw, ok := reports[n.ID]
	if !ok || n.Revoked {
		return nil
	}
	ttl := c.ReportTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if n.LastReport < time.Now().Add(-ttl).Unix() {
		return nil
	}
	var env report.Envelope
	var p struct {
		Exposures []dns.GatewayExposure `json:"exposures"`
		ExpiresAt int64                 `json:"expires_at"`
	}
	if json.Unmarshal([]byte(raw), &env) != nil || json.Unmarshal(env.Payload, &p) != nil || p.ExpiresAt <= time.Now().Unix() {
		return nil
	}
	return p.Exposures
}
func (c *Control) edgeReported(n store.NodeRecord, app store.ApplicationRecord, host string) bool {
	for _, e := range c.freshReport(n) {
		if e.Reachable && e.ApplicationID == app.ID && e.PublicHostname == host && e.Port == app.BackendPort {
			if e.ServiceName == app.ServiceName {
				return true
			}
		}
	}
	return false
}
func (c *Control) edgeIPs(n store.NodeRecord) ([]string, bool) {
	var endpoints struct {
		PublicIPv4 []string `json:"public_ipv4"`
		CheckedAt  int64    `json:"checked_at"`
	}
	_ = c.Store.GetJSON("node_endpoints:"+n.ID, &endpoints)
	d, _ := c.nodeDeployment(n.ID)
	if (len(endpoints.PublicIPv4) == 1 || d.AddressSource == "kubernetes" && len(endpoints.PublicIPv4) > 0) && endpoints.CheckedAt > time.Now().Add(-5*time.Minute).Unix() {
		return endpoints.PublicIPv4, false
	}
	// An ambiguous lookup retains only this edge's last owned public address.
	parts, err := c.Store.LedgerPartitions()
	if err != nil {
		return nil, true
	}
	ips := []string{}
	for _, p := range parts {
		if p.OwnerID != "loadbalancer:"+n.ID {
			continue
		}
		entries, e := c.Store.Ledger(p.ProviderID, p.Zone, p.OwnerID)
		if e != nil {
			continue
		}
		for _, entry := range entries {
			rr := entry.Record.RR()
			ip, e := netip.ParseAddr(rr.Data)
			if rr.Type == "A" && e == nil && ip.Is4() {
				ips = append(ips, ip.String())
			}
		}
	}
	return dns.PublicIPv4Endpoints(ips), true
}
func (c *Control) exposureDesired(e store.ExposureRecord, d policy.Document) ([]libdns.Record, []map[string]any) {
	records := []libdns.Record{}
	states := []map[string]any{}
	apps, err := c.Store.Applications()
	if err != nil {
		return records, states
	}
	members := map[string]store.ApplicationRecord{}
	anyEnabled := false
	for _, a := range apps {
		if contains(a.PublicHostnames, e.Hostname) && a.Enabled {
			members[a.ID] = a
			anyEnabled = true
		}
	}
	seen := map[string]bool{}
	authorized := c.authorizedReports(d)
	for _, edge := range c.loadBalancers() {
		reason := "Ready"
		var roles []string
		_ = c.Store.GetJSON("node_policy_roles:"+edge.ID, &roles)
		policyOK := false
		authorizedApps := map[string]bool{}
		for id, exposures := range authorized {
			n, er := c.Store.Node(id)
			if er != nil || !store.IsGateway(n.Mode) {
				continue
			}
			var r []string
			_ = c.Store.GetJSON("node_policy_roles:"+id, &r)
			for _, x := range exposures {
				if _, member := members[x.ApplicationID]; member && x.PublicHostname == e.Hostname {
					if ok, _ := d.Allows(e.Hostname, []string{id, n.TailnetID}, r); ok {
						policyOK = true
						authorizedApps[x.ApplicationID] = true
					}
				}
			}
		}
		selected := true
		fresh := c.edgeFresh(edge)
		backend := false
		for _, app := range members {
			if authorizedApps[app.ID] && c.edgeReported(edge, app, e.Hostname) {
				backend = true
			}
		}
		ips, degraded := c.edgeIPs(edge)
		switch {
		case !e.Active || !anyEnabled:
			reason = "Exposure disabled or removed"
		case !selected:
			reason = "Not selected by override"
		case !policyOK:
			reason = "Policy denied or no authorized backend report"
		case !fresh:
			reason = "Load balancer offline or health expired"
		case !backend:
			reason = "Backend test pending, failed, or expired"
		case len(ips) == 0:
			reason = "Public IPv4 unavailable"
		case degraded:
			reason = "Retaining previous public IPv4; endpoint discovery degraded"
		}
		ready := e.Active && anyEnabled && selected && policyOK && fresh && backend && len(ips) > 0
		states = append(states, map[string]any{"id": edge.ID, "name": edge.Name, "selected": selected, "policy_allowed": policyOK, "healthy": fresh, "backend_reachable": backend, "public_ipv4": ips, "ready": ready, "reason": reason})
		if ready {
			for _, ip := range ips {
				if !seen[ip] {
					records = append(records, libdns.RR{Name: e.Hostname, Type: "A", Data: ip, TTL: 60 * time.Second})
					seen[ip] = true
				}
			}
		}
	}
	return records, states
}
func (c *Control) reconcileExposures(ctx context.Context) error {
	es, err := c.Store.Exposures()
	if err != nil {
		return err
	}
	if _, err = c.Store.Applications(); err != nil {
		return err
	}
	if _, err = c.Store.Reports(); err != nil {
		return err
	}
	if _, err = c.Store.Nodes("loadbalancer"); err != nil {
		return err
	}
	d := c.loadExposurePolicy()
	failures := []string{}
	for _, e := range es {
		records, _ := c.exposureDesired(e, d)
		desired := map[store.LedgerPartition][]libdns.Record{}
		if len(records) > 0 {
			z, err := c.managedZone(e.Hostname)
			if err != nil {
				failures = append(failures, err.Error())
				continue
			}
			desired[store.LedgerPartition{ZoneID: z.ID, ProviderID: z.ProviderID, Zone: z.Zone, OwnerID: "exposure:" + e.ID}] = records
		}
		if err = c.reconcilePartitions(ctx, "exposure:"+e.ID, desired); err != nil {
			failures = append(failures, e.Hostname+": "+err.Error())
		}
	}
	_ = c.Store.SetJSON("exposure_mapping_sync", map[string]any{"pending": len(failures) > 0, "errors": failures, "at": time.Now().Unix()})
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func (c *Control) applicationAnyEdgeAllowed(app store.ApplicationRecord, ids, roles []string) bool {
	for _, host := range app.PublicHostnames {
		if c.anyEdgeAllows(c.loadExposurePolicy(), host, ids, roles) {
			return true
		}
	}
	return false
}

func reportHasRemovedEdgeField(payload json.RawMessage) bool {
	var p struct {
		Exposures []map[string]json.RawMessage `json:"exposures"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return true
	}
	for _, e := range p.Exposures {
		if _, ok := e["loadbalancer_id"]; ok {
			return true
		}
		if _, ok := e["loadbalancer_ids"]; ok {
			return true
		}
	}
	return false
}
