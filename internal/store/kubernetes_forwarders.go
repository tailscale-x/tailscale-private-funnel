package store

import (
	"database/sql"
	"fmt"
)

func (s *Store) ForwarderParent(id string) string {
	var parent string
	_ = s.db.QueryRow(`SELECT parent_id FROM kubernetes_forwarders WHERE node_id=?`, id).Scan(&parent)
	return parent
}
func (s *Store) Forwarders(parent string) ([]NodeRecord, error) {
	rows, err := s.db.Query(`SELECT node_id FROM kubernetes_forwarders WHERE parent_id=? ORDER BY node_id`, parent)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []NodeRecord{}
	for _, id := range ids {
		n, e := s.Node(id)
		if e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, nil
}

// ReserveForwarder never rebinds a registered device or changes a signing key.
func (s *Store) ReserveForwarder(parent, uid, hostname, public string) (NodeRecord, error) {
	p, e := s.Node(parent)
	if e != nil || p.Revoked || p.Mode != "k8s-gateway" {
		return NodeRecord{}, fmt.Errorf("active Kubernetes controller required")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return NodeRecord{}, e
	}
	defer tx.Rollback()
	var id, key string
	var revoked bool
	e = tx.QueryRow(`SELECT n.id,n.node_key,n.revoked FROM nodes n JOIN kubernetes_forwarders f ON f.node_id=n.id WHERE f.parent_id=? AND f.kubernetes_node_uid=?`, parent, uid).Scan(&id, &key, &revoked)
	if e == nil {
		if revoked || key != public {
			return NodeRecord{}, fmt.Errorf("revoked forwarder or signing key changed")
		}
		tx.Rollback()
		return s.Node(id)
	}
	if e != sql.ErrNoRows {
		return NodeRecord{}, e
	}
	id, e = NewResourceID()
	if e != nil {
		return NodeRecord{}, e
	}
	_, e = tx.Exec(`INSERT INTO nodes(id,name,mode,hostname,node_key) VALUES(?,?,'k8s-forwarder',?,?)`, id, hostname, hostname, public)
	if e != nil {
		return NodeRecord{}, e
	}
	_, e = tx.Exec(`INSERT INTO kubernetes_forwarders VALUES(?,?,?)`, id, parent, uid)
	if e != nil {
		return NodeRecord{}, e
	}
	if e = tx.Commit(); e != nil {
		return NodeRecord{}, e
	}
	return s.Node(id)
}
