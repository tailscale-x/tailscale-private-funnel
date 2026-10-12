-- Move global DNS overrides to existing load balancers without altering records.
INSERT OR IGNORE INTO settings(name,value,secret)
SELECT 'node_deployment:'||n.id,json_object(
 'loadbalancer_hostname',json_extract(s.value,'$.loadbalancer_hostname'),
 'public_ipv4',CASE WHEN n.mode='loadbalancer' THEN coalesce(json_extract(s.value,'$.loadbalancer_public_ipv4'),'') ELSE '' END,
 'cert_email',CASE WHEN n.mode='loadbalancer' THEN coalesce(json_extract(s.value,'$.cert_email'),'') ELSE '' END,
 'label_keys',CASE WHEN n.mode='ingress' THEN coalesce(json_extract(s.value,'$.label_keys'),'') ELSE '' END,
 'deny_networks',CASE WHEN n.mode='ingress' THEN coalesce(json_extract(s.value,'$.deny_networks'),'') ELSE '' END
),0 FROM nodes n JOIN settings s ON s.name='tailnet' AND s.secret=0
WHERE n.mode IN ('loadbalancer','ingress');
UPDATE settings SET value=json_remove(value,'$.loadbalancer_public_ipv4')
WHERE name='tailnet' AND secret=0;
