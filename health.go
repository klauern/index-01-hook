package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	workerStaleAfter  = 2 * time.Minute
	queueDelayedAfter = 15 * time.Minute
	providerSlowAfter = 25 * time.Second
)

type OperationalStatus struct {
	Status    string                    `json:"status"`
	CheckedAt string                    `json:"checked_at"`
	Reasons   []string                  `json:"reasons"`
	Intake    IntakeOperationalStatus   `json:"intake"`
	Worker    WorkerOperationalStatus   `json:"worker"`
	Queue     QueueOperationalStatus    `json:"queue"`
	Providers ProviderOperationalStatus `json:"providers"`
}

type IntakeOperationalStatus struct {
	RetainedUniqueRecordings int    `json:"retained_unique_recordings"`
	RetainedReceiveCount     int    `json:"retained_receive_count"`
	LastReceivedAt           string `json:"last_received_at,omitempty"`
	LastDuplicate            bool   `json:"last_duplicate"`
	LastResponseStatus       int    `json:"last_response_status,omitempty"`
}

type WorkerOperationalStatus struct {
	State                string `json:"state"`
	Stale                bool   `json:"stale"`
	StartedAt            string `json:"started_at,omitempty"`
	HeartbeatAt          string `json:"heartbeat_at,omitempty"`
	LastCycleStartedAt   string `json:"last_cycle_started_at,omitempty"`
	LastCycleCompletedAt string `json:"last_cycle_completed_at,omitempty"`
	LastCycleFailed      bool   `json:"last_cycle_failed"`
}

type QueueOperationalStatus struct {
	Active                 int   `json:"active"`
	OldestActiveAgeSeconds int64 `json:"oldest_active_age_seconds"`
	Retries                int   `json:"retries"`
	BlockedAuthentication  int   `json:"blocked_authentication"`
	NeedsReview            int   `json:"needs_review"`
	DeadLetter             int   `json:"dead_letter"`
}

type ProviderOperationalStatus struct {
	DeepSeek ProviderLatencyStatus `json:"deepseek"`
	TickTick ProviderLatencyStatus `json:"ticktick"`
}

type ProviderLatencyStatus struct {
	LastLatencyMilliseconds int64  `json:"last_latency_ms,omitempty"`
	LastObservedAt          string `json:"last_observed_at,omitempty"`
	LastFailed              bool   `json:"last_failed"`
}

func (s *Store) WorkerStarted(ctx context.Context, owner string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return fmt.Errorf("worker owner is required")
	}
	now := timestamp(s.now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO worker_health (
			singleton, owner, state, started_at, heartbeat_at, last_cycle_failed,
			deepseek_last_failed, ticktick_last_failed
		) VALUES (1, ?, 'running', ?, ?, 0, 0, 0)
		ON CONFLICT(singleton) DO UPDATE SET
			owner = excluded.owner,
			state = 'running',
			started_at = excluded.started_at,
			heartbeat_at = excluded.heartbeat_at,
			last_cycle_started_at = NULL,
			last_cycle_completed_at = NULL,
			last_cycle_failed = 0,
			deepseek_last_latency_ms = NULL,
			deepseek_last_observed_at = NULL,
			deepseek_last_failed = 0,
			ticktick_last_latency_ms = NULL,
			ticktick_last_observed_at = NULL,
			ticktick_last_failed = 0`, owner, now, now)
	if err != nil {
		return fmt.Errorf("record worker start: %w", err)
	}
	return nil
}

func (s *Store) WorkerCycleStarted(ctx context.Context, owner string) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return fmt.Errorf("worker owner is required")
	}
	now := timestamp(s.now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO worker_health (
			singleton, owner, state, started_at, heartbeat_at, last_cycle_started_at,
			last_cycle_failed, deepseek_last_failed, ticktick_last_failed
		) VALUES (1, ?, 'running', ?, ?, ?, 0, 0, 0)
		ON CONFLICT(singleton) DO UPDATE SET
			owner = excluded.owner,
			state = 'running',
			heartbeat_at = excluded.heartbeat_at,
			last_cycle_started_at = excluded.last_cycle_started_at`, owner, now, now, now)
	if err != nil {
		return fmt.Errorf("record worker cycle start: %w", err)
	}
	return nil
}

