package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tailscale-x/tailscale-private-funnel/internal/proxy"
)

type ExposureRecord struct {
	ID                string `json:"id"`
	ApplicationID     string `json:"application_id"`
	Hostname          string `json:"hostname"`
	Active            bool   `json:"active"`
	CloudflareProxied bool   `json:"cloudflare_proxied"`
	DNSRevision       uint64 `json:"dns_revision"`
}

// migrateExposures transfers only known application hostnames from node-owned
// ledger entries. Provider records are not touched by this transaction.
func (s *Store) migrateExposures() error {
	const version = "013_exposure_mapping"
	var done int
	if err := s.db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`CREATE TABLE exposures(id TEXT PRIMARY KEY,application_id TEXT NOT NULL,hostname TEXT NOT NULL UNIQUE,loadbalancer_ids TEXT,active INTEGER NOT NULL DEFAULT 1)`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id,public_hostnames FROM applications`)
	if err != nil {
		return err
	}
	type assignment struct{ id, hosts string }
	var apps []assignment
	for rows.Next() {
		var a assignment
		if err = rows.Scan(&a.id, &a.hosts); err != nil {
			rows.Close()
			return err
		}
		apps = append(apps, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range apps {
		var hosts []string
		if err = json.Unmarshal([]byte(a.hosts), &hosts); err != nil {
			return err
		}
		for _, host := range hosts {
			host = strings.ToLower(strings.TrimSuffix(host, "."))
			id, e := NewResourceID()
			if e != nil {
				return e
			}
			if _, err = tx.Exec(`INSERT INTO exposures(id,application_id,hostname) VALUES(?,?,?)`, id, a.id, host); err != nil {
				return err
			}
			// Transfer exact host records only, leaving each edge's own A record alone.
			if _, err = tx.Exec(`UPDATE records SET owner_id=? WHERE name=? AND type='CNAME' AND (owner_id LIKE 'gateway:%' OR owner_id LIKE 'loadbalancer:%')`, "exposure:"+id, host); err != nil {
				return err
			}
		}
	}
	// Remove obsolete assignment settings, including saved enrollment deployment.
	if _, err = tx.Exec(`UPDATE settings SET value=json_remove(value,'$.loadbalancer_id') WHERE name LIKE 'node_deployment:%' OR name LIKE 'enrollment_deployment:%'`); err != nil {
		return err
	}
	if _, err = tx.Exec(`ALTER TABLE applications DROP COLUMN loadbalancer_id`); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) syncApplicationExposures(tx *transaction, a ApplicationRecord) error {
	var err error
	for _, host := range a.PublicHostnames {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if err = validateSharedDockerHost(tx, a, host); err != nil {
			return err
		}
		var id, app string
		err = tx.QueryRow(`SELECT id,application_id FROM exposures WHERE hostname=?`, host).Scan(&id, &app)
		if err == sql.ErrNoRows {
			id, err = NewResourceID()
			if err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO exposures(id,application_id,hostname) VALUES(?,?,?)`, id, a.ID, host)
		}
		if err != nil {
			return err
		}
		// Retain the hostname identity/ledger owner while changing the primary
		// application after its previous membership has been retired.
		if app != "" && app != a.ID {
			var raw string
			e := tx.QueryRow(`SELECT public_hostnames FROM applications WHERE id=?`, app).Scan(&raw)
			if e != nil && e != sql.ErrNoRows {
				return e
			}
			var prior []string
			if e == nil {
				if e = json.Unmarshal([]byte(raw), &prior); e != nil {
					return e
				}
			}
			member := false
			for _, h := range prior {
				member = member || h == host
			}
			if !member {
				if _, err = tx.Exec(`UPDATE exposures SET application_id=? WHERE id=?`, a.ID, id); err != nil {
					return err
				}
			}
		}
	}
	// Application hostnames are the membership relation. Keep the stable
	// hostname/owner row active while any member still assigns it. Backend health and enabled state
	// are evaluated separately by control, preserving disabled inventory rows.
	rows, e := tx.Query(`SELECT public_hostnames FROM applications`)
	if e != nil {
		return e
	}
	active := map[string]bool{}
	for rows.Next() {
		var raw string
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return e
		}
		var hosts []string
		if e = json.Unmarshal([]byte(raw), &hosts); e != nil {
			rows.Close()
			return e
		}
		for _, host := range hosts {
			active[strings.ToLower(strings.TrimSuffix(host, "."))] = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = tx.Query(`SELECT id,hostname,active FROM exposures`)
	if e != nil {
		return e
	}
	type item struct {
		id, host string
		active   int
	}
	var items []item
	for rows.Next() {
		var i item
		if e = rows.Scan(&i.id, &i.host, &i.active); e != nil {
			rows.Close()
			return e
		}
		items = append(items, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, i := range items {
		if i.active == boolInt(active[i.host]) {
			continue
		}
		if _, e = tx.Exec(`UPDATE exposures SET active=? WHERE id=?`, boolInt(active[i.host]), i.id); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) Exposures() ([]ExposureRecord, error) {
	if s.reads != nil {
		return readCached(s, "exposures", func(b *Store) ([]ExposureRecord, error) { return b.Exposures() })
	}
	rows, err := s.db.Query(`SELECT e.id,e.application_id,e.hostname,e.active,COALESCE(d.cloudflare_proxied,0),COALESCE(d.revision,0) FROM exposures e LEFT JOIN hostname_dns d ON d.hostname=e.hostname ORDER BY e.hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExposureRecord{}
	for rows.Next() {
		var e ExposureRecord
		if err = rows.Scan(&e.ID, &e.ApplicationID, &e.Hostname, &e.Active, &e.CloudflareProxied, &e.DNSRevision); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Exposure(id string) (ExposureRecord, error) {
	es, err := s.Exposures()
	if err != nil {
		return ExposureRecord{}, err
	}
	for _, e := range es {
		if e.ID == id {
			return e, nil
		}
	}
	return ExposureRecord{}, sql.ErrNoRows
}

func validateSharedDockerHost(tx *transaction, a ApplicationRecord, host string) error {
	rows, err := tx.Query(`SELECT id,backend_source,public_hostnames,allowed_gateway_nodes,http_config FROM applications WHERE id<>?`, a.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	prefix := func(rules []proxy.HTTPRule) string {
		if len(rules) == 0 {
			return "/"
		}
		if len(rules[0].Matches) == 0 {
			return "/"
		}
		p := strings.TrimRight(rules[0].Matches[0].Path, "/")
		if p == "" {
			return "/"
		}
		return p
	}
	for rows.Next() {
		var id, source, hostsRaw, nodesRaw, configRaw string
		if err = rows.Scan(&id, &source, &hostsRaw, &nodesRaw, &configRaw); err != nil {
			return err
		}
		var hosts, nodes []string
		var config struct {
			Rules []proxy.HTTPRule `json:"rules"`
		}
		if err = json.Unmarshal([]byte(hostsRaw), &hosts); err != nil {
			return err
		}
		matched := false
		for _, h := range hosts {
			if h == host {
				matched = true
			}
		}
		if !matched {
			continue
		}
		if err = json.Unmarshal([]byte(nodesRaw), &nodes); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(configRaw), &config); err != nil {
			return err
		}
		sameGateway := len(nodes) == 1 && len(a.AllowedGatewayNodes) == 1 && nodes[0] == a.AllowedGatewayNodes[0]
		if a.BackendSource != "docker" || source != "docker" || !sameGateway || prefix(config.Rules) == prefix(a.HTTPRules) {
			return fmt.Errorf("hostname/path already assigned to another application")
		}
	}
	return rows.Err()
}
