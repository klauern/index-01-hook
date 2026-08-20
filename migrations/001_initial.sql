CREATE TABLE recordings (
    id                  INTEGER PRIMARY KEY,
    recorded_at_ms      INTEGER NOT NULL,
    client              TEXT NOT NULL,
    trigger             TEXT NOT NULL DEFAULT '',
    transcription       TEXT NOT NULL DEFAULT '',
    audio_filename      TEXT NOT NULL DEFAULT '',
    audio_byte_count    INTEGER NOT NULL DEFAULT 0 CHECK (audio_byte_count >= 0),
    payload_fingerprint TEXT NOT NULL UNIQUE,
    receive_count       INTEGER NOT NULL DEFAULT 1 CHECK (receive_count > 0),
    first_received_at   TEXT NOT NULL,
    last_received_at    TEXT NOT NULL
);

CREATE TABLE dispatches (
    id                  INTEGER PRIMARY KEY,
    recording_id        INTEGER NOT NULL REFERENCES recordings(id) ON DELETE CASCADE,
    destination         TEXT NOT NULL,
    status              TEXT NOT NULL,
    attempt_count       INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    response_json       TEXT,
    error_message       TEXT,
    created_at          TEXT NOT NULL,
    first_attempted_at  TEXT,
    last_attempted_at   TEXT,
    completed_at        TEXT,
    UNIQUE (recording_id, destination)
);

CREATE INDEX dispatches_claim_idx
    ON dispatches(destination, status, created_at);
