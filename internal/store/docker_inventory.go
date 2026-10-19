package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
	"strings"
	"time"
)

// DockerInventory is a complete declaration inventory, not a reachability report.
// Stopped containers are included. Only verified, parsed inventories reach storage.
type DockerDeclaration struct {
	Hostname   string               `json:"hostname"`
	Port       uint16               `json:"port"`
	PathPrefix string               `json:"path_prefix,omitempty"`
	Rewrite    *proxy.PrefixRewrite `json:"rewrite,omitempty"`
}
type DockerContainer struct {
	ID      string              `json:"id"`
	Running bool                `json:"running"`
	Routes  []DockerDeclaration `json:"routes"`
}
type DockerInventory struct {
	Revision   int64             `json:"revision"`
	Containers []DockerContainer `json:"containers"`
}
type DockerRetirement struct {
	NodeID, ApplicationID, Hostname, ServiceName string
	Port                                         uint16
}

func dockerPath(p string) string {
	if p == "" {
		return "/"
	}
	if p != "/" {
		return strings.TrimRight(p, "/")
	}
	return p
}
func declarationMatches(d DockerDeclaration, a ApplicationRecord) bool {
	if d.Port != a.BackendPort {
		return false
	}
	host := false
	for _, h := range a.PublicHostnames {
		host = host || h == d.Hostname
	}
	if !host {
		return false
	}
	path := "/"
	var rewrite *proxy.PrefixRewrite
	if len(a.HTTPRules) > 0 {
		if len(a.HTTPRules) != 1 || len(a.HTTPRules[0].Matches) != 1 {
			return false
		}
		path = dockerPath(a.HTTPRules[0].Matches[0].Path)
		rewrite = a.HTTPRules[0].Rewrite
	}
	x, _ := json.Marshal(rewrite)
	y, _ := json.Marshal(d.Rewrite)
	return path == dockerPath(d.PathPrefix) && string(x) == string(y)
}
func inventoryHas(i DockerInventory, a ApplicationRecord, container string, running bool) bool {
	for _, c := range i.Containers {
		if container != "" && container != c.ID {
			continue
		}
		if running && !c.Running {
			continue
		}
		for _, d := range c.Routes {
			if declarationMatches(d, a) {
				return true
			}
		}
	}
	return false
}
func (s *Store) DockerDeclarationAllowed(node string, a ApplicationRecord, container string, running bool) (bool, error) {
	var raw string
	err := s.db.QueryRow(`SELECT payload FROM docker_inventories WHERE node_id=?`, node).Scan(&raw)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var i DockerInventory
	if err = json.Unmarshal([]byte(raw), &i); err != nil {
		return false, err
	}
	return inventoryHas(i, a, container, running), nil
}
func (s *Store) RetiredDockerExposure(n, app, host, service string, port uint16) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM docker_retirements WHERE node_id=? AND application_id=? AND hostname=? AND service_name=? AND port=?`, n, app, host, service, port).Scan(&count)
	return count == 1, err
}

// AcceptDockerInventory atomically binds revision/nonce and retires declarations.
// DNS ledger and exposure rows are deliberately retained, never rewritten here.
func (s *Store) AcceptDockerInventory(node, key, nonce string, i DockerInventory) (int, error) {
	if i.Revision <= 0 {
		return 0, errors.New("positive inventory revision required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var valid int
	var actor string
	if err = tx.QueryRow(`SELECT count(*) FROM nodes WHERE id=? AND mode='docker-gateway' AND revoked=0 AND node_key=? AND node_key<>''`, node, key).Scan(&valid); err != nil {
		return 0, err
	}
	if valid != 1 {
		return 0, ErrInvalidNodeSnapshot
	}
	if err = tx.QueryRow(`SELECT tailnet_id FROM nodes WHERE id=?`, node).Scan(&actor); err != nil {
		return 0, err
	}
	var revision int64
	err = tx.QueryRow(`SELECT revision FROM docker_inventories WHERE node_id=?`, node).Scan(&revision)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	if revision >= i.Revision {
		return 0, errors.New("inventory revision already used or out of order")
	}
	if _, err = tx.Exec(`INSERT INTO report_nonces(node_id,nonce,received_at) VALUES(?,?,?)`, node, nonce, time.Now().Unix()); err != nil {
		return 0, err
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`INSERT INTO docker_inventories(node_id,revision,payload,received_at) VALUES(?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET revision=excluded.revision,payload=excluded.payload,received_at=excluded.received_at`, node, i.Revision, string(raw), time.Now().Unix()); err != nil {
		return 0, err
	}
	rows, err := tx.Query(`SELECT id,service_name,backend_port,public_hostnames,allowed_gateway_nodes,http_config FROM applications WHERE backend_source='docker'`)
	if err != nil {
		return 0, err
	}
	var apps []ApplicationRecord
	for rows.Next() {
		var a ApplicationRecord
		var hosts, nodes, config string
		if err = rows.Scan(&a.ID, &a.ServiceName, &a.BackendPort, &hosts, &nodes, &config); err != nil {
			rows.Close()
			return 0, err
		}
		var routing struct {
			Rules []proxy.HTTPRule `json:"rules"`
		}
		if err = json.Unmarshal([]byte(hosts), &a.PublicHostnames); err == nil {
			err = json.Unmarshal([]byte(nodes), &a.AllowedGatewayNodes)
		}
		if err == nil {
			err = json.Unmarshal([]byte(config), &routing)
		}
		if err != nil {
			rows.Close()
			return 0, err
		}
		a.HTTPRules = routing.Rules
		if len(a.AllowedGatewayNodes) == 1 && a.AllowedGatewayNodes[0] == node && len(a.PublicHostnames) > 0 {
			apps = append(apps, a)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	retired := 0
	for _, a := range apps {
		if inventoryHas(i, a, "", false) {
			continue
		}
		for _, host := range a.PublicHostnames {
			if _, err = tx.Exec(`INSERT INTO docker_retirements(node_id,application_id,hostname,service_name,port,retired_at) VALUES(?,?,?,?,?,?) ON CONFLICT(node_id,application_id,hostname) DO UPDATE SET service_name=excluded.service_name,port=excluded.port,retired_at=excluded.retired_at,cleanup_pending=1`, node, a.ID, host, a.ServiceName, a.BackendPort, time.Now().Unix()); err != nil {
				return 0, err
			}
		}
		if _, err = tx.Exec(`UPDATE applications SET public_hostnames='[]',enabled=0,updated_at=? WHERE id=?`, time.Now().Unix(), a.ID); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(`UPDATE backend_assignments SET revoked=1 WHERE node_id=? AND application_id=?`, node, a.ID); err != nil {
			return 0, err
		}
		a.PublicHostnames = nil
		if err = s.syncApplicationExposures(tx, a); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(`INSERT INTO audit(action,actor,created_at) VALUES(?,?,?)`, "docker.application.retire:"+a.ID, "node:"+actor, time.Now().Unix()); err != nil {
			return 0, err
		}
		retired++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return retired, nil
}
func validateDockerDeclaration(tx *transaction, a ApplicationRecord) error {
	if a.BackendSource != "docker" || len(a.AllowedGatewayNodes) != 1 || len(a.PublicHostnames) == 0 {
		return nil
	}
	var raw string
	err := tx.QueryRow(`SELECT payload FROM docker_inventories WHERE node_id=?`, a.AllowedGatewayNodes[0]).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var i DockerInventory
	if err = json.Unmarshal([]byte(raw), &i); err != nil {
		return err
	}
	if !inventoryHas(i, a, "", false) {
		return fmt.Errorf("Docker declaration absent from the latest complete inventory")
	}
	return nil
}
func (s *Store) PendingDockerRetirements() ([]DockerRetirement, error) {
	rows, err := s.db.Query(`SELECT node_id,application_id,hostname,service_name,port FROM docker_retirements WHERE cleanup_pending=1 ORDER BY retired_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DockerRetirement{}
	for rows.Next() {
		var r DockerRetirement
		if err = rows.Scan(&r.NodeID, &r.ApplicationID, &r.Hostname, &r.ServiceName, &r.Port); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) CompleteDockerRetirement(service string) error {
	_, err := s.db.Exec(`UPDATE docker_retirements SET cleanup_pending=0 WHERE service_name=?`, service)
	return err
}
