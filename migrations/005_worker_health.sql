CREATE TABLE worker_health (
    singleton                       INTEGER PRIMARY KEY CHECK (singleton = 1),
    owner                           TEXT NOT NULL,
    state                           TEXT NOT NULL CHECK (state IN ('running', 'stopped')),
    started_at                      TEXT NOT NULL,
    heartbeat_at                    TEXT NOT NULL,
    last_cycle_started_at           TEXT,
    last_cycle_completed_at         TEXT,
    last_cycle_failed               INTEGER NOT NULL DEFAULT 0 CHECK (last_cycle_failed IN (0, 1)),
    deepseek_last_latency_ms         INTEGER CHECK (deepseek_last_latency_ms >= 0),
    deepseek_last_observed_at        TEXT,
    deepseek_last_failed             INTEGER NOT NULL DEFAULT 0 CHECK (deepseek_last_failed IN (0, 1)),
    ticktick_last_latency_ms         INTEGER CHECK (ticktick_last_latency_ms >= 0),
    ticktick_last_observed_at        TEXT,
    ticktick_last_failed             INTEGER NOT NULL DEFAULT 0 CHECK (ticktick_last_failed IN (0, 1))
);
