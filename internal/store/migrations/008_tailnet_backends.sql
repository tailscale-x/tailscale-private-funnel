ALTER TABLE applications ADD COLUMN backend_source TEXT NOT NULL DEFAULT 'docker';
ALTER TABLE applications ADD COLUMN backend_device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN backend_addresses TEXT NOT NULL DEFAULT '[]';
