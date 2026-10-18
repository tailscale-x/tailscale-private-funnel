-- Existing enrolled identities remain valid. Pending legacy codes expire relative
-- to their original issuance rather than gaining a new validity window.
ALTER TABLE enrollments ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;
UPDATE enrollments SET expires_at=created_at+900;