func (s *Store) WorkerCycleCompleted(ctx context.Context, owner string, failed bool) error {
	now := timestamp(s.now())
	result, err := s.db.ExecContext(ctx, `
		UPDATE worker_health
		SET heartbeat_at = ?, last_cycle_completed_at = ?, last_cycle_failed = ?
		WHERE singleton = 1 AND owner = ?`, now, now, healthBoolInt(failed), strings.TrimSpace(owner))
	if err != nil {
		return fmt.Errorf("record worker cycle completion: %w", err)
	}
	return requireHealthRow(result)
}

func (s *Store) WorkerStopped(ctx context.Context, owner string) error {
	now := timestamp(s.now())
	result, err := s.db.ExecContext(ctx, `
		UPDATE worker_health SET state = 'stopped', heartbeat_at = ?
		WHERE singleton = 1 AND owner = ?`, now, strings.TrimSpace(owner))
	if err != nil {
		return fmt.Errorf("record worker stop: %w", err)
	}
	return requireHealthRow(result)
}

func (s *Store) RecordProviderLatency(ctx context.Context, owner, provider string, duration time.Duration, failed bool) error {
	if duration < 0 {
		duration = 0
	}
	var statement string
	switch provider {
	case "deepseek":
		statement = `UPDATE worker_health SET
			deepseek_last_latency_ms = ?, deepseek_last_observed_at = ?, deepseek_last_failed = ?
			WHERE singleton = 1 AND owner = ?`
	case "ticktick":
		statement = `UPDATE worker_health SET
			ticktick_last_latency_ms = ?, ticktick_last_observed_at = ?, ticktick_last_failed = ?
			WHERE singleton = 1 AND owner = ?`
	default:
		return fmt.Errorf("provider is invalid")
	}
	result, err := s.db.ExecContext(ctx, statement, duration.Milliseconds(), timestamp(s.now()), healthBoolInt(failed), strings.TrimSpace(owner))
	if err != nil {
		return fmt.Errorf("record provider latency: %w", err)
	}
	return requireHealthRow(result)
}

func (s *Store) OperationalStatus(ctx context.Context) (OperationalStatus, error) {
	now := s.now().UTC()
	status := OperationalStatus{
		Status: "ok", CheckedAt: timestamp(now), Reasons: make([]string, 0),
		Worker: WorkerOperationalStatus{State: "missing"},
	}
	var startedAt, heartbeatAt, cycleStartedAt, cycleCompletedAt sql.NullString
	var workerState sql.NullString
	var cycleFailed, deepSeekFailed, tickTickFailed bool
	var deepSeekLatency, tickTickLatency sql.NullInt64
	var deepSeekObservedAt, tickTickObservedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT state, started_at, heartbeat_at, last_cycle_started_at,
			last_cycle_completed_at, last_cycle_failed,
			deepseek_last_latency_ms, deepseek_last_observed_at, deepseek_last_failed,
			ticktick_last_latency_ms, ticktick_last_observed_at, ticktick_last_failed
		FROM worker_health WHERE singleton = 1`).Scan(
		&workerState, &startedAt, &heartbeatAt, &cycleStartedAt, &cycleCompletedAt, &cycleFailed,
		&deepSeekLatency, &deepSeekObservedAt, &deepSeekFailed,
		&tickTickLatency, &tickTickObservedAt, &tickTickFailed,
	)
	if err != nil && !errorsIsNoRows(err) {
		return OperationalStatus{}, fmt.Errorf("query worker health: %w", err)
	}
	if err == nil {
		status.Worker = WorkerOperationalStatus{
			State: workerState.String, StartedAt: startedAt.String, HeartbeatAt: heartbeatAt.String,
			LastCycleStartedAt: cycleStartedAt.String, LastCycleCompletedAt: cycleCompletedAt.String,
			LastCycleFailed: cycleFailed,
		}
		if heartbeat, parseErr := time.Parse(time.RFC3339Nano, heartbeatAt.String); parseErr != nil {
			return OperationalStatus{}, fmt.Errorf("parse worker heartbeat: %w", parseErr)
		} else {
			status.Worker.Stale = now.Sub(heartbeat) > workerStaleAfter
		}
	}
	status.Providers.DeepSeek = providerLatencyStatus(deepSeekLatency, deepSeekObservedAt, deepSeekFailed)
	status.Providers.TickTick = providerLatencyStatus(tickTickLatency, tickTickObservedAt, tickTickFailed)

	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*), coalesce(sum(receive_count), 0)
		FROM recordings`).Scan(
		&status.Intake.RetainedUniqueRecordings,
		&status.Intake.RetainedReceiveCount,
	); err != nil {
		return OperationalStatus{}, fmt.Errorf("query intake totals: %w", err)
	}
	var lastReceivedAt sql.NullString
	var lastReceiveCount int
	err = s.db.QueryRowContext(ctx, `
		SELECT last_received_at, receive_count
		FROM recordings
		ORDER BY last_received_at DESC, id DESC
		LIMIT 1`).Scan(&lastReceivedAt, &lastReceiveCount)
	if err != nil && !errorsIsNoRows(err) {
		return OperationalStatus{}, fmt.Errorf("query latest intake: %w", err)
	}
	if err == nil {
		status.Intake.LastReceivedAt = lastReceivedAt.String
		status.Intake.LastDuplicate = lastReceiveCount > 1
		status.Intake.LastResponseStatus = 202
		if status.Intake.LastDuplicate {
			status.Intake.LastResponseStatus = 200
		}
	}

	var oldestActive sql.NullString
	err = s.db.QueryRowContext(ctx, `
		WITH active_queue AS (
			SELECT workflow_state, created_at FROM extraction_jobs WHERE workflow_state != 'complete'
			UNION ALL
			SELECT workflow_state, created_at FROM delivery_tasks WHERE workflow_state != 'complete'
		)
		SELECT count(*), min(created_at),
			coalesce(sum(CASE WHEN workflow_state = 'retry_wait' THEN 1 ELSE 0 END), 0),
			coalesce(sum(CASE WHEN workflow_state = 'blocked_auth' THEN 1 ELSE 0 END), 0),
			coalesce(sum(CASE WHEN workflow_state = 'needs_review' THEN 1 ELSE 0 END), 0),
			coalesce(sum(CASE WHEN workflow_state = 'dead_letter' THEN 1 ELSE 0 END), 0)
		FROM active_queue`).Scan(
		&status.Queue.Active, &oldestActive, &status.Queue.Retries,
		&status.Queue.BlockedAuthentication, &status.Queue.NeedsReview, &status.Queue.DeadLetter,
	)
	if err != nil {
		return OperationalStatus{}, fmt.Errorf("query queue health: %w", err)
	}
	if oldestActive.Valid {
		oldest, parseErr := time.Parse(time.RFC3339Nano, oldestActive.String)
		if parseErr != nil {
			return OperationalStatus{}, fmt.Errorf("parse oldest queue time: %w", parseErr)
		}
		if age := now.Sub(oldest); age > 0 {
			status.Queue.OldestActiveAgeSeconds = int64(age / time.Second)
		}
	}

	status.Reasons = healthReasons(status)
	if len(status.Reasons) != 0 {
		status.Status = "degraded"
	}
	return status, nil
}

