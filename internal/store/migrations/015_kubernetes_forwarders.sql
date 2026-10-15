CREATE TABLE kubernetes_forwarders (
 node_id TEXT PRIMARY KEY REFERENCES nodes(id),
 parent_id TEXT NOT NULL REFERENCES nodes(id),
 kubernetes_node_uid TEXT NOT NULL,
 UNIQUE(parent_id, kubernetes_node_uid)
);
