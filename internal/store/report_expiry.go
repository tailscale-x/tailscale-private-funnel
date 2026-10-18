package store

import (
	"database/sql"
	"time"
)

// ExpireReportBefore rechecks freshness in the same transaction as removal.
// A report renewed after the sweeper's candidate scan must survive.
func (s *Store) ExpireReportBefore(id string, before int64) (bool, error) {
	return s.expireRuntimeBefore(id, before, false)
}
func (s *Store) ExpireNodeBefore(id string, before int64) (bool, error) {
	return s.expireRuntimeBefore(id, before, true)
}
func (s *Store) expireRuntimeBefore(id string, before int64, heartbeat bool) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	query := `SELECT received_at FROM reports WHERE node_id=?`
	if heartbeat {
		query = `SELECT last_report FROM nodes WHERE id=? AND revoked=0`
	}
	var seen int64
	if err = tx.QueryRow(query, id).Scan(&seen); err == sql.ErrNoRows {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if seen == 0 || seen >= before {
		return false, nil
	}
	if _, err = tx.Exec(`UPDATE backend_assignments SET revoked=1,last_seen=? WHERE node_id=?`, time.Now().Unix(), id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`DELETE FROM reports WHERE node_id=?`, id); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
