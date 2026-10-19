package store

import (
	"fmt"
	"strings"
)

// RefreshNodeHostname uses only the verified WhoIs name of the bound device.
// Operational IDs and DNS ownership stay stable when a device is renamed.
func (s *Store) RefreshNodeHostname(id, tailnetID, hostname string) error {
	hostname = strings.SplitN(strings.TrimSuffix(strings.TrimSpace(hostname), "."), ".", 2)[0]
	if hostname == "" {
		return fmt.Errorf("verified hostname is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE nodes SET name=CASE WHEN mode='docker-gateway' OR name=hostname THEN ? ELSE name END,hostname=? WHERE id=? AND tailnet_id=? AND revoked=0`, hostname, hostname, id, tailnetID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("unknown or revoked device identity")
	}

	return tx.Commit()
}
