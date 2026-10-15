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
	b.WriteString(pageIntro("Overview", "Exposures", "All eligible load balancers serve each exposure by default. Assignment is managed automatically by control."))
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
		path := "Load balancer → Service → gateway → container"
		backend := ""
		for _, app := range apps {
			if app.ID == e.ApplicationID {
				backend = fmt.Sprintf("%s:%d", app.ServiceFQDN, app.BackendPort)
				if app.BackendSource == "kubernetes" {
					path = "Load balancer → Service → Kubernetes gateway → ClusterIP Service"
				}
				if app.BackendSource == "tailnet" {
					path = "Load balancer → Service → Tailnet gateway → backend machine"
					backend = fmt.Sprintf("%s:%d", app.BackendDeviceID, app.BackendPort)
				}
			}
		}
		fmt.Fprintf(&b, `<section class="pf-surface" data-exposure-id="%s"><div class="pf-section-heading"><div><h2 class="font-mono">%s</h2><p class="pf-help">%s · %s</p></div></div>`, safeText(e.ID), safeText(e.Hostname), safeText(path), safeText(backend))
		_, states := c.exposureDesired(e, c.loadExposurePolicy())
		b.WriteString(`<p class="pf-help">Control automatically assigns all eligible load balancers.</p>`)
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
