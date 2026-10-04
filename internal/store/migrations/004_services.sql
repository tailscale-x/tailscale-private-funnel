CREATE TABLE IF NOT EXISTS applications(
  id TEXT PRIMARY KEY,
  service_name TEXT NOT NULL UNIQUE,
  service_fqdn TEXT NOT NULL,
  service_port INTEGER NOT NULL,
  backend_port INTEGER NOT NULL,
  public_hostnames TEXT NOT NULL DEFAULT '[]',
  allowed_ingress_nodes TEXT NOT NULL DEFAULT '[]',
  policy_revision INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS service_hosts(
  service_name TEXT NOT NULL,
  device_id TEXT NOT NULL,
  approved INTEGER NOT NULL DEFAULT 0,
  approval_state TEXT NOT NULL DEFAULT 'pending',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(service_name, device_id)
);
CREATE TABLE IF NOT EXISTS relay_assignments(
  id TEXT PRIMARY KEY,
  application_id TEXT NOT NULL,
  node_id TEXT NOT NULL,
  container_id TEXT NOT NULL,
  backend_ip TEXT NOT NULL,
  backend_port INTEGER NOT NULL,
  networks TEXT NOT NULL DEFAULT '[]',
  reachable INTEGER NOT NULL DEFAULT 0,
  config_revision INTEGER NOT NULL DEFAULT 0,
  last_seen INTEGER NOT NULL DEFAULT 0,
  revoked INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS relay_assignments_node_idx ON relay_assignments(node_id);
CREATE INDEX IF NOT EXISTS applications_host_idx ON applications(service_name);
