package store

import "github.com/tailscale-x/tailscale-private-funnel/internal/secretbox"

func (s *Store) SetSecrets(values map[string]string) error {
	sealed := map[string]string{}
	for key, value := range values {
		encrypted, err := secretbox.Seal(s.key, value)
		if err != nil {
			return err
		}
		sealed[key] = encrypted
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range sealed {
		if _, err = tx.Exec("INSERT INTO settings(name,value,secret) VALUES(?,?,1) ON CONFLICT(name) DO UPDATE SET value=excluded.value,secret=1", key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
