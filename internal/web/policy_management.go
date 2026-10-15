package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
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
func (c *Control) exposureAllowed(e dns.GatewayExposure, ids, roles []string) bool {
	return c.anyEdgeAllows(c.loadExposurePolicy(), e.PublicHostname, ids, roles)
}
func (c *Control) policyGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, c.loadExposurePolicy())
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(v)
}
func readPolicy(w http.ResponseWriter, r *http.Request) (policy.Document, error) {
	var d policy.Document
	var err error
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(&d)
	} else {
		err = json.Unmarshal([]byte(r.FormValue("policy")), &d)
	}
	if err != nil {
		return d, fmt.Errorf("invalid policy JSON: %w", err)
	}
	return d, d.Validate()
}
func (c *Control) policyValidate(w http.ResponseWriter, r *http.Request) {
	d, err := readPolicy(w, r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"valid": true, "rules": len(d.Rules), "saved": false})
}
func (c *Control) policyPreview(w http.ResponseWriter, r *http.Request) {
	d, err := readPolicy(w, r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"affected_exposures": c.policyImpact(d), "revision": c.loadExposurePolicy().Revision})
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
func (c *Control) savePolicy(w http.ResponseWriter, r *http.Request, d policy.Document) {
	for i := range d.Rules {
		name, err := store.ValidateName(d.Rules[i].Name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		d.Rules[i].Name = name
		for _, id := range d.Rules[i].GatewayIDs {
			n, err := c.Store.Node(id)
			if err != nil || !store.IsGateway(n.Mode) {
				http.Error(w, "Unknown gateway reference", 400)
				return
			}
		}
	}
	if err := d.Validate(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.policyMu.Lock()
	if err := c.policyStorageError(); err != nil {
		c.policyMu.Unlock()
		http.Error(w, "Exposure policy unavailable; retry later", 503)
		return
	}
	old := c.policyLocked()
	if d.Revision != old.Revision {
		c.policyMu.Unlock()
		http.Error(w, "Policy changed. Reload before saving.", 409)
		return
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
		http.Error(w, err.Error(), 500)
		return
	}
	action := "exposure-policy.save"
	if id := chi.URLParam(r, "id"); id != "" {
		action = "exposure-policy.rule." + strings.ToLower(r.Method) + ":" + id
	}
	_ = c.Store.Audit(action+fmt.Sprintf(":revision:%d", d.Revision), identityFromRequest(r))
	_ = c.Store.SetJSON("policy_cleanup", map[string]any{"pending": true, "revision": d.Revision, "message": "Policy saved. Routes withdraw on node polling; DNS cleanup is queued."})
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	_ = c.ReconcilePolicy(ctx)
	cancel()
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/policies")
		return
	}
	writeJSON(w, map[string]any{"policy": d, "cleanup_pending": true})
}
func (c *Control) policySave(w http.ResponseWriter, r *http.Request) {
	d, err := readPolicy(w, r)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.savePolicy(w, r, d)
}
func (c *Control) policyRuleSave(w http.ResponseWriter, r *http.Request) {
	d := c.loadExposurePolicy()
	var rule policy.Rule
	rev := uint64(0)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var input struct {
			Revision uint64      `json:"revision"`
			Rule     policy.Rule `json:"rule"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			http.Error(w, "invalid rule", 400)
			return
		}
		rule = input.Rule
		rev = input.Revision
	} else {
		rev, _ = strconv.ParseUint(r.FormValue("revision"), 10, 64)
		rule = policy.Rule{Name: r.FormValue("name"), HostnameRegexes: r.Form["hostname_regexes"], Roles: splitValues(r.FormValue("roles")), GatewayIDs: splitValues(r.FormValue("gateway_ids")), Enabled: r.FormValue("enabled") == "on"}
	}
	if rev != d.Revision {
		http.Error(w, "Policy changed. Reload before saving.", 409)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" && rule.ID != "" {
		http.Error(w, "Rule IDs are server generated", 400)
		return
	}
	if id != "" && rule.ID != "" && rule.ID != id {
		http.Error(w, "Rule ID is immutable", 400)
		return
	}
	name, nameErr := store.ValidateName(rule.Name)
	if nameErr != nil {
		http.Error(w, nameErr.Error(), 400)
		return
	}
	rule.Name = name
	if id == "" {
		d.Rules = append(d.Rules, rule)
	} else {
		found := false
		for i := range d.Rules {
			if d.Rules[i].ID == id {
				rule.ID = id
				d.Rules[i] = rule
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
	}
	c.savePolicy(w, r, d)
}
func (c *Control) policyRuleDelete(w http.ResponseWriter, r *http.Request) {
	d := c.loadExposurePolicy()
	rev, _ := strconv.ParseUint(r.URL.Query().Get("revision"), 10, 64)
	if rev != d.Revision {
		http.Error(w, "Policy changed. Reload before deleting.", 409)
		return
	}
	for i, rule := range d.Rules {
		if rule.ID == chi.URLParam(r, "id") {
			d.Rules = append(d.Rules[:i], d.Rules[i+1:]...)
			c.savePolicy(w, r, d)
			return
		}
	}
	http.NotFound(w, r)
}
func (c *Control) policyTest(w http.ResponseWriter, r *http.Request) {
	if r.FormValue("loadbalancer_id") != "" || r.FormValue("loadbalancer_ids") != "" {
		http.Error(w, "load-balancer selection is control-managed", 400)
		return
	}
	h := r.FormValue("hostname")
	ok, name := c.loadExposurePolicy().Allows(h, splitValues(r.FormValue("gateway_ids")), splitValues(r.FormValue("roles")))
	writeJSON(w, map[string]any{"allowed": ok, "matched_rule": name, "hostname": h})
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
				if !applicationAllows(a, n) || a.ID != e.ApplicationID || !contains(a.PublicHostnames, e.PublicHostname) || a.ServiceName != e.ServiceName || a.BackendPort != e.Port {
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

// retainOwnedType keeps records managed by another reconciliation loop for the
// same load-balancer owner. The endpoint loop owns A records; reports own CNAMEs.
func (c *Control) retainOwnedType(owner, recordType string, desired map[store.LedgerPartition][]libdns.Record) error {
	parts, err := c.Store.LedgerPartitions()
	if err != nil {
		return err
	}
	for _, p := range parts {
		if p.OwnerID != owner {
			continue
		}
		entries, err := c.Store.Ledger(p.ProviderID, p.Zone, owner)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Record.RR().Type, recordType) {
				desired[p] = append(desired[p], entry.Record)
			}
		}
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
