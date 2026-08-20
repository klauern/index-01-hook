ALTER TABLE delivery_tasks ADD COLUMN item_kind TEXT NOT NULL DEFAULT 'task'
    CHECK (item_kind IN ('task', 'note'));
