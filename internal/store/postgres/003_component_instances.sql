CREATE TABLE component_instances (
 component_id TEXT PRIMARY KEY REFERENCES nodes(id),
 parent_id TEXT NOT NULL DEFAULT '',
 instance_id TEXT NOT NULL,
 device_id TEXT NOT NULL,
 generation BIGINT NOT NULL,
 lease_until BIGINT NOT NULL
);
