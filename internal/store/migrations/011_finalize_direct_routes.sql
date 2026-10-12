-- Direct tailnet routes have no application Tailscale Service or ingress assignment.
DELETE FROM service_hosts WHERE service_name IN (
  SELECT service_name FROM applications WHERE backend_source='tailnet'
);
DELETE FROM backend_assignments WHERE application_id IN (
  SELECT id FROM applications WHERE backend_source='tailnet'
);
UPDATE applications SET service_name='direct:' || id, service_fqdn='', service_port=0,
  allowed_ingress_nodes='[]' WHERE backend_source='tailnet';
DELETE FROM settings WHERE name LIKE 'direct_cutover:%' OR name LIKE 'direct_previous:%';
