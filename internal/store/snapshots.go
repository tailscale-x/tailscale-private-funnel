package store

import (
	"errors"
	"time"
)

var ErrInvalidNodeSnapshot = errors.New("unknown or inactive component")

// AcceptNodeSnapshot shares the report nonce space without replacing exposures.
func (s *Store) AcceptNodeSnapshot(nodeID, nonce, setting string, payload []byte) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var valid int
	if err = tx.QueryRow(`SELECT count(*) FROM nodes WHERE id=? AND revoked=0`, nodeID).Scan(&valid); err != nil {
		return err
	}
	if valid != 1 {
		return ErrInvalidNodeSnapshot
	}
	if _, err = tx.Exec(`INSERT INTO report_nonces(node_id,nonce,received_at) VALUES(?,?,?)`, nodeID, nonce, time.Now().Unix()); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO settings(name,value,secret) VALUES(?,?,0) ON CONFLICT(name) DO UPDATE SET value=excluded.value,secret=0`, setting, string(payload)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteApplication(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owned int
	if err = tx.QueryRow(`SELECT count(*) FROM records r JOIN exposures e ON r.owner_id='exposure:'||e.id WHERE e.application_id=?`, id).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		return errors.New("clean up owned exposure records before deleting the application")
	}
	if _, err = tx.Exec(`DELETE FROM exposures WHERE application_id IN (SELECT id FROM applications WHERE id=? AND backend_source='tailnet' AND enabled=0)`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM applications WHERE id=? AND backend_source='tailnet' AND enabled=0`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// WasTailnetExposureDeleted proves ownership for idempotent cleanup retries;
// a guessed Service name or a shared tag is not sufficient ownership evidence.
func (s *Store) WasTailnetExposureDeleted(id string) (bool, error) {
	var owned bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM audit WHERE action=?)`, "tailnet.exposure.delete:"+id).Scan(&owned)
	return owned, err
}
