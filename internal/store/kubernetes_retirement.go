package store

import (
	"encoding/json"
	"errors"
	"time"
)

// RetireKubernetesApplications releases hostname claims only after explicit
// revocation and successful exact-ledger cleanup. Keep the rows and history;
// an offline gateway, a disabled application, or an empty report is not proof
// that a different gateway may take its names.
func (s *Store) RetireKubernetesApplications(nodeID string, apps []ApplicationRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var mode string
	var revoked int
	if err = tx.QueryRow(`SELECT mode,revoked FROM nodes WHERE id=?`, nodeID).Scan(&mode, &revoked); err != nil {
		return err
	}
	if mode != "k8s-gateway" || revoked != 1 {
		return errors.New("explicitly revoked Kubernetes gateway required")
	}
	now := time.Now().Unix()
	for _, app := range apps {
		if app.BackendSource != "kubernetes" || len(app.AllowedGatewayNodes) != 1 || app.AllowedGatewayNodes[0] != nodeID {
			return errors.New("application is not exclusively owned by this gateway")
		}
		var source, hosts, nodes string
		var enabled, owned int
		if err = tx.QueryRow(`SELECT backend_source,public_hostnames,allowed_gateway_nodes,enabled FROM applications WHERE id=?`, app.ID).Scan(&source, &hosts, &nodes, &enabled); err != nil {
			return err
		}
		expectedHosts, _ := json.Marshal(app.PublicHostnames)
		expectedNodes, _ := json.Marshal(app.AllowedGatewayNodes)
		if source != "kubernetes" || enabled != 0 || nodes != string(expectedNodes) || hosts != string(expectedHosts) {
			return errors.New("application changed during cleanup")
		}
		if err = tx.QueryRow(`SELECT count(*) FROM records r JOIN exposures e ON r.owner_id='exposure:'||e.id WHERE e.application_id=?`, app.ID).Scan(&owned); err != nil {
			return err
		}
		if owned != 0 {
			return errors.New("owned DNS cleanup remains pending")
		}
		if len(app.PublicHostnames) == 0 {
			continue
		}
		history, _ := json.Marshal(map[string]any{"node_id": nodeID, "application": app, "retired_at": now})
		if _, err = tx.Exec(`INSERT INTO settings(name,value,secret) VALUES(?,?,0) ON CONFLICT(name) DO NOTHING`, "kubernetes_retirement:"+app.ID, string(history)); err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE applications SET public_hostnames='[]',updated_at=? WHERE id=?`, now, app.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO audit(action,actor,created_at) VALUES(?,?,?)`, "kubernetes.application.retire:"+app.ID, "cleanup:"+nodeID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
