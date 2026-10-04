package store

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"sync"
)

var configurationKeyMu sync.Mutex

func (s *Store) ConfigurationKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	configurationKeyMu.Lock()
	defer configurationKeyMu.Unlock()
	value, err := s.GetSecret("control_configuration_key")
	if err == sql.ErrNoRows {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		if err := s.SetSecret("control_configuration_key", base64.RawStdEncoding.EncodeToString(key)); err != nil {
			return nil, nil, err
		}
		return pub, key, nil
	}
	if err != nil {
		return nil, nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("invalid configuration key")
	}
	priv := ed25519.PrivateKey(key)
	return priv.Public().(ed25519.PublicKey), priv, nil
}
func (s *Store) ServiceHosts() ([]ServiceHostRecord, error) {
	rows, err := s.db.Query(`SELECT service_name,device_id,approved,approval_state,updated_at FROM service_hosts ORDER BY service_name,device_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceHostRecord
	for rows.Next() {
		var h ServiceHostRecord
		if err := rows.Scan(&h.ServiceName, &h.DeviceID, &h.Approved, &h.ApprovalState, &h.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
