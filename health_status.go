package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

func (s *Store) OperationalStatus(ctx context.Context) (OperationalStatus, error) {
	now := s.now().UTC()
	status := OperationalStatus{
		Status: "ok", CheckedAt: timestamp(now), Reasons: make([]string, 0),
		Worker: WorkerOperationalStatus{State: "missing"},
	}
	if err := s.readWorkerHealth(ctx, now, &status); err != nil {
		return OperationalStatus{}, err
	}
	if err := s.readIntakeHealth(ctx, &status); err != nil {
		return OperationalStatus{}, err
	}
	if err := s.readQueueHealth(ctx, now, &status); err != nil {
		return OperationalStatus{}, err
	}

	status.Reasons = healthReasons(status)
	if len(status.Reasons) != 0 {
		status.Status = "degraded"
	}
	return status, nil
}

func (s *Store) readWorkerHealth(ctx context.Context, now time.Time, status *OperationalStatus) error {
	var startedAt, heartbeatAt, cycleStartedAt, cycleCompletedAt sql.NullString
	var workerState sql.NullString
	var cycleFailed, deepSeekFailed, tickTickFailed, typeSafeFailed bool
	var deepSeekLatency, tickTickLatency, typeSafeLatency sql.NullInt64
	var deepSeekObservedAt, tickTickObservedAt, typeSafeObservedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT state, started_at, heartbeat_at, last_cycle_started_at,
			last_cycle_completed_at, last_cycle_failed,
			deepseek_last_latency_ms, deepseek_last_observed_at, deepseek_last_failed,
			ticktick_last_latency_ms, ticktick_last_observed_at, ticktick_last_failed,
			typesafe_last_latency_ms, typesafe_last_observed_at, typesafe_last_failed
		FROM worker_health WHERE singleton = 1`).Scan(
		&workerState, &startedAt, &heartbeatAt, &cycleStartedAt, &cycleCompletedAt, &cycleFailed,
		&deepSeekLatency, &deepSeekObservedAt, &deepSeekFailed,
		&tickTickLatency, &tickTickObservedAt, &tickTickFailed,
		&typeSafeLatency, &typeSafeObservedAt, &typeSafeFailed,
	)
	if err != nil && !errorsIsNoRows(err) {
		return fmt.Errorf("query worker health: %w", err)
	}
	if err == nil {
		status.Worker = WorkerOperationalStatus{
			State: workerState.String, StartedAt: startedAt.String, HeartbeatAt: heartbeatAt.String,
			LastCycleStartedAt: cycleStartedAt.String, LastCycleCompletedAt: cycleCompletedAt.String,
			LastCycleFailed: cycleFailed,
		}
		if heartbeat, parseErr := time.Parse(time.RFC3339Nano, heartbeatAt.String); parseErr != nil {
			return fmt.Errorf("parse worker heartbeat: %w", parseErr)
		} else {
			status.Worker.Stale = now.Sub(heartbeat) > workerStaleAfter
		}
	}
	status.Providers.DeepSeek = providerLatencyStatus(deepSeekLatency, deepSeekObservedAt, deepSeekFailed)
	status.Providers.TickTick = providerLatencyStatus(tickTickLatency, tickTickObservedAt, tickTickFailed)

	status.Providers.TypeSafe = providerLatencyStatus(typeSafeLatency, typeSafeObservedAt, typeSafeFailed)

	return nil
}

func (s *Store) readIntakeHealth(ctx context.Context, status *OperationalStatus) error {
	if err := s.db.QueryRowContext(ctx, `
		SELECT count(*), coalesce(sum(receive_count), 0)
		FROM recordings`).Scan(
		&status.Intake.RetainedUniqueRecordings,
		&status.Intake.RetainedReceiveCount,
	); err != nil {
		return fmt.Errorf("query intake totals: %w", err)
	}
	var lastReceivedAt sql.NullString
	var lastReceiveCount int
	err := s.db.QueryRowContext(ctx, `
		SELECT last_received_at, receive_count
		FROM recordings
		ORDER BY last_received_at DESC, id DESC
		LIMIT 1`).Scan(&lastReceivedAt, &lastReceiveCount)
	if err != nil && !errorsIsNoRows(err) {
		return fmt.Errorf("query latest intake: %w", err)
	}
	if err == nil {
		status.Intake.LastReceivedAt = lastReceivedAt.String
		status.Intake.LastDuplicate = lastReceiveCount > 1
		status.Intake.LastResponseStatus = 202
		if status.Intake.LastDuplicate {
			status.Intake.LastResponseStatus = 200
		}
	}

	return nil
}

func (s *Store) readQueueHealth(ctx context.Context, now time.Time, status *OperationalStatus) error {

	var oldestActive sql.NullString
	err := s.db.QueryRowContext(ctx, `
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
		return fmt.Errorf("query queue health: %w", err)
	}
	if oldestActive.Valid {
		oldest, parseErr := time.Parse(time.RFC3339Nano, oldestActive.String)
		if parseErr != nil {
			return fmt.Errorf("parse oldest queue time: %w", parseErr)
		}
		if age := now.Sub(oldest); age > 0 {
			status.Queue.OldestActiveAgeSeconds = int64(age / time.Second)
		}
	}

	return nil
}
