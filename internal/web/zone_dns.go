package web

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func (c *Control) zoneDesiredContext(ctx context.Context, z store.ManagedZone, node store.NodeRecord) ([]libdns.Record, error) {
	if node.Revoked {
		return nil, nil
	}
	if store.IsGateway(node.Mode) {
		return nil, nil
	}
	deployment, err := c.nodeDeployment(node.ID)
	if err != nil {
		return nil, err
	}
	if deployment.LoadBalancerHostname == "" {
		return nil, nil
	}
	assignment, err := c.managedZone(deployment.LoadBalancerHostname)
	if err != nil {
		return nil, err
	}
	if assignment.ID != z.ID {
		return nil, nil
	}
	var health loadBalancerHealth
	_ = c.Store.GetJSON("node_health:"+node.ID, &health)
	ttl := c.ReportTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if health.ReceivedAt <= 0 || time.Since(time.Unix(health.ReceivedAt, 0)) > ttl {
		return nil, nil
	}
	if deployment.AddressSource == "kubernetes" {
		ips, err := c.kubernetesAddresses(ctx, node, deployment)
		if err != nil {
			return nil, err
		}
		records := make([]libdns.Record, 0, len(ips))
		for _, ip := range ips {
			records = append(records, libdns.RR{Name: deployment.LoadBalancerHostname, Type: "A", Data: ip, TTL: time.Minute})
		}
		return records, nil
	}
	client, err := c.tailscaleRuntimeClient()
	if err != nil {
		return nil, fmt.Errorf("Endpoint lookup failed; previous managed A record retained: %w", err)
	}
	device, err := client.GetDevice(ctx, node.TailnetID)
	if err != nil {
		return nil, fmt.Errorf("Endpoint lookup failed; previous managed A record retained: %w", err)
	}
	record, _, err := dns.LoadBalancerRecord(dns.LoadBalancerEndpoint{Hostname: deployment.LoadBalancerHostname, PublicIPv4: dns.PublicIPv4Endpoints(device.ClientConnectivity.Endpoints)})
	if err != nil {
		return nil, fmt.Errorf("%v; previous managed A record retained", err)
	}
	return []libdns.Record{record}, nil
}

func (c *Control) zoneDNSSyncOperation(ctx context.Context, zoneID string, dryRun bool, approvals map[string]string, actor string) (map[string]any, error) {
	z, err := c.Store.ZoneByID(zoneID)
	if err != nil {
		return nil, rpcFailure(connect.CodeNotFound, errors.New("Managed zone not found"))
	}
	results := []map[string]any{}
	failures := false
	ledger, err := c.Store.PartitionZoneLedger(z.ProviderID, z.Zone)
	if err != nil {
		return nil, rpcFailure(connect.CodeInternal, errors.New("Unable to read managed records"))
	}
	seen := map[string]bool{}
	for _, mode := range []string{"loadbalancer", "docker-gateway"} {
		nodes, err := c.Store.Nodes(mode)
		if err != nil {
			return nil, rpcFailure(connect.CodeInternal, errors.New("Unable to read nodes"))
		}
		for _, item := range nodes {
			id := fmt.Sprint(item["id"])
			node, err := c.Store.Node(id)
			if err != nil {
				return nil, rpcFailure(connect.CodeInternal, errors.New("Unable to read node"))
			}
			owner := mode + ":" + id
			seen[owner] = true
			desired, err := c.zoneDesiredContext(ctx, z, node)
			owns := false
			for _, entry := range ledger {
				if entry.OwnerID == owner {
					owns = true
				}
			}
			if len(desired) == 0 && !owns && err == nil {
				continue
			}
			result := map[string]any{"node_id": id, "node_name": node.Name}
			if err != nil {
				result["error"] = err.Error()
				failures = true
			} else {
				result, err = c.reconcile(ctx, z.ProviderID, z.Zone, owner, desired, dryRun, approvals[id] != "", approvals[id])
				if err != nil {
					result = map[string]any{"error": err.Error()}
					failures = true
				}
				result["node_id"] = id
				result["node_name"] = node.Name
				if result["requires_approval"] == true {
					failures = true
				}
			}
			results = append(results, result)
		}
	}
	exposures, err := c.Store.Exposures()
	if err != nil {
		return nil, rpcFailure(connect.CodeInternal, errors.New("Unable to read exposures"))
	}
	for _, e := range exposures {
		owner := "exposure:" + e.ID
		seen[owner] = true
		records, _ := c.exposureDesired(e, c.loadExposurePolicy())
		desired := []libdns.Record{}
		for _, record := range records {
			assignment, err := c.managedZone(record.RR().Name)
			if err == nil && assignment.ID == z.ID {
				desired = append(desired, record)
			}
		}
		owns := false
		for _, entry := range ledger {
			if entry.OwnerID == owner {
				owns = true
			}
		}
		if !owns && len(desired) == 0 {
			continue
		}
		res, err := c.reconcile(ctx, z.ProviderID, z.Zone, owner, desired, dryRun, approvals[e.ID] != "", approvals[e.ID])
		if err != nil {
			res = map[string]any{"error": err.Error()}
			failures = true
		}
		res["node_id"] = e.ID
		res["node_name"] = e.Hostname
		if res["requires_approval"] == true {
			failures = true
		}
		results = append(results, res)
	}
	for _, entry := range ledger {
		if !seen[entry.OwnerID] {
			failures = true
			results = append(results, map[string]any{"node_name": "Unknown owner", "error": "Managed records reference a missing node. Cleanup requires investigation; records retained."})
			break
		}
	}
	if !dryRun {
		status := "Reconciled"
		if failures {
			status = "Pending / failed: review zone DNS details"
		} else {
			_ = c.Store.SetJSON("zone_last_sync:"+z.ID, time.Now().Unix())
		}
		_ = c.Store.SetJSON("zone_sync:"+z.ProviderID+":"+z.Zone, status)
	}
	_ = c.Store.Audit(map[bool]string{true: "dns.zone-preview:", false: "dns.zone-retry:"}[dryRun]+z.ID, actor)
	return map[string]any{"dry_run": dryRun, "results": results, "pending": failures}, nil
}
