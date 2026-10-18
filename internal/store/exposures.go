package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ExposureRecord struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	Hostname      string `json:"hostname"`
	Active        bool   `json:"active"`
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
	if _, err = tx.Exec(`UPDATE exposures SET active=0 WHERE application_id=?`, a.ID); err != nil {
		return err
	}
	for _, host := range a.PublicHostnames {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		var id, app string
		err = tx.QueryRow(`SELECT id,application_id FROM exposures WHERE hostname=?`, host).Scan(&id, &app)
		if err == sql.ErrNoRows {
			id, err = NewResourceID()
			if err != nil {
				return err
			}
			_, err = tx.Exec(`INSERT INTO exposures(id,application_id,hostname) VALUES(?,?,?)`, id, a.ID, host)
		} else if err == nil && app != a.ID {
			return fmt.Errorf("hostname already assigned to another application")
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`UPDATE exposures SET active=1 WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) Exposures() ([]ExposureRecord, error) {
	rows, err := s.db.Query(`SELECT id,application_id,hostname,active FROM exposures ORDER BY hostname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExposureRecord{}
	for rows.Next() {
		var e ExposureRecord
		if err = rows.Scan(&e.ID, &e.ApplicationID, &e.Hostname, &e.Active); err != nil {
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
