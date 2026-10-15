package web

import (
	"fmt"
	"net/http"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
)

func managedRecordViews(records []libdns.Record, ledger []dns.LedgerEntry, owner string) []map[string]any {
	views := recordViews(records)
	for i, record := range records {
		status := "Unmanaged"
		for _, entry := range ledger {
			if dns.RecordKey(entry.Record) == dns.RecordKey(record) {
				status = "Managed by another node"
				if entry.OwnerID == owner {
					status = "Managed by Local Funnel"
				}
				break
			}
		}
		views[i]["management"] = status
	}
	return views
}
func (c *Control) operations(w http.ResponseWriter, r *http.Request) {
	events, err := c.Store.AuditEvents(100)
	if err != nil {
		http.Error(w, "Unable to load audit history", 500)
		return
	}
	version := c.BuildVersion
	if version == "" {
		version = "Development build"
	}
	b := pageIntro("Operations", "Operations", "Review control activity, runtime information, and recovery guidance. DNS status and recovery are available within each managed zone.")
	b += `<div class="pf-action-row"><a class="pf-secondary-button" href="/zones">Open zone DNS status</a></div><section class="pf-surface"><h2>Runtime and recovery</h2><div class="pf-status-row"><span>Version</span><strong>` + safeText(version) + `</strong></div><p class="pf-help">Runtime logs are available through journalctl for native services or docker logs for the control container. Use the same state and data paths when replacing the executable or image.</p><details><summary class="pf-summary">Update and rollback</summary><p class="pf-help">Record the running version and stop the selected service. Install a verified release binary or pinned image, then restart with the existing state and data. For rollback, restore the previous artifact and a compatible database backup; do not reset the Tailscale identity.</p><a class="pf-text-link" href="https://github.com/tailscale-x/tailscale-private-funnel/blob/main/PRIVATE_FUNNEL.md">Deployment documentation</a></details></section><section class="pf-surface" id="audit"><div class="pf-section-heading"><h2>Audit history</h2><span class="pf-help">Most recent 100 events</span></div>`
	if len(events) == 0 {
		b += `<div class="pf-empty"><strong>No audit events yet</strong><span>Settings, enrollment, policy, and DNS activity appear here.</span></div>`
	} else {
		b += `<div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Activity</th><th>Actor</th><th>Time</th></tr></thead><tbody>`
		for _, event := range events {
			b += fmt.Sprintf(`<tr><td data-label="Activity">%s</td><td data-label="Actor">%s</td><td data-label="Time">%s</td></tr>`, safeText(event.Action), safeText(event.Actor), safeText(unixLabel(event.CreatedAt)))
		}
		b += `</tbody></table></div>`
	}
	b += `</section>`
	render(w, "Operations", b)
}
