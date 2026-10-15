package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/store"
)

func jsonLiteral(v any) string { b, _ := json.Marshal(v); return string(b) }
func vendorOptions(selected string) string {
	var b strings.Builder
	for _, v := range dns.Catalog {
		if dns.Registry[v.ID] != nil {
			fmt.Fprintf(&b, `<option value="%s" %s>%s</option>`, safeText(v.ID), selectedAttr(v.ID == selected), safeText(v.Name))
		}
	}
	return b.String()
}
func vendorName(id string) string {
	for _, spec := range dns.Catalog {
		if spec.ID == id {
			return spec.Name
		}
	}
	return id
}
func selectedAttr(v bool) string {
	if v {
		return "selected"
	}
	return ""
}
func providerOptions(providers []store.ProviderRecord, selected string) string {
	var b strings.Builder
	for _, p := range providers {
		fmt.Fprintf(&b, `<option value="%s" %s>%s</option>`, safeText(p.ID), selectedAttr(p.ID == selected), safeText(p.Name+" · "+vendorName(p.Provider)+" · "+p.AccountID+" · "+p.ID[:min(len(p.ID), 6)]))
	}
	return b.String()
}
func (c *Control) credentialForm(id, vendor, account string) string {
	var b strings.Builder
	name := ""
	if id != "" {
		p, _, _ := c.Store.ProviderConfig(id)
		name = p.Name
	}
	fmt.Fprintf(&b, `<form class="pf-form-grid" data-provider-form data-id="%s"><label class="pf-field"><span class="pf-label">Name</span><input class="pf-input" name="name" required maxlength="128" value="%s"><small class="pf-help">An editable display name. References use an immutable internal ID.</small></label><label class="pf-field"><span class="pf-label">Vendor</span>`, safeText(id), safeText(name))
	if id != "" {
		fmt.Fprintf(&b, `<input class="pf-input" name="provider" readonly value="%s">`, safeText(vendor))
	} else {
		b.WriteString(`<select class="pf-input" name="provider">` + vendorOptions(vendor) + `</select>`)
	}
	b.WriteString(`</label>`)
	cfg := map[string]string{}
	if id != "" {
		_, cfg, _ = c.Store.ProviderConfig(id)
	}
	for _, spec := range dns.Catalog {
		if dns.Registry[spec.ID] == nil {
			continue
		}
		for _, field := range spec.Fields {
			if field == "account_id" {
				continue
			}
			typ, value := "password", ""
			if field == "region" {
				typ = "text"
				value = cfg[field]
			}
			fmt.Fprintf(&b, `<label class="pf-field" data-vendor="%s"><span class="pf-label">%s</span><input class="pf-input" type="%s" name="credential_%s" value="%s" autocomplete="off" placeholder="%s"></label>`, safeText(spec.ID), safeText(strings.ReplaceAll(field, "_", " ")), typ, safeText(spec.ID+"_"+field), safeText(value), map[bool]string{true: "Leave blank to keep the current value", false: "Enter credential"}[id != ""])
		}
	}
	fmt.Fprintf(&b, `<label class="pf-field" data-vendor="cloudflare"><span class="pf-label">Account ID</span><input class="pf-input" name="account_id" value="%s"></label><p class="pf-help pf-field-wide">Credentials are encrypted. Secret fields are never returned to your browser. Cloudflare requires Zone Read and DNS Edit for selected zones.</p><p class="pf-field-wide" data-form-error role="alert"></p><div class="pf-form-actions"><button class="pf-button">%s</button><button type="button" class="pf-secondary-button" data-cancel>Cancel</button></div></form>`, safeText(account), map[bool]string{true: "Save changes", false: "Add DNS provider"}[id != ""])
	return b.String()
}
func (c *Control) providerRows(admin bool) string {
	providers, _ := c.Store.Providers()
	zones, _ := c.Store.Zones()
	var b strings.Builder
	for _, p := range providers {
		n := 0
		for _, z := range zones {
			if z.ProviderID == p.ID {
				n++
			}
		}
		var test string
		_ = c.Store.GetJSON("provider_test:"+p.ID, &test)
		if test == "" {
			test = "Not tested"
		}
		actions := `<a class="pf-text-link" href="/zones?provider_id=` + url.QueryEscape(p.ID) + `">Manage zones</a>`
		if admin {
			actions += fmt.Sprintf(` <button class="pf-link-button" data-edit-provider="%s" aria-expanded="false">Edit DNS provider</button> <button class="pf-link-button" data-provider-action="test" data-id="%s">Test</button> <button class="pf-link-button" data-provider-action="enable" data-id="%s" data-value="%t">%s</button> <button class="pf-link-button pf-link-danger" data-provider-action="delete" data-id="%s">Delete</button>`, safeText(p.ID), safeText(p.ID), safeText(p.ID), !p.Enabled, map[bool]string{true: "Disable", false: "Enable"}[p.Enabled], safeText(p.ID))
		}
		fmt.Fprintf(&b, `<tr data-provider-row="%s"><td data-label="Name"><strong>%s</strong><small class="pf-muted">ID · %s</small></td><td data-label="Vendor">%s</td><td data-label="Account"><code>%s</code></td><td data-label="Managed zones">%d</td><td data-label="State">%s</td><td data-label="Test status">%s<small>%s</small></td><td data-label="Actions"><div class="pf-inline-actions">%s</div></td></tr>`, safeText(p.ID), safeText(p.Name), safeText(p.ID[:min(len(p.ID), 6)]), safeText(vendorName(p.Provider)), safeText(p.AccountID), n, map[bool]string{true: "Enabled", false: "Disabled"}[p.Enabled], safeText(test), safeText(strings.Join(p.Capabilities, ", ")), actions)
		if admin {
			fmt.Fprintf(&b, `<tr class="pf-editor-row" data-editor="%s" hidden><td colspan="7"><h3>Edit DNS provider · %s</h3>%s</td></tr>`, safeText(p.ID), safeText(p.Name), c.credentialForm(p.ID, p.Provider, p.AccountID))
		}
	}
	if len(providers) == 0 {
		b.WriteString(`<tr><td colspan="7">No DNS providers yet. Add a provider to manage your existing domains.</td></tr>`)
	}
	return b.String()
}
func (c *Control) providers(w http.ResponseWriter, r *http.Request) {
	admin := requestAccess(r).Admin
	if r.URL.Query().Get("fragment") == "rows" {
		fmt.Fprint(w, c.providerRows(admin))
		return
	}
	b := pageIntro("Infrastructure", "DNS providers", "Configure a DNS provider once, then manage multiple existing domains with its credentials.") + `<section class="pf-surface"><div class="pf-section-heading"><h2>DNS providers</h2>`
	if admin {
		b += `<button class="pf-button" data-open="add-provider">Add DNS provider</button>`
	}
	b += `</div><div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Name</th><th>Vendor</th><th>Account</th><th>Managed zones</th><th>State</th><th>Test status</th><th>Actions</th></tr></thead><tbody id="provider-rows">` + c.providerRows(admin) + `</tbody></table></div><p id="dns-result" role="status"></p></section>`
	if admin {
		b += `<dialog id="add-provider" class="pf-dialog" aria-labelledby="add-provider-title"><h2 id="add-provider-title">Add DNS provider</h2>` + c.credentialForm("", "cloudflare", "") + `</dialog>`
	}
	render(w, "DNS providers", b+`<script src="/assets/dns-management.js" defer></script><script src="/assets/zone-dns.js" defer></script>`)
}
func (c *Control) zoneRows(admin bool) string {
	zones, _ := c.Store.Zones()
	var b strings.Builder
	var cleanup struct {
		Pending bool `json:"pending"`
	}
	_ = c.Store.GetJSON("policy_cleanup", &cleanup)
	if cleanup.Pending {
		b.WriteString(`<tr><td colspan="5" role="status">Cleanup pending. Policy withdrawal is queued or retrying; DNS removal is not yet confirmed.</td></tr>`)
	}
	for _, z := range zones {
		var test, sync string
		_ = c.Store.GetJSON("zone_test:"+z.ProviderID+":"+z.Zone, &test)
		_ = c.Store.GetJSON("zone_sync:"+z.ProviderID+":"+z.Zone, &sync)
		if test == "" {
			test = "Not tested"
		}
		if sync == "" {
			sync = "Not reconciled"
		}
		if strings.Contains(sync, "Pending") {
			sync = "Sync retry needed: " + sync
		}
		actions := fmt.Sprintf(`<button class="pf-link-button" data-zone-dns="%s" data-domain="%s">DNS details</button>`, safeText(z.ID), safeText(z.Zone))
		if admin {
			actions += " " + fmt.Sprintf(`<button class="pf-link-button" data-zone-action="test" data-zone-id="%s" data-provider="%s">Test read access</button> <button class="pf-link-button pf-link-danger" data-zone-action="delete" data-zone-id="%s" data-provider="%s">Remove</button>`, safeText(z.ID), safeText(z.ProviderID), safeText(z.ID), safeText(z.ProviderID))
		}
		fmt.Fprintf(&b, `<tr data-managed-zone="%s" data-provider="%s"><td data-label="Domain"><code>%s</code><small>Managed by Local Funnel</small></td><td data-label="DNS provider">%s</td><td data-label="Read access">%s</td><td data-label="Reconciliation">%s</td><td data-label="Actions"><div class="pf-inline-actions">%s</div></td></tr>`, safeText(z.Zone), safeText(z.ProviderID), safeText(z.Zone), safeText(c.providerDisplayName(z.ProviderID)), safeText(test), safeText(sync), actions)
	}
	if len(zones) == 0 {
		b.WriteString(`<tr><td colspan="5">No managed zones. Add an existing domain to begin.</td></tr>`)
	}
	return b.String()
}
func (c *Control) zones(w http.ResponseWriter, r *http.Request) {
	admin := requestAccess(r).Admin
	if r.URL.Query().Get("fragment") == "rows" {
		fmt.Fprint(w, c.zoneRows(admin))
		return
	}
	providers, _ := c.Store.Providers()
	options := providerOptions(providers, r.URL.Query().Get("provider_id"))
	b := pageIntro("Infrastructure", "Managed zones", "Add domains from your DNS provider. Inspect managed records, preview changes, and retry DNS sync for each zone.") + `<section class="pf-surface"><div class="pf-section-heading"><h2>Zones</h2>`
	if admin {
		b += `<button class="pf-button" data-open="add-zone">Add zone</button>`
	}
	b += `</div><div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Domain</th><th>DNS provider</th><th>Read access</th><th>Reconciliation</th><th>Actions</th></tr></thead><tbody id="zone-rows">` + c.zoneRows(admin) + `</tbody></table></div><p id="dns-result" role="status"></p></section>`
	if admin {
		b += `<dialog id="add-zone" class="pf-dialog" aria-labelledby="add-zone-title"><h2 id="add-zone-title">Add zone</h2><p>Cloudflare requires <strong>Zone Read and DNS Edit</strong> for every selected zone. Adding a zone does not register a domain or change nameservers.</p><label class="pf-field"><span class="pf-label">DNS provider</span><select class="pf-input" id="zone-provider">` + options + `</select></label><label class="pf-field"><span class="pf-label">Search domains</span><input class="pf-input" id="zone-search" type="search" placeholder="Filter accessible zones"></label><p id="discovery-status" role="status"></p><div id="zone-candidates" class="pf-zone-candidates"></div><button class="pf-secondary-button" id="retry-zones">Retry discovery</button><p id="zone-save-error" role="alert"></p><div class="pf-form-actions"><button class="pf-button" id="add-selected" disabled>Add selected zones</button><button class="pf-secondary-button" data-cancel>Cancel</button></div></dialog>`
	}
	b += c.zoneDNSDialog(r)
	render(w, "Managed zones", b+`<script src="/assets/dns-management.js" defer></script><script src="/assets/zone-dns.js" defer></script>`)
}
