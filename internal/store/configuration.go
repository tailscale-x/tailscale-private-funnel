package store

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
