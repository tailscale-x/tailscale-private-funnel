-- Remove manual/reported IP sources without changing the DNS ownership ledger.
UPDATE settings SET value=json_remove(value,'$.public_ipv4')
WHERE secret=0 AND (name LIKE 'node_deployment:%' OR name LIKE 'node_health:%');
UPDATE settings SET value=json_remove(value,'$.loadbalancer_public_ipv4')
WHERE secret=0 AND name='tailnet';
