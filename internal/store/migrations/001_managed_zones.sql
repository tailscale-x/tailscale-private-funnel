CREATE TABLE IF NOT EXISTS managed_zones(
  provider_id TEXT NOT NULL,
  zone TEXT NOT NULL,
  PRIMARY KEY(provider_id, zone)
);
