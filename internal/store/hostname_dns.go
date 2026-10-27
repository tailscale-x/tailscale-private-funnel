package store

import (
	"errors"
	"strings"
)

var ErrHostnameDNSConflict = errors.New("hostname DNS settings changed; reload")

// SaveHostnameDNS uses one hostname row for every gateway/cluster contribution.
// Rows survive inactive exposure membership so rediscovery does not reset policy.
func (s *Store) SaveHostnameDNS(host string, proxied bool, revision uint64) error {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO hostname_dns(hostname) VALUES(?) ON CONFLICT(hostname) DO NOTHING`, host); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE hostname_dns SET cloudflare_proxied=?,revision=revision+1 WHERE hostname=? AND revision=?`, boolInt(proxied), host, revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrHostnameDNSConflict
	}
	return tx.Commit()
}
