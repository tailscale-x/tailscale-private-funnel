ALTER TABLE applications RENAME COLUMN allowed_ingress_nodes TO allowed_gateway_nodes;
UPDATE nodes SET mode='docker-gateway' WHERE mode='ingress';
UPDATE enrollments SET mode='docker-gateway' WHERE mode='ingress';
UPDATE records SET owner_id='gateway:'||substr(owner_id,9) WHERE owner_id LIKE 'ingress:%';
UPDATE settings SET value=json_set(value,'$.rules',json(COALESCE((SELECT json_group_array(json(json_remove(json_set(j.value,'$.gateway_ids',json(COALESCE(json_extract(j.value,'$.ingress_ids'),json_extract(j.value,'$.gateway_ids'),'[]'))),'$.ingress_ids','$.loadbalancer_ids'))) FROM json_each(settings.value,'$.rules') j),'[]'))) WHERE name='exposure_policy' AND secret=0;

ALTER TABLE applications ADD COLUMN http_config TEXT NOT NULL DEFAULT '{}';
