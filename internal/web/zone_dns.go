package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// zoneDNSState is a ledger view, not a claim that the vendor's records match it.
func (c *Control) zoneDNSState(w http.ResponseWriter, r *http.Request) {
	z, err := c.Store.ZoneByID(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Managed zone not found", 404)
		return
	}
	ledger, err := c.Store.PartitionZoneLedger(z.ProviderID, z.Zone)
	if err != nil {
		http.Error(w, "Unable to read managed records", 500)
		return
	}
	records := []map[string]any{}
	for _, entry := range ledger {
		rr := entry.Record.RR()
		owner := strings.SplitN(entry.OwnerID, ":", 2)
		management := "Managed by Local Funnel"
		if strings.HasPrefix(entry.OwnerID, "foreign-provider:") {
			management = "Managed by another node"
		}
		name := "Unknown node"
		if len(owner) == 2 {
			if owner[0] == "exposure" {
				if e, err := c.Store.Exposure(owner[1]); err == nil {
					name = e.Hostname
				}
			} else {
				name = c.nodeDisplayName(owner[1])
			}
		}
		records = append(records, map[string]any{"name": rr.Name, "type": rr.Type, "data": rr.Data, "node": name, "management": management})
	}
	var status string
	_ = c.Store.GetJSON("zone_sync:"+z.ProviderID+":"+z.Zone, &status)
	var applied int64
	_ = c.Store.GetJSON("zone_last_sync:"+z.ID, &applied)
	writeJSON(w, map[string]any{"zone": z.Zone, "records": records, "status": status, "last_success": applied})
}

// zoneDesired reconstructs desired state from approved, fresh reports. Failed
// endpoint discovery never becomes an empty desired state for a load balancer.
func (c *Control) zoneDesired(r *http.Request, z store.ManagedZone, node store.NodeRecord) ([]libdns.Record, error) {
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
		ips, err := c.kubernetesAddresses(r.Context(), node, deployment)
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
	device, err := client.GetDevice(r.Context(), node.TailnetID)
	if err != nil {
		return nil, fmt.Errorf("Endpoint lookup failed; previous managed A record retained: %w", err)
	}
	record, _, err := dns.LoadBalancerRecord(dns.LoadBalancerEndpoint{Hostname: deployment.LoadBalancerHostname, PublicIPv4: dns.PublicIPv4Endpoints(device.ClientConnectivity.Endpoints)})
	if err != nil {
		return nil, fmt.Errorf("%v; previous managed A record retained", err)
	}
	return []libdns.Record{record}, nil
}

func (c *Control) zoneDNSSync(w http.ResponseWriter, r *http.Request) {
	z, err := c.Store.ZoneByID(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Managed zone not found", 404)
		return
	}
	var input struct {
		DryRun    bool              `json:"dry_run"`
		Approvals map[string]string `json:"approvals"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&input) != nil {
		http.Error(w, "Invalid zone sync request", 400)
		return
	}
	if !input.DryRun && !requestAccess(r).Admin {
		http.Error(w, "Administrator access required to retry DNS sync", 403)
		return
	}
	if len(input.Approvals) > 0 && !requestAccess(r).Admin {
		http.Error(w, "Administrator approval required", 403)
		return
	}
	results := []map[string]any{}
	failures := false
	ledger, err := c.Store.PartitionZoneLedger(z.ProviderID, z.Zone)
	if err != nil {
		http.Error(w, "Unable to read managed records", 500)
		return
	}
	seen := map[string]bool{}
	for _, mode := range []string{"loadbalancer", "docker-gateway"} {
		nodes, err := c.Store.Nodes(mode)
		if err != nil {
			http.Error(w, "Unable to read nodes", 500)
			return
		}
		for _, item := range nodes {
			id := fmt.Sprint(item["id"])
			node, err := c.Store.Node(id)
			if err != nil {
				http.Error(w, "Unable to read node", 500)
				return
			}
			owner := mode + ":" + id
			seen[owner] = true
			desired, err := c.zoneDesired(r, z, node)
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
				result, err = c.reconcile(r.Context(), z.ProviderID, z.Zone, owner, desired, input.DryRun, input.Approvals[id] != "", input.Approvals[id])
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
		http.Error(w, "Unable to read exposures", 500)
		return
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
		res, err := c.reconcile(r.Context(), z.ProviderID, z.Zone, owner, desired, input.DryRun, input.Approvals[e.ID] != "", input.Approvals[e.ID])
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
	if !input.DryRun {
		status := "Reconciled"
		if failures {
			status = "Pending / failed: review zone DNS details"
		} else {
			_ = c.Store.SetJSON("zone_last_sync:"+z.ID, time.Now().Unix())
		}
		_ = c.Store.SetJSON("zone_sync:"+z.ProviderID+":"+z.Zone, status)
	}
	_ = c.Store.Audit(map[bool]string{true: "dns.zone-preview:", false: "dns.zone-retry:"}[input.DryRun]+z.ID, identityFromRequest(r))
	writeJSON(w, map[string]any{"dry_run": input.DryRun, "results": results, "pending": failures})
}

func (c *Control) zoneDNSDialog(r *http.Request) string {
	b := `<dialog id="zone-dns" class="pf-dialog pf-dns-dialog" aria-labelledby="zone-dns-title"><div class="pf-section-heading"><div><p class="pf-kicker">DNS status &amp; recovery</p><h2 id="zone-dns-title">Zone DNS</h2></div><button class="pf-secondary-button" type="button" data-dns-close>Close</button></div><p class="pf-help">Records are generated from approved exposures and node health. Unmanaged records are left untouched unless an administrator approves a specific conflict.</p><p id="zone-dns-status" role="status" aria-live="polite"></p><div id="zone-dns-records"></div>`
	access := requestAccess(r)
	if access.Operator || access.Admin {
		b += `<div class="pf-form-actions"><button class="pf-secondary-button" type="button" id="zone-dns-preview">Preview changes</button>`
		if access.Admin {
			b += `<button class="pf-button" type="button" id="zone-dns-retry">Retry sync</button>`
		}
		b += `</div>`
	}
	b += `<div id="zone-dns-preview-result" aria-live="polite"></div>`
	if access.Admin {
		b += `<div id="zone-dns-approval" class="pf-alert pf-alert-warn" hidden><label class="pf-check-field"><input type="checkbox" id="zone-dns-approve"><span>Approve the conflicting records shown in this preview. Unrelated records remain untouched.</span></label><button class="pf-button" type="button" id="zone-dns-apply" disabled>Apply approved preview</button></div>`
	}
	return b + `</dialog>`
}
