package store

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/tunnel"
)

// RetireLocalTunnel releases only this foreground component's membership. The
// hostname row and DNS ledger survive until normal ownership-scoped cleanup.
// before=0 is explicit withdrawal; otherwise recheck freshness transactionally.
func (s *Store) RetireLocalTunnel(id string, before int64) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var raw, mode string
	var last int64
	if err = tx.QueryRow(`SELECT n.mode,n.last_report,s.value FROM nodes n JOIN settings s ON s.name=? WHERE n.id=?`, "component_registration:"+id, id).Scan(&mode, &last, &raw); err != nil {
		return false, err
	}
	var reg Registration
	if err = json.Unmarshal([]byte(raw), &reg); err != nil {
		return false, err
	}
	if mode != "tailnet-gateway" || reg.Runtime != tunnel.Runtime || (reg.Connection != "host" && reg.Connection != "embedded") {
		return false, errors.New("component is not a foreground local tunnel")
	}
	if before > 0 && last >= before {
		return false, nil
	}
	if before > 0 {
		var lease int64
		if err = tx.QueryRow(`SELECT lease_until FROM component_instances WHERE component_id=?`, id).Scan(&lease); err != nil {
			return false, err
		}
		if lease > time.Now().Unix() {
			return false, nil
		}
	}
	rows, err := tx.Query(`SELECT id,allowed_gateway_nodes FROM applications WHERE backend_source='local'`)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var app, nodes string
		if err = rows.Scan(&app, &nodes); err != nil {
			rows.Close()
			return false, err
		}
		var allowed []string
		if err = json.Unmarshal([]byte(nodes), &allowed); err != nil {
			rows.Close()
			return false, err
		}
		if len(allowed) == 1 && allowed[0] == id {
			ids = append(ids, app)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, app := range ids {
		if _, err = tx.Exec(`UPDATE applications SET public_hostnames='[]',enabled=0,updated_at=? WHERE id=?`, time.Now().Unix(), app); err != nil {
			return false, err
		}
	}
	if err = s.syncApplicationExposures(tx, ApplicationRecord{}); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`DELETE FROM reports WHERE node_id=?`, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE backend_assignments SET revoked=1 WHERE node_id=?`, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE component_instances SET lease_until=0 WHERE component_id=?`, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`UPDATE nodes SET last_report=0 WHERE id=?`, id); err != nil {
		return false, err
	}
	if _, err = tx.Exec(`INSERT INTO audit(action,actor,created_at) VALUES(?,?,?)`, "tunnel.retire:"+id, "control", time.Now().Unix()); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
