package store

import (
	"encoding/json"
	"fmt"
)

// ReplaceGateway moves application assignments only after the old identity has
// been revoked and its owned DNS cleaned. It never transfers another owner's DNS.
func (s *Store) ReplaceGateway(oldID, newID string) error {
	if oldID == newID {
		return fmt.Errorf("replacement must be a different node")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid int
	if err = tx.QueryRow(`SELECT count(*) FROM nodes old JOIN nodes replacement ON replacement.id=? WHERE old.id=? AND old.mode='docker-gateway' AND old.revoked=1 AND replacement.mode='docker-gateway' AND replacement.revoked=0 AND replacement.tailnet_id<>''`, newID, oldID).Scan(&valid); err != nil {
		return err
	}
	if valid != 1 {
		return fmt.Errorf("select a revoked gateway and an enrolled active replacement")
	}
	if err = tx.QueryRow(`SELECT count(*) FROM records WHERE owner_id=?`, "gateway:"+oldID).Scan(&valid); err != nil {
		return err
	}
	if valid != 0 {
		return fmt.Errorf("clean the old gateway owned DNS before replacement")
	}
	rows, err := tx.Query(`SELECT id,allowed_gateway_nodes FROM applications WHERE backend_source='docker'`)
	if err != nil {
		return err
	}
	updates := map[string]string{}
	for rows.Next() {
		var id, raw string
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var nodes []string
		if err = json.Unmarshal([]byte(raw), &nodes); err != nil {
			rows.Close()
			return err
		}
		changed := false
		seen := map[string]bool{}
		next := []string{}
		for _, n := range nodes {
			if n == oldID {
				n = newID
				changed = true
			}
			if !seen[n] {
				next = append(next, n)
				seen[n] = true
			}
		}
		if changed {
			b, _ := json.Marshal(next)
			updates[id] = string(b)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for id, raw := range updates {
		if _, err = tx.Exec(`UPDATE applications SET allowed_gateway_nodes=? WHERE id=?`, raw, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
