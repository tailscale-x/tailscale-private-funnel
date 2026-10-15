package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/policy"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

type providerInput struct {
	Name      string            `json:"name"`
	Provider  string            `json:"provider"`
	Config    map[string]string `json:"config"`
	AccountID string            `json:"account_id"`
	Enabled   *bool             `json:"enabled"`
}

func (c *Control) providerList(w http.ResponseWriter, r *http.Request) {
	p, err := c.Store.Providers()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, p)
}
func (c *Control) providerSave(w http.ResponseWriter, r *http.Request) {
	var in providerInput
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil {
			http.Error(w, "invalid DNS provider", 400)
			return
		}
	} else {
		in.Name = r.FormValue("name")
		if r.FormValue("id") != "" || r.FormValue("create_only") != "" {
			http.Error(w, "Resource IDs are server generated", 400)
			return
		}
		in.Provider = r.FormValue("provider")
		in.AccountID = r.FormValue("account_id")
		in.Config = map[string]string{}
		if raw := strings.TrimSpace(r.FormValue("config")); raw != "" {
			if json.Unmarshal([]byte(raw), &in.Config) != nil {
				http.Error(w, "Credentials must be a JSON object", 400)
				return
			}
		}
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		if in.Config == nil {
			in.Config = map[string]string{}
		}
		for _, spec := range dns.Catalog {
			if spec.ID == in.Provider {
				for _, field := range spec.Fields {
					if value := r.FormValue("credential_" + spec.ID + "_" + field); value != "" {
						in.Config[field] = value
					}
				}
			}
		}
	}
	id := chi.URLParam(r, "id")
	creating := id == ""
	enabled := true
	config := map[string]string{}
	if !creating {
		old, existing, err := c.Store.ProviderConfig(id)
		if err != nil {
			http.Error(w, "DNS provider not found", 404)
			return
		}
		if in.Provider != "" && in.Provider != old.Provider {
			http.Error(w, "Vendor cannot be changed", 409)
			return
		}
		in.Provider = old.Provider
		config = existing
		enabled = old.Enabled
		if in.Name == "" {
			in.Name = old.Name
		}
	}
	name, err := store.ValidateName(in.Name)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if dns.Registry[in.Provider] == nil {
		http.Error(w, "Choose a linked vendor", 400)
		return
	}
	if creating {
		id, err = store.NewResourceID()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	credentialsChanged := creating
	for k, v := range in.Config {
		if strings.TrimSpace(v) != "" {
			credentialsChanged = credentialsChanged || config[k] != v
			config[k] = v
		}
	}
	if in.AccountID != "" {
		credentialsChanged = credentialsChanged || config["account_id"] != in.AccountID
		config["account_id"] = in.AccountID
	}
	required := map[string][]string{"cloudflare": {"api_token"}, "godaddy": {"api_token"}, "route53": {"access_key_id", "secret_access_key"}}
	for _, field := range required[in.Provider] {
		if strings.TrimSpace(config[field]) == "" {
			http.Error(w, strings.ReplaceAll(field, "_", " ")+" is required", 400)
			return
		}
	}
	if len(config) == 0 {
		http.Error(w, "credentials are required for a new DNS provider", 400)
		return
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := dns.Registry[in.Provider](config); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var saveErr error
	if credentialsChanged {
		saveErr = c.Store.SaveProvider(id, in.Provider, config, enabled, name)
	} else {
		saveErr = c.Store.RenameResource("provider", id, name)
		if saveErr == nil && in.Enabled != nil {
			saveErr = c.Store.SetProviderEnabled(id, enabled)
		}
	}
	if err := saveErr; err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("provider.save:"+id, identityFromRequest(r))
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", "/providers")
		return
	}
	if creating {
		w.WriteHeader(201)
	}
	p, _, _ := c.Store.ProviderConfig(id)
	writeJSON(w, p)
}
func (c *Control) providerAdapter(id string) (store.ProviderRecord, dns.Provider, error) {
	p, config, err := c.Store.ProviderConfig(id)
	if err != nil {
		return p, nil, fmt.Errorf("DNS provider not found")
	}
	f := dns.Registry[p.Provider]
	if f == nil {
		return p, nil, fmt.Errorf("provider has no linked runtime adapter")
	}
	a, err := f(config)
	return p, a, err
}
func (c *Control) discover(ctx context.Context, id string) ([]libdns.Zone, error) {
	_, a, err := c.providerAdapter(id)
	if err != nil {
		return nil, err
	}
	l, ok := a.(interface {
		ListZones(context.Context) ([]libdns.Zone, error)
	})
	if !ok {
		return nil, fmt.Errorf("zone discovery is unavailable for this vendor")
	}
	return l.ListZones(ctx)
}
func (c *Control) providerDiscover(w http.ResponseWriter, r *http.Request) {
	zones, err := c.discover(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, zones)
}
func (c *Control) providerTest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, a, err := c.providerAdapter(id)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if _, ok := a.(interface {
		ListZones(context.Context) ([]libdns.Zone, error)
	}); ok {
		_, err = c.discover(r.Context(), id)
	} else {
		zones, _ := c.Store.Zones()
		tested := false
		for _, z := range zones {
			if z.ProviderID == id {
				_, err = a.List(r.Context(), z.Zone)
				tested = true
				break
			}
		}
		if !tested {
			err = fmt.Errorf("assign a zone before testing this provider")
		}
	}
	if err != nil {
		_ = c.Store.SetJSON("provider_test:"+id, err.Error())
		http.Error(w, err.Error(), 502)
		return
	}
	_ = c.Store.MarkProviderTested(p.ID, a.Capabilities())
	_ = c.Store.SetJSON("provider_test:"+id, "Read access verified. DNS Edit permission is still required to apply records.")
	_ = c.Store.Audit("provider.test:"+id, identityFromRequest(r))
	writeJSON(w, map[string]any{"read_access": true, "capabilities": a.Capabilities()})
}
func (c *Control) providerDelete(w http.ResponseWriter, r *http.Request) {
	if err := c.Store.DeleteProvider(chi.URLParam(r, "id")); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	_ = c.Store.Audit("provider.delete:"+chi.URLParam(r, "id"), identityFromRequest(r))
	w.WriteHeader(204)
}
func (c *Control) providerEnable(w http.ResponseWriter, r *http.Request) {
	enabled := r.FormValue("enabled") == "true"
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid enabled state", 400)
			return
		}
		enabled = in.Enabled
	}
	if err := c.Store.SetProviderEnabled(chi.URLParam(r, "id"), enabled); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = c.Store.Audit("provider.enable:"+chi.URLParam(r, "id"), identityFromRequest(r))
	writeJSON(w, map[string]any{"enabled": enabled})
}
func (c *Control) zoneList(w http.ResponseWriter, r *http.Request) {
	zones, err := c.Store.Zones()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeJSON(w, zones)
}
func (c *Control) zoneSave(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "JSON is required", 415)
		return
	}
	var in struct {
		ProviderID string `json:"provider_id"`
		Zone       string `json:"zone"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "Only provider_id and zone are accepted", 400)
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		http.Error(w, "Invalid zone request", 400)
		return
	}
	in.Zone = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.Zone), "."))
	if policy.ValidateHostname(in.Zone) != nil || in.ProviderID == "" {
		http.Error(w, "DNS provider and valid domain are required", 400)
		return
	}
	c.syncMu.Lock()
	defer c.syncMu.Unlock()
	zones, err := c.Store.Zones()
	if err != nil {
		http.Error(w, "Unable to read managed zones", 503)
		return
	}
	for _, z := range zones {
		if z.Zone == in.Zone {
			http.Error(w, "Already managed", 409)
			return
		}
	}
	discovered, err := c.discover(r.Context(), in.ProviderID)
	if err != nil {
		http.Error(w, "Unable to verify domain with DNS provider: "+err.Error(), 502)
		return
	}
	count := 0
	for _, z := range discovered {
		if strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z.Name), ".")) == in.Zone {
			count++
		}
	}
	if count == 0 {
		http.Error(w, "Domain was not returned by this DNS provider. Configure it with the vendor and check listing permissions, then retry.", 400)
		return
	}
	if count > 1 {
		http.Error(w, "Multiple provider zones match this domain; resolve the ambiguity with the DNS vendor", 409)
		return
	}
	_, adapter, err := c.providerAdapter(in.ProviderID)
	if err == nil {
		_, err = adapter.List(r.Context(), in.Zone)
	}
	if err != nil {
		http.Error(w, "Domain DNS read access could not be verified; check DNS permissions and vendor setup", 502)
		return
	}
	if err := c.Store.AddZone(in.ProviderID, in.Zone); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	_ = c.Store.Audit("zone.add:"+in.ProviderID+":"+in.Zone, identityFromRequest(r))
	zones, _ = c.Store.Zones()
	for _, zone := range zones {
		if zone.ProviderID == in.ProviderID && zone.Zone == in.Zone {
			writeJSON(w, zone)
			return
		}
	}
}

func (c *Control) zoneTest(w http.ResponseWriter, r *http.Request) {
	assignment, err := c.Store.ZoneByID(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Zone not found", 404)
		return
	}
	provider, zone := assignment.ProviderID, assignment.Zone
	_, a, adapterErr := c.providerAdapter(provider)
	err = adapterErr
	if err == nil {
		_, err = a.List(r.Context(), zone)
	}
	message := "Read access verified. Write access was not tested."
	if err != nil {
		message = err.Error()
	}
	_ = c.Store.SetJSON("zone_test:"+provider+":"+zone, message)
	_ = c.Store.Audit("zone.test:"+provider+":"+zone, identityFromRequest(r))
	if err != nil {
		http.Error(w, message, 502)
		return
	}
	writeJSON(w, map[string]any{"read_access": true})
}
func (c *Control) zoneDelete(w http.ResponseWriter, r *http.Request) {
	assignment, err := c.Store.ZoneByID(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "Zone not found", 404)
		return
	}
	if err := c.Store.DeleteZone(assignment.ProviderID, assignment.Zone); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	_ = c.Store.Audit("zone.delete", identityFromRequest(r))
	w.WriteHeader(204)
}
