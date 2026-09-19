-- Shadow verification is private evaluation evidence and follows evaluation expiry.
CREATE TABLE typesafe_shadow_verifications (
    recording_fingerprint TEXT NOT NULL,
    item_index            INTEGER NOT NULL CHECK (item_index >= 0),
    evidence_json         TEXT,
    scores_json           TEXT,
    decision              TEXT NOT NULL,
    model                 TEXT,
    prompt_version        TEXT NOT NULL,
    outcome               TEXT NOT NULL,
    error                 TEXT,
    error_kind            TEXT,
    created_at_ms         INTEGER NOT NULL,
    expires_at_ms         INTEGER NOT NULL,
    PRIMARY KEY (recording_fingerprint, item_index),
    FOREIGN KEY (recording_fingerprint) REFERENCES evaluation_evidence(recording_fingerprint) ON DELETE CASCADE,
    CHECK (expires_at_ms > created_at_ms)
);
CREATE INDEX typesafe_shadow_expiry_idx ON typesafe_shadow_verifications(expires_at_ms);
