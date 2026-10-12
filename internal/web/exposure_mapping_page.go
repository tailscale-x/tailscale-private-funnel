package web

import (
	"fmt"
	"net/http"
	"strings"
)

func (c *Control) exposureMappingPage(w http.ResponseWriter, r *http.Request) {
	es, err := c.Store.Exposures()
	if err != nil {
		http.Error(w, "Unable to read exposures", 503)
		return
	}
	apps, _ := c.Store.Applications()
	b := strings.Builder{}
	b.WriteString(pageIntro("Overview", "Exposures", "All eligible load balancers serve each exposure by default. Override individual hostnames when an edge should serve only selected traffic."))
	if requestAccess(r).Admin {
		b.WriteString(`<div class="pf-action-row"><a class="pf-button" href="/tailnet-exposures">Expose a tailnet machine</a><a class="pf-secondary-button" href="/applications">Applications and aliases</a></div>`)
	}
	b.WriteString(`<div class="pf-alert pf-alert-info">Public DNS lists the eligible load balancers’ IPv4 addresses. Policy, backend connectivity and current health must all permit an edge. DNS caching can delay changes; multiple addresses do not guarantee client failover.</div>`)
	var sync struct {
		Pending bool     `json:"pending"`
		Errors  []string `json:"errors"`
	}
	_ = c.Store.GetJSON("exposure_mapping_sync", &sync)
	if sync.Pending {
		fmt.Fprintf(&b, `<div class="pf-alert pf-alert-warn" role="status">DNS cleanup pending: %s</div>`, safeText(strings.Join(sync.Errors, "; ")))
	}
	count := 0
	for _, e := range es {
		if !e.Active {
			continue
		}
		count++
		path := "Load balancer → Service → ingress → container"
		backend := ""
		for _, app := range apps {
			if app.ID == e.ApplicationID {
				backend = fmt.Sprintf("%s:%d", app.ServiceFQDN, app.BackendPort)
				if app.BackendSource == "tailnet" {
					path = "Load balancer → backend machine"
					backend = fmt.Sprintf("%s:%d", app.BackendDeviceID, app.BackendPort)
				}
			}
		}
		fmt.Fprintf(&b, `<section class="pf-surface" data-exposure-id="%s"><div class="pf-section-heading"><div><h2 class="font-mono">%s</h2><p class="pf-help">%s · %s</p></div></div>`, safeText(e.ID), safeText(e.Hostname), safeText(path), safeText(backend))
		_, states := c.exposureDesired(e, c.loadExposurePolicy())
		if requestAccess(r).Admin {
			fmt.Fprintf(&b, `<form class="pf-form-grid" data-edge-mapping="%s"><label class="pf-field"><span class="pf-label">Load balancers</span><select class="pf-input" name="selection"><option value="all"%s>All load balancers</option><option value="selected"%s>Selected load balancers</option></select></label><fieldset class="pf-field-wide" data-edge-choices%s><legend class="pf-label">Selected load balancers</legend>`, safeText(e.ID), selectedAttr(e.LoadBalancerIDs == nil), selectedAttr(e.LoadBalancerIDs != nil), hiddenAttr(e.LoadBalancerIDs == nil))
			for _, edge := range c.loadBalancers() {
				fmt.Fprintf(&b, `<label class="pf-check-field"><input type="checkbox" name="loadbalancer_ids" value="%s"%s><span>%s <small class="pf-help">%s</small></span></label>`, safeText(edge.ID), checkedAttr(containsExact(e.LoadBalancerIDs, edge.ID)), safeText(edge.Name), safeText(edge.Hostname))
			}
			b.WriteString(`</fieldset><div class="pf-form-actions"><button class="pf-button" type="submit">Save load balancers</button><p data-edge-result role="status" aria-live="polite"></p></div></form>`)
		} else {
			mode := "All load balancers"
			if e.LoadBalancerIDs != nil {
				mode = "Selected load balancers"
			}
			fmt.Fprintf(&b, `<p>%s</p>`, mode)
		}
		b.WriteString(`<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Load balancer</th><th>Public IPv4</th><th>Status</th></tr></thead><tbody data-edge-states>`)
		for _, state := range states {
			fmt.Fprintf(&b, `<tr><td data-label="Load balancer">%s</td><td data-label="Public IPv4" class="font-mono">%s</td><td data-label="Status">%s</td></tr>`, safeText(fmt.Sprint(state["name"])), safeText(fmt.Sprint(state["public_ipv4"])), safeText(fmt.Sprint(state["reason"])))
		}
		b.WriteString(`</tbody></table></div>`)
		if z, err := c.managedZone(e.Hostname); err == nil {
			fmt.Fprintf(&b, `<a class="pf-text-link" href="/zones?zone_id=%s">DNS status and recovery</a>`, safeText(z.ID))
		}
		b.WriteString(`</section>`)
	}
	if count == 0 {
		b.WriteString(`<section class="pf-surface pf-empty"><strong>No exposures yet</strong><p>Add a policy-approved Docker label or expose a tailnet machine.</p></section>`)
	}
	b.WriteString(`<script src="/assets/exposure-mapping.js" defer></script>`)
	render(w, "Exposures", b.String())
}
func checkedAttr(v bool) string {
	if v {
		return " checked"
	}
	return ""
}
func hiddenAttr(v bool) string {
	if v {
		return " hidden"
	}
	return ""
}
