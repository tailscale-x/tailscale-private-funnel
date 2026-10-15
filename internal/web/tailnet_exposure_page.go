package web

import (
	"fmt"
	"net/http"
	"strings"
)

func (c *Control) tailnetExposurePage(w http.ResponseWriter, r *http.Request) {
	if !requestAccess(r).Admin {
		http.Error(w, "Administrator required to manage tailnet exposures", 403)
		return
	}
	nodes, err := c.Store.Nodes("tailnet-gateway")
	if err != nil {
		http.Error(w, "Unable to read tailnet gateways", 503)
		return
	}
	var options strings.Builder
	options.WriteString(`<option value="">Select tailnet gateway</option>`)
	for _, node := range nodes {
		if node["revoked"] == true {
			continue
		}
		id := stringValue(node, "id", "")
		fmt.Fprintf(&options, `<option value="%s">%s</option>`, safeText(id), safeText(stringValue(node, "name", id)+" · "+stringValue(node, "hostname", id)))
	}
	b := pageIntro("Infrastructure", "Expose a tailnet machine", "Public HTTPS goes from each eligible tailnet gateway through the selected tailnet gateway to the backend machine over Tailscale. Tailscale grants decide which backend machines that tailnet gateway can reach.")
	b += `<div class="pf-alert pf-alert-info">Manage additional tailnet gateway tags and access grants in Tailscale. Local Funnel does not modify ACLs. Visible machines still require a successful TCP connection test. This workflow supports HTTP backends.</div><section class="pf-surface pf-form-surface"><h2 id="remote-heading">New tailnet exposure</h2><form id="remote-form" class="pf-form-grid"><label class="pf-field"><span class="pf-label">Discover and test through</span><select class="pf-input" id="remote-node" required>` + options.String() + `</select><small class="pf-help">Choose an edge for discovery and the initial connection test. All eligible tailnet gateways serve this exposure by default; Control assigns load balancers automatically.</small></label><label class="pf-field"><span class="pf-label">Backend machine</span><select class="pf-input" id="remote-peer" required disabled><option value="">Choose tailnet gateway first</option></select></label><p class="pf-help pf-field-wide" id="remote-peer-status" role="status"></p><div class="pf-field-wide pf-inline-actions"><button class="pf-secondary-button" id="remote-refresh" type="button">Refresh machines</button></div><label class="pf-field"><span class="pf-label">Backend HTTP port</span><input class="pf-input" id="remote-port" type="number" min="1" max="65535" value="8080" required></label><label class="pf-field"><span class="pf-label">Name</span><input class="pf-input" id="remote-name" maxlength="128" placeholder="Defaults to public hostname"></label><label class="pf-field"><span class="pf-label">Public hostname</span><input class="pf-input" id="remote-host" placeholder="app.example.com" required></label><label class="pf-check-field pf-field-wide"><input id="remote-enabled" type="checkbox" checked><span>Enable public exposure<small class="pf-help">Requires policy approval and a successful backend test.</small></span></label><div class="pf-form-actions"><button type="button" class="pf-secondary-button" id="remote-policy">Check exposure policy</button><button type="button" class="pf-secondary-button" id="remote-test">Test backend connection</button><button class="pf-button" id="remote-save">Save exposure</button><button type="button" class="pf-secondary-button" id="remote-cancel" hidden>Cancel edit</button></div><p class="pf-field-wide" id="remote-result" role="status" aria-live="polite"></p></form></section><section class="pf-surface"><h2>Tailnet exposures</h2><p class="pf-help">Disable or delete to withdraw the public route. DNS caching and tailnet gateway polling can delay visible cleanup.</p><div id="remote-list"></div></section><script src="/assets/tailnet-exposures.js" defer></script>`
	render(w, "Tailnet exposures", b)
}
