ALTER TABLE schema_migrations ADD COLUMN checksum TEXT NOT NULL DEFAULT '';

PRAGMA application_id = 1227895112;
