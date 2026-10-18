package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// A failed read is not an empty desired policy. Preserve DNS and stored rules.
func (c *Control) policyStorageError() error {
	var d policy.Document
	if err := c.Store.GetJSON("exposure_policy", &d); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("exposure policy unavailable: %w", err)
	}
	return d.Validate()
}
func (c *Control) loadExposurePolicy() policy.Document {
	c.policyMu.Lock()
	defer c.policyMu.Unlock()
	return c.policyLocked()
}
func (c *Control) policyLocked() policy.Document {
	d := policy.Document{Rules: []policy.Rule{}}
	if err := c.Store.GetJSON("exposure_policy", &d); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return policy.Document{Rules: []policy.Rule{}}
	}
	if d.Rules == nil {
		d.Rules = []policy.Rule{}
	}
	changed := false
	for i := range d.Rules {
		if d.Rules[i].ID == "" {
			b, _ := json.Marshal(d.Rules[i])
			d.Rules[i].ID = fmt.Sprintf("rule-%x", sha256.Sum256(append(b, byte(i))))[:29]
			changed = true
		}
	}
	if d.Revision == 0 {
		d.Revision = 1
		changed = true
	}
	if changed {
		_ = c.Store.SetJSON("exposure_policy", d)
	}
	return d
}

func (c *Control) policyImpact(d policy.Document) []string {
	old := c.authorizedHostnames(c.loadExposurePolicy())
	next := c.authorizedHostnames(d)
	out := []string{}
	for h := range old {
		if !next[h] {
			out = append(out, h)
		}
	}
	return out
}

