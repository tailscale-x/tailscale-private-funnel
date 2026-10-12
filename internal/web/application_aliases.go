package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

// An alias shares a Docker application's Service, backend and freshness lease.
// It is derived from a validated signed backend report, never a second backend.
func (c *Control) expandDockerAliases(exposures []dns.IngressExposure, apps []store.ApplicationRecord, node store.NodeRecord, roles []string) []dns.IngressExposure {
	return c.expandDockerAliasesForPolicy(exposures, apps, node, roles, c.loadExposurePolicy())
}
func (c *Control) expandDockerAliasesForPolicy(exposures []dns.IngressExposure, apps []store.ApplicationRecord, node store.NodeRecord, roles []string, d policy.Document) []dns.IngressExposure {
	out := append([]dns.IngressExposure(nil), exposures...)
	seen := map[string]bool{}
	for _, e := range exposures {
		seen[e.ApplicationID+":"+e.PublicHostname] = true
	}
	for _, e := range exposures {
		app, ok := approvedApplication(apps, e)
		if !ok || app.BackendSource == "tailnet" || !applicationAllows(app, node) {
			continue
		}
		for _, host := range app.PublicHostnames {
			key := app.ID + ":" + host
			alias := e
			alias.PublicHostname = host
			alias.ApplicationID = app.ID
			alias.ServiceName = app.ServiceName
			if seen[key] || !c.anyEdgeAllows(d, alias.PublicHostname, []string{node.ID, node.TailnetID}, roles) {
				continue
			}
			if _, err := c.managedZone(host); err != nil {
				continue
			}
			seen[key] = true
			out = append(out, alias)
		}
	}
	return out
}

func (c *Control) applicationAlias(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Hostname string `json:"hostname"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil {
		http.Error(w, "Only hostname is accepted", 400)
		return
	}
	in.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Hostname), "."))
	if err := policy.ValidateHostname(in.Hostname); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c.cleanupMu.Lock()
	defer c.cleanupMu.Unlock()
	c.provisionMu.Lock()
	defer c.provisionMu.Unlock()
	apps, err := c.Store.Applications()
	if err != nil {
		http.Error(w, "Unable to read applications", 503)
		return
	}
	var app store.ApplicationRecord
	for _, candidate := range apps {
		if candidate.ID == chi.URLParam(r, "id") {
			app = candidate
		}
		for _, host := range candidate.PublicHostnames {
			if host == in.Hostname {
				http.Error(w, "Hostname already assigned", 409)
				return
			}
		}
	}
	if app.ID == "" {
		http.NotFound(w, r)
		return
	}
	if !app.Enabled || app.BackendSource == "tailnet" {
		http.Error(w, "Select an enabled Docker application", 400)
		return
	}
	if _, err = c.managedZone(in.Hostname); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	allowed := false
	for _, id := range app.AllowedIngressNodes {
		n, err := c.Store.Node(id)
		if err != nil || n.Revoked {
			continue
		}
		var roles []string
		_ = c.Store.GetJSON("node_policy_roles:"+id, &roles)
		if c.anyEdgeAllows(c.loadExposurePolicy(), in.Hostname, []string{id, n.TailnetID}, roles) {
			allowed = true
			break
		}
	}
	if !allowed {
		http.Error(w, "Alias denied by exposure policy", 403)
		return
	}
	app.PublicHostnames = append(app.PublicHostnames, in.Hostname)
	if err = c.Store.UpsertApplication(app); err != nil {
		http.Error(w, "Unable to save alias", 500)
		return
	}
	_ = c.Store.Audit("application.alias:"+app.ID+":"+in.Hostname, identityFromRequest(r))
	writeJSON(w, map[string]any{"application": app, "status": "DNS reconciliation pending; existing backend reports determine availability"})
}
