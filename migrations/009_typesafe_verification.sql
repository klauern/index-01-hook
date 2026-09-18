ALTER TABLE extraction_jobs ADD COLUMN verification_json TEXT;
ALTER TABLE worker_health ADD COLUMN typesafe_last_latency_ms INTEGER CHECK (typesafe_last_latency_ms >= 0);
ALTER TABLE worker_health ADD COLUMN typesafe_last_observed_at TEXT;
ALTER TABLE worker_health ADD COLUMN typesafe_last_failed INTEGER NOT NULL DEFAULT 0 CHECK (typesafe_last_failed IN (0, 1));