// Desired DNS and routes are reconstructed from signed, unexpired reports and
// verified selectors saved by the report handler, never client supplied roles.
func (c *Control) authorizedReports(d policy.Document) map[string][]dns.GatewayExposure {
	out := map[string][]dns.GatewayExposure{}
	apps, err := c.Store.Applications()
	if err != nil {
		return out
	}
	nodes, err := c.Store.Nodes("gateways")
	if err != nil {
		return out
	}
	for _, item := range nodes {
		n, err := c.Store.Node(fmt.Sprint(item["id"]))
		if err != nil || n.Revoked {
			continue
		}
		var roles []string
		_ = c.Store.GetJSON("node_policy_roles:"+n.ID, &roles)
		exposures := c.freshReport(n)
		if n.Mode == "docker-gateway" {
			exposures = c.expandDockerAliasesForPolicy(exposures, apps, n, roles, d)
		}
		for _, e := range exposures {
			if !e.Reachable {
				continue
			}
			for _, a := range apps {
				if !applicationAllows(a, n) || !dockerReportRouteMatches(a, e) || a.ID != e.ApplicationID || !contains(a.PublicHostnames, e.PublicHostname) || a.ServiceName != e.ServiceName || a.BackendPort != e.Port {
					continue
				}
				if _, err := c.managedZone(e.PublicHostname); err != nil {
					continue
				}
				if ok, _ := d.Allows(e.PublicHostname, []string{n.ID, n.TailnetID}, roles); !ok {
					continue
				}
				out[n.ID] = append(out[n.ID], e)
				break
			}
		}
	}
	return out
}
func (c *Control) authorizedHostnames(d policy.Document) map[string]bool {
	out := map[string]bool{}
	for _, exposures := range c.authorizedReports(d) {
		for _, e := range exposures {
			out[strings.ToLower(strings.TrimSuffix(e.PublicHostname, "."))] = true
		}
	}
	return out
}
func (c *Control) managedZone(host string) (store.ManagedZone, error) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	zones, err := c.Store.Zones()
	if err != nil {
		return store.ManagedZone{}, err
	}
	providers, err := c.Store.Providers()
	if err != nil {
		return store.ManagedZone{}, err
	}
	enabled := map[string]bool{}
	for _, p := range providers {
		enabled[p.ID] = p.Enabled
	}
	best := store.ManagedZone{}
	ambiguous := false
	for _, z := range zones {
		if !enabled[z.ProviderID] || !(host == z.Zone || strings.HasSuffix(host, "."+z.Zone)) {
			continue
		}
		if len(z.Zone) > len(best.Zone) {
			best = z
			ambiguous = false
		} else if len(z.Zone) == len(best.Zone) {
			ambiguous = true
		}
	}
	if ambiguous {
		return best, fmt.Errorf("hostname %s has ambiguous managed zone assignments", host)
	}
	if best.Zone == "" {
		return best, fmt.Errorf("hostname %s has no enabled managed DNS zone", host)
	}
	return best, nil
}
func (c *Control) reconcilePartitions(ctx context.Context, owner string, desired map[store.LedgerPartition][]libdns.Record) error {
	if desired == nil {
		desired = map[store.LedgerPartition][]libdns.Record{}
	}
	zones, zoneErr := c.Store.Zones()
	if zoneErr != nil {
		return zoneErr
	}
	normalized := map[store.LedgerPartition][]libdns.Record{}
	for p, records := range desired {
		for _, z := range zones {
			if z.ProviderID == p.ProviderID && z.Zone == p.Zone {
				p.ZoneID = z.ID
				break
			}
		}
		normalized[p] = append(normalized[p], records...)
	}
	desired = normalized
	parts, err := c.Store.LedgerPartitions()
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.OwnerID == owner {
			if _, ok := desired[p]; !ok {
				desired[p] = nil
			}
		}
	}
	var failures []string
	for p, records := range desired {
		if _, err := c.reconcile(ctx, p.ProviderID, p.Zone, p.OwnerID, records, false, false); err != nil {
			failures = append(failures, fmt.Sprintf("%s/%s: %v", p.ProviderID, p.Zone, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
}

func (c *Control) ReconcilePolicy(ctx context.Context) error {
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
	if err := c.policyStorageError(); err != nil {
		return err
	}
	d := c.loadExposurePolicy()
	var failures []string
	if err := c.reconcileExposures(ctx); err != nil {
		failures = append(failures, err.Error())
	}
	if err := c.ReconcileServiceApprovals(ctx); err != nil {
		failures = append(failures, err.Error())
	}
	message := "Owned DNS reconciled. Nodes apply policy on their next configuration poll."
	if len(failures) > 0 {
		message = strings.Join(failures, "; ")
	}
	var previous struct {
		Pending bool   `json:"pending"`
		Message string `json:"message"`
	}
	_ = c.Store.GetJSON("policy_cleanup", &previous)
	if previous.Pending || previous.Message != message {
		_ = c.Store.Audit("exposure-policy.reconcile", message)
	}
	_ = c.Store.SetJSON("policy_cleanup", map[string]any{"pending": len(failures) > 0, "revision": d.Revision, "message": message, "at": time.Now().Unix()})
	if len(failures) > 0 {
		return fmt.Errorf("%s", message)
	}
	return nil
}

func (c *Control) savePolicyOperation(ctx context.Context, d policy.Document, actor string) (policy.Document, error) {
	for i := range d.Rules {
		name, err := store.ValidateName(d.Rules[i].Name)
		if err != nil {
			return policy.Document{}, rpcFailure(connect.CodeInvalidArgument, errors.New(err.Error()))
		}
		d.Rules[i].Name = name
		for _, id := range d.Rules[i].GatewayIDs {
			n, err := c.Store.Node(id)
			if err != nil || !store.IsGateway(n.Mode) {
				return policy.Document{}, rpcFailure(connect.CodeInvalidArgument, errors.New("Unknown gateway reference"))
			}
		}
	}
	if err := d.Validate(); err != nil {
		return policy.Document{}, rpcFailure(connect.CodeInvalidArgument, errors.New(err.Error()))
	}
	c.policyMu.Lock()
	if err := c.policyStorageError(); err != nil {
		c.policyMu.Unlock()
		return policy.Document{}, rpcFailure(connect.CodeUnavailable, errors.New("Exposure policy unavailable; retry later"))
	}
	old := c.policyLocked()
	if d.Revision != old.Revision {
		c.policyMu.Unlock()
		return policy.Document{}, rpcFailure(connect.CodeAborted, errors.New("Policy changed. Reload before saving."))
	}
	d.Revision++
	for i := range d.Rules {
		if d.Rules[i].ID == "" {
			d.Rules[i].ID = fmt.Sprintf("rule-%x", sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%d", time.Now().UnixNano(), d.Revision, i))))[:29]
		}
	}
	err := c.Store.SetJSON("exposure_policy", d)
	c.policyMu.Unlock()
	if err != nil {
		return policy.Document{}, rpcFailure(connect.CodeInternal, errors.New(err.Error()))
	}
	_ = c.Store.Audit(fmt.Sprintf("exposure-policy.save:revision:%d", d.Revision), actor)
	_ = c.Store.SetJSON("policy_cleanup", map[string]any{"pending": true, "revision": d.Revision, "message": "Policy saved. Routes withdraw on node polling; DNS cleanup is queued."})
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = c.ReconcilePolicy(bounded)
	return d, nil
}
