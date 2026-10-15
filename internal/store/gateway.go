package store

// IsGateway identifies backend nodes; public load balancers have a separate role.
func IsGateway(mode string) bool {
	return mode == "docker-gateway" || mode == "tailnet-gateway" || mode == "k8s-gateway" || mode == "k8s-forwarder"
}
func BackendSource(mode string) string {
	switch mode {
	case "docker-gateway":
		return "docker"
	case "tailnet-gateway":
		return "tailnet"
	case "k8s-gateway", "k8s-forwarder":
		return "kubernetes"
	}
	return ""
}

func (s *Store) finishGatewayMigration() error {
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM pragma_table_info('exposures') WHERE name='loadbalancer_ids'").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		_, err := s.db.Exec("ALTER TABLE exposures DROP COLUMN loadbalancer_ids")
		return err
	}
	return nil
}
