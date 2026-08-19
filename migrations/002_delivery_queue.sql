CREATE TABLE extraction_jobs (
    recording_id            INTEGER PRIMARY KEY REFERENCES recordings(id) ON DELETE CASCADE,
    state                   TEXT NOT NULL CHECK (state IN ('pending', 'leased', 'retry', 'frozen', 'completed', 'review')),
    attempt_count           INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at_ms      INTEGER NOT NULL DEFAULT 0,
    lease_owner             TEXT,
    lease_expires_at_ms     INTEGER,
    last_classification     TEXT CHECK (
        last_classification IS NULL OR last_classification IN (
            'success', 'retryable', 'authentication', 'configuration',
            'malformed', 'ambiguous', 'review'
        )
    ),
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    completed_at            TEXT,
    CHECK ((lease_owner IS NULL) = (lease_expires_at_ms IS NULL))
);

INSERT INTO extraction_jobs (
    recording_id, state, attempt_count, next_attempt_at_ms,
    created_at, updated_at
)
SELECT recording_id, 'pending', 0, 0, created_at, created_at
FROM dispatches
WHERE destination = 'deepseek'
ON CONFLICT(recording_id) DO NOTHING;

CREATE INDEX extraction_jobs_claim_idx
    ON extraction_jobs(state, next_attempt_at_ms, lease_expires_at_ms, recording_id);

CREATE TABLE extractions (
    id                      INTEGER PRIMARY KEY,
    recording_id            INTEGER NOT NULL UNIQUE REFERENCES recordings(id) ON DELETE CASCADE,
    provider                TEXT NOT NULL,
    model                   TEXT NOT NULL,
    provider_response_id    TEXT,
    task_count              INTEGER NOT NULL CHECK (task_count BETWEEN 0 AND 10),
    created_at              TEXT NOT NULL
);

CREATE TABLE extraction_attempts (
    id                      INTEGER PRIMARY KEY,
    recording_id            INTEGER NOT NULL REFERENCES recordings(id) ON DELETE CASCADE,
    attempt_number          INTEGER NOT NULL CHECK (attempt_number > 0),
    classification          TEXT NOT NULL CHECK (classification IN (
        'success', 'retryable', 'authentication', 'configuration',
        'malformed', 'ambiguous', 'review'
    )),
    created_at              TEXT NOT NULL,
    UNIQUE(recording_id, attempt_number)
);

CREATE TABLE delivery_tasks (
    id                      INTEGER PRIMARY KEY,
    recording_id            INTEGER NOT NULL REFERENCES recordings(id) ON DELETE CASCADE,
    task_index              INTEGER NOT NULL CHECK (task_index >= 0),
    title                   TEXT NOT NULL,
    notes                   TEXT NOT NULL DEFAULT '',
    due_at                  TEXT,
    all_day                 INTEGER NOT NULL DEFAULT 0 CHECK (all_day IN (0, 1)),
    priority                INTEGER NOT NULL CHECK (priority IN (0, 1, 3, 5)),
    tags_json               TEXT NOT NULL DEFAULT '[]',
    project_alias           TEXT,
    marker                  TEXT NOT NULL UNIQUE,
    state                   TEXT NOT NULL CHECK (state IN ('pending', 'leased', 'retry', 'completed', 'review')),
    attempt_count           INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at_ms      INTEGER NOT NULL DEFAULT 0,
    lease_owner             TEXT,
    lease_expires_at_ms     INTEGER,
    last_classification     TEXT CHECK (
        last_classification IS NULL OR last_classification IN (
            'retryable', 'authentication', 'configuration', 'malformed',
            'ambiguous', 'review', 'created', 'reconciled'
        )
    ),
    ticktick_task_id        TEXT,
    ticktick_project_id     TEXT,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    completed_at            TEXT,
    UNIQUE(recording_id, task_index),
    CHECK ((lease_owner IS NULL) = (lease_expires_at_ms IS NULL))
);

CREATE INDEX delivery_tasks_claim_idx
    ON delivery_tasks(state, next_attempt_at_ms, lease_expires_at_ms, id);

CREATE TABLE delivery_attempts (
    id                      INTEGER PRIMARY KEY,
    delivery_task_id        INTEGER NOT NULL REFERENCES delivery_tasks(id) ON DELETE CASCADE,
    attempt_number          INTEGER NOT NULL CHECK (attempt_number > 0),
    classification          TEXT NOT NULL CHECK (classification IN (
        'retryable', 'authentication', 'configuration', 'malformed',
        'ambiguous', 'review', 'created', 'reconciled'
    )),
    created_at              TEXT NOT NULL,
    UNIQUE(delivery_task_id, attempt_number)
);
