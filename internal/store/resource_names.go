package store

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

func NewResourceID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 {
		return "", fmt.Errorf("Name must contain 1–128 characters")
	}
	return name, nil
}

// Resource IDs and all reference rewrites commit together. Secrets and ownership
// values are never decoded or rewritten by this migration.
func (s *Store) migrateResourceNames() error {
	const version = "009_resource_names"
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
	for _, q := range []string{`ALTER TABLE providers ADD COLUMN name TEXT NOT NULL DEFAULT ''`, `ALTER TABLE nodes ADD COLUMN name TEXT NOT NULL DEFAULT ''`, `ALTER TABLE enrollments ADD COLUMN name TEXT NOT NULL DEFAULT ''`, `ALTER TABLE applications ADD COLUMN name TEXT NOT NULL DEFAULT ''`, `ALTER TABLE managed_zones ADD COLUMN id TEXT NOT NULL DEFAULT ''`, `ALTER TABLE reports ADD COLUMN snapshot TEXT NOT NULL DEFAULT ''`, `ALTER TABLE records ADD COLUMN zone_id TEXT NOT NULL DEFAULT ''`} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	providerIDs := map[string]string{}
	rows, err := tx.Query(`SELECT id FROM providers`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var old string
		if err = rows.Scan(&old); err != nil {
			rows.Close()
			return err
		}
		id, e := NewResourceID()
		if e != nil {
			rows.Close()
			return e
		}
		providerIDs[old] = id
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for old, id := range providerIDs {
		for _, q := range []string{`UPDATE providers SET id=?,name=? WHERE id=?`, `UPDATE managed_zones SET provider_id=? WHERE provider_id=?`, `UPDATE records SET provider_id=? WHERE provider_id=?`} {
			var e error
			if strings.Contains(q, "name=") {
				_, e = tx.Exec(q, id, old, old)
			} else {
				_, e = tx.Exec(q, id, old)
			}
			if e != nil {
				return e
			}
		}
	}
	if _, err = tx.Exec(`UPDATE nodes SET name=hostname; UPDATE enrollments SET name=hostname; UPDATE applications SET name=COALESCE(json_extract(public_hostnames,'$[0]'),service_name)`); err != nil {
		return err
	}
	rows, err = tx.Query(`SELECT provider_id,zone FROM managed_zones`)
	if err != nil {
		return err
	}
	var zones []ManagedZone
	for rows.Next() {
		var z ManagedZone
		if err = rows.Scan(&z.ProviderID, &z.Zone); err != nil {
			rows.Close()
			return err
		}
		z.ID, err = NewResourceID()
		if err != nil {
			rows.Close()
			return err
		}
		zones = append(zones, z)
	}
	rows.Close()
	for _, z := range zones {
		if _, err = tx.Exec(`UPDATE managed_zones SET id=? WHERE provider_id=? AND zone=?`, z.ID, z.ProviderID, z.Zone); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`UPDATE records SET zone_id=COALESCE((SELECT id FROM managed_zones z WHERE z.provider_id=records.provider_id AND z.zone=records.zone),'')`); err != nil {
		return err
	}
	if _, err = tx.Exec(`CREATE UNIQUE INDEX managed_zones_id ON managed_zones(id)`); err != nil {
		return err
	}
	// Resolve old edge hostname references once, never during normal operation.
	lbIDs := map[string]string{}
	rows, err = tx.Query(`SELECT n.id,s.value FROM nodes n JOIN settings s ON s.name='node_deployment:'||n.id WHERE n.mode='loadbalancer'`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, value string
		if err = rows.Scan(&id, &value); err != nil {
			rows.Close()
			return err
		}
		var d map[string]any
		if err = json.Unmarshal([]byte(value), &d); err != nil {
			rows.Close()
			return err
		}
		host, _ := d["loadbalancer_hostname"].(string)
		if host != "" {
			if old, ok := lbIDs[host]; ok && old != id {
				rows.Close()
				return fmt.Errorf("ambiguous load balancer reference %s", host)
			}
			lbIDs[host] = id
		}
	}
	rows.Close()
	type item struct{ key, value string }
	var settings []item
	rows, err = tx.Query(`SELECT name,value FROM settings WHERE secret=0`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.key, &v.value); err != nil {
			rows.Close()
			return err
		}
		settings = append(settings, v)
	}
	rows.Close()
	var rewrite func(any, string) error
	rewrite = func(value any, key string) error {
		switch v := value.(type) {
		case map[string]any:
			for k, x := range v {
				if k == "provider_id" {
					if old, ok := x.(string); ok {
						if id, exists := providerIDs[old]; exists {
							v[k] = id
						}
					}
				}
				if k == "loadbalancer_hostnames" {
					a, ok := x.([]any)
					if !ok {
						return fmt.Errorf("invalid load balancer selectors")
					}
					ids := []any{}
					for _, h := range a {
						host, _ := h.(string)
						id, ok := lbIDs[host]
						if !ok {
							return fmt.Errorf("unresolved load balancer %s", host)
						}
						ids = append(ids, id)
					}
					delete(v, k)
					v["loadbalancer_ids"] = ids
					continue
				}
				if k == "loadbalancer_hostname" && !strings.HasPrefix(key, "node_deployment:") {
					if host, ok := x.(string); ok && host != "" {
						if id, exists := lbIDs[host]; exists {
							v["loadbalancer_id"] = id
							delete(v, k)
						}
					}
				}
				if err := rewrite(x, key); err != nil {
					return err
				}
			}
		case []any:
			for _, x := range v {
				if err := rewrite(x, key); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, v := range settings {
		var data any
		if json.Unmarshal([]byte(v.value), &data) != nil {
			continue
		}
		if err = rewrite(data, v.key); err != nil {
			return err
		}
		if strings.HasPrefix(v.key, "node_deployment:") {
			var mode string
			_ = tx.QueryRow(`SELECT mode FROM nodes WHERE id=?`, strings.TrimPrefix(v.key, "node_deployment:")).Scan(&mode)
			if mode == "" {
				_ = tx.QueryRow(`SELECT mode FROM enrollments WHERE id=?`, strings.TrimPrefix(v.key, "node_deployment:")).Scan(&mode)
			}
			if mode == "ingress" {
				m := data.(map[string]any)
				host, _ := m["loadbalancer_hostname"].(string)
				if host != "" {
					id, ok := lbIDs[host]
					if !ok {
						return fmt.Errorf("unresolved ingress edge %s", host)
					}
					m["loadbalancer_id"] = id
					delete(m, "loadbalancer_hostname")
				}
			}
		}
		b, e := json.Marshal(data)
		if e != nil {
			return e
		}
		newKey := v.key
		for old, id := range providerIDs {
			if v.key == "provider_test:"+old {
				newKey = "provider_test:" + id
			}
			for _, prefix := range []string{"zone_test:", "zone_sync:"} {
				if strings.HasPrefix(v.key, prefix+old+":") {
					newKey = prefix + id + strings.TrimPrefix(v.key, prefix+old)
				}
			}
		}
		if _, err = tx.Exec(`UPDATE settings SET value=?,name=? WHERE name=?`, string(b), newKey, v.key); err != nil {
			return err
		}
	}
	rows, err = tx.Query(`SELECT node_id,payload FROM reports`)
	if err != nil {
		return err
	}
	var reports []item
	for rows.Next() {
		var v item
		if err = rows.Scan(&v.key, &v.value); err != nil {
			rows.Close()
			return err
		}
		reports = append(reports, v)
	}
	rows.Close()
	for _, v := range reports {
		var data any
		if err = json.Unmarshal([]byte(v.value), &data); err != nil {
			return err
		}
		if err = rewrite(data, "report"); err != nil {
			return err
		}
		b, e := json.Marshal(data)
		if e != nil {
			return e
		}
		if _, err = tx.Exec(`UPDATE reports SET snapshot=? WHERE node_id=?`, string(b), v.key); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,unixepoch())`, version); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RenameResource(kind, id, name string) error {
	name, err := ValidateName(name)
	if err != nil {
		return err
	}
	tables := map[string]string{"provider": "providers", "node": "nodes", "enrollment": "enrollments", "application": "applications"}
	table, ok := tables[kind]
	if !ok {
		return fmt.Errorf("unknown resource")
	}
	result, err := s.db.Exec(`UPDATE `+table+` SET name=? WHERE id=?`, name, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("resource not found")
	}
	return nil
}
func (s *Store) ZoneByID(id string) (ManagedZone, error) {
	var z ManagedZone
	err := s.db.QueryRow(`SELECT id,provider_id,zone FROM managed_zones WHERE id=?`, id).Scan(&z.ID, &z.ProviderID, &z.Zone)
	return z, err
}

// Run after resource names have been introduced, including on fresh databases.
func (s *Store) migrateIngressNames() error {
	const version = "012_ingress_hostname_names"
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var done int
	if err = tx.QueryRow(`SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&done); err != nil {
		return err
	}
	if done != 0 {
		return nil
	}
	for _, q := range []string{`UPDATE nodes SET name=hostname WHERE mode='ingress'`, `UPDATE enrollments SET name=hostname WHERE mode='ingress'`} {
		if _, err = tx.Exec(q); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,strftime('%s','now'))`, version); err != nil {
		return err
	}
	return tx.Commit()
}
