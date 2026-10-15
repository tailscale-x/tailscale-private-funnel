package store

import (
	"fmt"
	"regexp"
	"time"
)

// Run after name/exposure migrations: applications retain their own IDs and
// hostnames while several applications may reference one shared backend Service.
func (s *Store) migrateSharedApplicationServices() error {
	const version = "015_shared_application_services"
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
	var schema string
	if err = tx.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='applications'`).Scan(&schema); err != nil {
		return err
	}
	constraint := regexp.MustCompile(`(?i)(service_name\s+TEXT\s+NOT\s+NULL)\s+UNIQUE`)
	if constraint.MatchString(schema) {
		updated := constraint.ReplaceAllString(schema, `${1}`)
		table := regexp.MustCompile(`(?i)^CREATE TABLE\s+(?:"applications"|applications)`)
		if !table.MatchString(updated) {
			return fmt.Errorf("unrecognized applications schema; migration aborted")
		}
		updated = table.ReplaceAllString(updated, "CREATE TABLE applications_shared")
		for _, q := range []string{updated, `INSERT INTO applications_shared SELECT * FROM applications`, `DROP TABLE applications`, `ALTER TABLE applications_shared RENAME TO applications`, `CREATE INDEX applications_host_idx ON applications(service_name)`} {
			if _, err = tx.Exec(q); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}
