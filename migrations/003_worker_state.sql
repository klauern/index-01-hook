ALTER TABLE extraction_jobs ADD COLUMN workflow_state TEXT NOT NULL DEFAULT 'received'
    CHECK (workflow_state IN (
        'received', 'extracting', 'extracted', 'retry_wait', 'blocked_auth',
        'needs_review', 'dead_letter', 'complete'
    ));

ALTER TABLE extraction_jobs ADD COLUMN cycle_attempt_count INTEGER NOT NULL DEFAULT 0
    CHECK (cycle_attempt_count >= 0);

UPDATE extraction_jobs SET cycle_attempt_count = attempt_count;

UPDATE extraction_jobs
SET workflow_state = CASE state
    WHEN 'pending' THEN 'received'
    WHEN 'leased' THEN 'extracting'
    WHEN 'retry' THEN 'retry_wait'
    WHEN 'frozen' THEN 'extracted'
    WHEN 'completed' THEN 'complete'
    WHEN 'review' THEN 'needs_review'
END;

ALTER TABLE delivery_tasks ADD COLUMN workflow_state TEXT NOT NULL DEFAULT 'extracted'
    CHECK (workflow_state IN (
        'extracted', 'creating', 'retry_wait', 'blocked_auth',
        'needs_review', 'dead_letter', 'complete'
    ));

ALTER TABLE delivery_tasks ADD COLUMN reconcile_attempt_count INTEGER NOT NULL DEFAULT 0
    CHECK (reconcile_attempt_count >= 0);

ALTER TABLE delivery_tasks ADD COLUMN cycle_attempt_count INTEGER NOT NULL DEFAULT 0
    CHECK (cycle_attempt_count >= 0);

UPDATE delivery_tasks SET cycle_attempt_count = attempt_count;

UPDATE delivery_tasks
SET workflow_state = CASE state
    WHEN 'pending' THEN 'extracted'
    WHEN 'leased' THEN 'creating'
    WHEN 'retry' THEN 'retry_wait'
    WHEN 'completed' THEN 'complete'
    WHEN 'review' THEN 'needs_review'
END;

CREATE TABLE workflow_transitions (
    domain      TEXT NOT NULL CHECK (domain IN ('extraction', 'delivery')),
    from_state  TEXT NOT NULL,
    to_state    TEXT NOT NULL,
    PRIMARY KEY (domain, from_state, to_state)
);

INSERT INTO workflow_transitions(domain, from_state, to_state) VALUES
    ('extraction', 'received', 'extracting'),
    ('extraction', 'retry_wait', 'extracting'),
    ('extraction', 'extracting', 'extracted'),
    ('extraction', 'extracting', 'retry_wait'),
    ('extraction', 'extracting', 'blocked_auth'),
    ('extraction', 'extracting', 'needs_review'),
    ('extraction', 'extracting', 'dead_letter'),
    ('extraction', 'extracting', 'complete'),
    ('extraction', 'blocked_auth', 'received'),
    ('extraction', 'needs_review', 'received'),
    ('extraction', 'dead_letter', 'received'),
    ('extraction', 'extracted', 'complete'),
    ('delivery', 'extracted', 'creating'),
    ('delivery', 'retry_wait', 'creating'),
    ('delivery', 'creating', 'retry_wait'),
    ('delivery', 'creating', 'blocked_auth'),
    ('delivery', 'creating', 'needs_review'),
    ('delivery', 'creating', 'dead_letter'),
    ('delivery', 'creating', 'complete'),
    ('delivery', 'blocked_auth', 'extracted'),
    ('delivery', 'needs_review', 'extracted'),
    ('delivery', 'dead_letter', 'extracted');

CREATE TRIGGER extraction_workflow_transition
BEFORE UPDATE OF workflow_state ON extraction_jobs
WHEN OLD.workflow_state != NEW.workflow_state
    AND NOT EXISTS (
        SELECT 1 FROM workflow_transitions
        WHERE domain = 'extraction'
            AND from_state = OLD.workflow_state
            AND to_state = NEW.workflow_state
    )
BEGIN
    SELECT RAISE(ABORT, 'illegal extraction workflow transition');
END;

CREATE TRIGGER delivery_workflow_transition
BEFORE UPDATE OF workflow_state ON delivery_tasks
WHEN OLD.workflow_state != NEW.workflow_state
    AND NOT EXISTS (
        SELECT 1 FROM workflow_transitions
        WHERE domain = 'delivery'
            AND from_state = OLD.workflow_state
            AND to_state = NEW.workflow_state
    )
BEGIN
    SELECT RAISE(ABORT, 'illegal delivery workflow transition');
END;
