package web

import (
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