func providerLatencyStatus(latency sql.NullInt64, observedAt sql.NullString, failed bool) ProviderLatencyStatus {
	status := ProviderLatencyStatus{LastObservedAt: observedAt.String, LastFailed: failed}
	if latency.Valid {
		status.LastLatencyMilliseconds = latency.Int64
	}
	return status
}

func healthReasons(status OperationalStatus) []string {
	reasons := make([]string, 0, 8)
	switch {
	case status.Worker.State == "missing":
		reasons = append(reasons, "worker_missing")
	case status.Worker.State == "stopped":
		reasons = append(reasons, "worker_stopped")
	case status.Worker.Stale:
		reasons = append(reasons, "worker_stale")
	}
	if status.Worker.LastCycleFailed {
		reasons = append(reasons, "worker_cycle_failed")
	}
	if status.Queue.OldestActiveAgeSeconds > int64(queueDelayedAfter/time.Second) {
		reasons = append(reasons, "queue_delayed")
	}
	if status.Queue.BlockedAuthentication > 0 {
		reasons = append(reasons, "blocked_authentication")
	}
	if status.Queue.NeedsReview > 0 {
		reasons = append(reasons, "needs_review")
	}
	if status.Queue.DeadLetter > 0 {
		reasons = append(reasons, "dead_letter")
	}
	if status.Providers.DeepSeek.LastLatencyMilliseconds > providerSlowAfter.Milliseconds() {
		reasons = append(reasons, "deepseek_slow")
	}
	if status.Providers.TickTick.LastLatencyMilliseconds > providerSlowAfter.Milliseconds() {
		reasons = append(reasons, "ticktick_slow")
	}
	return reasons
}

func requireHealthRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read worker health row count: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("worker health row is missing")
	}
	return nil
}

func errorsIsNoRows(err error) bool {
	return err == sql.ErrNoRows
}

func healthBoolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
