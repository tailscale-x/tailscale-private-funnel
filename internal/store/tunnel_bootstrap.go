package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrTunnelBootstrapRate = errors.New("tunnel bootstrap rate limit reached; retry after one minute")

// Reserve before external issuance. Failed issuance still counts against the
// budget. Both counters commit together; a restart cannot reset them.
func (s *Store) ReserveTunnelBootstrap(device string, now time.Time) error {
	if device == "" {
		return errors.New("verified device required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	window := now.Unix() / 60
	for _, limit := range []struct {
		key string
		max int
	}{{"tunnel_bootstrap:global", 30}, {fmt.Sprintf("tunnel_bootstrap:device:%x", sha256.Sum256([]byte(device))), 5}} {
		var raw string
		var count struct {
			Window int64 `json:"window"`
			Count  int   `json:"count"`
		}
		err = tx.QueryRow(`SELECT value FROM settings WHERE name=?`, limit.key).Scan(&raw)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			if err = json.Unmarshal([]byte(raw), &count); err != nil {
				return err
			}
		}
		if count.Window != window {
			count.Window = window
			count.Count = 0
		}
		if count.Count >= limit.max {
			return ErrTunnelBootstrapRate
		}
		count.Count++
		data, _ := json.Marshal(count)
		if _, err = tx.Exec(`INSERT INTO settings(name,value,secret) VALUES(?,?,0) ON CONFLICT(name) DO UPDATE SET value=excluded.value`, limit.key, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
