package store

import "strings"

func (s *Store) ForwarderParent(id string) string {
	r, e := s.ComponentRegistration(id)
	if e != nil {
		return ""
	}
	return r.ParentID
}
func (s *Store) Forwarders(parent string) ([]NodeRecord, error) {
	rows, e := s.db.Query(`SELECT name FROM settings WHERE name LIKE 'component_registration:%' ORDER BY name`)
	if e != nil {
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			rows.Close()
			return nil, e
		}
		ids = append(ids, strings.TrimPrefix(name, "component_registration:"))
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []NodeRecord{}
	for _, id := range ids {
		reg, e := s.ComponentRegistration(id)
		if e != nil {
			return nil, e
		}
		if reg.ParentID == parent {
			n, e := s.Node(id)
			if e != nil {
				return nil, e
			}
			out = append(out, n)
		}
	}
	return out, nil
}
