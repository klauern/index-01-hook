-- Evidence has its own retention period. Queue deletion does not delete evidence.
CREATE TABLE evaluation_evidence (
    recording_fingerprint TEXT PRIMARY KEY,
    transcript            TEXT NOT NULL,
    recorded_at_ms        INTEGER NOT NULL,
    captured_at_ms        INTEGER NOT NULL,
    expires_at_ms         INTEGER NOT NULL,
    extraction_json       TEXT,
    CHECK (expires_at_ms > captured_at_ms)
);

CREATE INDEX evaluation_evidence_expiry_idx ON evaluation_evidence(expires_at_ms);

CREATE TABLE evaluation_deliveries (
    recording_fingerprint TEXT NOT NULL REFERENCES evaluation_evidence(recording_fingerprint) ON DELETE CASCADE,
    item_index            INTEGER NOT NULL CHECK (item_index >= 0),
    task_id               TEXT NOT NULL,
    project_id            TEXT NOT NULL,
    item_kind             TEXT NOT NULL,
    title                 TEXT NOT NULL,
    PRIMARY KEY (recording_fingerprint, item_index)
);

CREATE INDEX evaluation_deliveries_task_idx ON evaluation_deliveries(task_id);

CREATE TABLE evaluation_observations (
    recording_fingerprint TEXT NOT NULL,
    item_index            INTEGER NOT NULL,
    observed_at_ms        INTEGER NOT NULL,
    observation_json      TEXT NOT NULL,
    PRIMARY KEY (recording_fingerprint, item_index),
    FOREIGN KEY (recording_fingerprint, item_index)
        REFERENCES evaluation_deliveries(recording_fingerprint, item_index) ON DELETE CASCADE
);
