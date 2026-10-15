package store

import (
	"fmt"

	"github.com/libdns/libdns"
	"github.com/tailscale-x/tailscale-private-funnel/internal/dns"
)

// LedgerPartition identifies an independent ownership boundary.
type LedgerPartition struct {
	ZoneID     string `json:"zone_id"`
	ProviderID string `json:"provider_id"`
	Zone       string `json:"zone"`
	OwnerID    string `json:"owner_id"`
}

func (s *Store) LedgerPartitions() ([]LedgerPartition, error) {
	rows, err := s.db.Query(`SELECT DISTINCT zone_id,provider_id,zone,owner_id FROM records ORDER BY provider_id,zone,owner_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LedgerPartition{}
	for rows.Next() {
		var p LedgerPartition
		if err := rows.Scan(&p.ZoneID, &p.ProviderID, &p.Zone, &p.OwnerID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) DeleteZone(provider, zone string) error {
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM records WHERE provider_id=? AND zone=?`, provider, zone).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("clean up owned records before removing this zone")
	}
	_, err := s.db.Exec(`DELETE FROM managed_zones WHERE provider_id=? AND zone=?`, provider, zone)
	return err
}

func (s *Store) PartitionZoneLedger(provider, zone string) ([]dns.LedgerEntry, error) {
	rows, err := s.db.Query(`SELECT name,type,value,CASE WHEN provider_id=? THEN owner_id ELSE 'foreign-provider:'||provider_id||':'||owner_id END FROM records WHERE zone=?`, provider, zone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dns.LedgerEntry{}
	for rows.Next() {
		var name, typ, value, owner string
		if err := rows.Scan(&name, &typ, &value, &owner); err != nil {
			return nil, err
		}
		out = append(out, dns.LedgerEntry{Record: libdns.RR{Name: name, Type: typ, Data: value}, OwnerID: owner})
	}
	return out, rows.Err()
}
