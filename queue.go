package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxExtractionTasks   = 10
	maxQueuedNotesBytes  = 32 << 10
	maxProjectAliasBytes = 100
	maxProviderIDBytes   = 256
)

var ErrLeaseLost = errors.New("queue lease is not active")
var ErrManualRetryNotAllowed = errors.New("manual retry is not allowed for this state")

type OutcomeClassification string

const (
	OutcomeSuccess        OutcomeClassification = "success"
	OutcomeRetryable      OutcomeClassification = "retryable"
	OutcomeAuthentication OutcomeClassification = "authentication"
	OutcomeConfiguration  OutcomeClassification = "configuration"
	OutcomeMalformed      OutcomeClassification = "malformed"
	OutcomeAmbiguous      OutcomeClassification = "ambiguous"
	OutcomeReview         OutcomeClassification = "review"
	OutcomeCreated        OutcomeClassification = "created"
	OutcomeReconciled     OutcomeClassification = "reconciled"
)

type ExtractionClaim struct {
	RecordingID        int64
	RecordingHash      string
	Transcription      string
	AttemptNumber      int
	CycleAttemptNumber int
}

type ItemKind string

const (
	ItemKindTask ItemKind = "task"
	ItemKindNote ItemKind = "note"
)

type QueuedItem struct {
	Kind         ItemKind
	Title        string
	Content      string
	Due          *time.Time
	AllDay       bool
	Priority     int
	Tags         []string
	ProjectAlias string
}

type QueuedTask struct {
	Title        string
	Notes        string
	Due          *time.Time
	AllDay       bool
	Priority     int
	Tags         []string
	ProjectAlias string
}

type FrozenExtraction struct {
	Provider           string
	Model              string
	ProviderResponseID string
	Items              []QueuedItem
	Tasks              []QueuedTask
}

type DeliveryClaim struct {
	ID                    int64
	RecordingID           int64
	RecordingHash         string
	TaskIndex             int
	Kind                  ItemKind
	Title                 string
	Notes                 string
	Due                   *time.Time
	AllDay                bool
	Priority              int
	Tags                  []string
	ProjectAlias          string
	Marker                string
	AttemptNumber         int
	CycleAttemptNumber    int
	LastClassification    OutcomeClassification
	ReconcileAttemptCount int
	RecoveredLease        bool
}

type DeliveryCompletion struct {
	TaskID            int64
	LeaseOwner        string
	Classification    OutcomeClassification
	TickTickTaskID    string
	TickTickProjectID string
}

type DeliveryStatus struct {
	ID                 int64    `json:"id"`
	TaskIndex          int      `json:"task_index"`
	Kind               ItemKind `json:"kind"`
	State              string   `json:"state"`
	AttemptCount       int      `json:"attempt_count"`
	LastClassification string   `json:"last_classification,omitempty"`
	Marker             string   `json:"marker"`
	TickTickTaskID     string   `json:"ticktick_task_id,omitempty"`
	TickTickProjectID  string   `json:"ticktick_project_id,omitempty"`
	UpdatedAt          string   `json:"updated_at"`
	NextAttemptAt      string   `json:"next_attempt_at,omitempty"`
}

type RecordingQueueStatus struct {
	RecordingID        int64            `json:"recording_id"`
	RecordingHash      string           `json:"recording_hash"`
	State              string           `json:"state"`
	AttemptCount       int              `json:"attempt_count"`
	LastClassification string           `json:"last_classification,omitempty"`
	Provider           string           `json:"provider,omitempty"`
	Model              string           `json:"model,omitempty"`
	ProviderResponseID string           `json:"provider_response_id,omitempty"`
	UpdatedAt          string           `json:"updated_at"`
	NextAttemptAt      string           `json:"next_attempt_at,omitempty"`
	Tasks              []DeliveryStatus `json:"tasks"`
}

func (s *Store) ClaimExtraction(ctx context.Context, owner string, leaseDuration time.Duration) (*ExtractionClaim, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || leaseDuration <= 0 {
		return nil, fmt.Errorf("owner and positive lease duration are required")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin extraction claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var claim ExtractionClaim
	err = tx.QueryRowContext(ctx, `
		SELECT j.recording_id, r.payload_fingerprint, r.transcription,
			j.attempt_count + 1, j.cycle_attempt_count + 1
		FROM extraction_jobs j
		JOIN recordings r ON r.id = j.recording_id
		WHERE (
			(j.state IN ('pending', 'retry') AND j.next_attempt_at_ms <= ?)
			OR (j.state = 'leased' AND j.lease_expires_at_ms <= ?)
		)
		ORDER BY j.next_attempt_at_ms, j.recording_id
		LIMIT 1`, now.UnixMilli(), now.UnixMilli()).Scan(
		&claim.RecordingID, &claim.RecordingHash, &claim.Transcription,
		&claim.AttemptNumber, &claim.CycleAttemptNumber,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select extraction claim: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE extraction_jobs
		SET state = 'leased', workflow_state = 'extracting', attempt_count = ?,
			cycle_attempt_count = ?, lease_owner = ?,
			lease_expires_at_ms = ?, updated_at = ?
		WHERE recording_id = ? AND (
			(state IN ('pending', 'retry') AND next_attempt_at_ms <= ?)
			OR (state = 'leased' AND lease_expires_at_ms <= ?)
		)`,
		claim.AttemptNumber, claim.CycleAttemptNumber, owner,
		now.Add(leaseDuration).UnixMilli(), timestamp(now),
		claim.RecordingID, now.UnixMilli(), now.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("claim extraction: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit extraction claim: %w", err)
	}
	return &claim, nil
}

func (s *Store) FreezeExtraction(ctx context.Context, recordingID int64, owner string, frozen FrozenExtraction) error {
	if err := validateFrozenExtraction(frozen); err != nil {
		return err
	}
	items := frozenItems(frozen)
	owner = strings.TrimSpace(owner)
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin extraction freeze: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var hash string
	var attempt int
	err = tx.QueryRowContext(ctx, `
		SELECT r.payload_fingerprint, j.attempt_count
		FROM extraction_jobs j
		JOIN recordings r ON r.id = j.recording_id
		WHERE j.recording_id = ? AND j.state = 'leased' AND j.lease_owner = ?
			AND j.lease_expires_at_ms > ?`, recordingID, owner, now.UnixMilli()).Scan(&hash, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("verify extraction lease: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO extractions (
			recording_id, provider, model, provider_response_id, task_count, created_at
		) VALUES (?, ?, ?, NULLIF(?, ''), ?, ?)
		ON CONFLICT(recording_id) DO NOTHING`,
		recordingID, strings.TrimSpace(frozen.Provider), strings.TrimSpace(frozen.Model),
		strings.TrimSpace(frozen.ProviderResponseID), len(items), timestamp(now),
	)
	if err != nil {
		return fmt.Errorf("freeze extraction: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return fmt.Errorf("extraction is already frozen: %w", err)
	}
	for index, item := range items {
		tags, _ := normalizeTickTickTags(item.Tags)
		tagsJSON, err := json.Marshal(tags)
		if err != nil {
			return fmt.Errorf("encode item tags: %w", err)
		}
		marker, err := tickTickMarker(hash, index)
		if err != nil {
			return fmt.Errorf("build item marker: %w", err)
		}
		var due any
		if item.Due != nil {
			due = item.Due.UTC().Format(time.RFC3339)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO delivery_tasks (
				recording_id, task_index, item_kind, title, notes, due_at, all_day,
				priority, tags_json, project_alias, marker, state,
				next_attempt_at_ms, created_at, updated_at, workflow_state
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, 'pending', ?, ?, ?, 'extracted')`,
			recordingID, index, item.Kind, strings.TrimSpace(item.Title), item.Content, due, item.AllDay,
			item.Priority, string(tagsJSON), strings.TrimSpace(item.ProjectAlias), marker,
			now.UnixMilli(), timestamp(now), timestamp(now),
		)
		if err != nil {
			return fmt.Errorf("freeze item %d: %w", index, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO extraction_attempts (recording_id, attempt_number, classification, created_at)
		VALUES (?, ?, ?, ?)`, recordingID, attempt, OutcomeSuccess, timestamp(now)); err != nil {
		return fmt.Errorf("record extraction attempt: %w", err)
	}
	state := "frozen"
	workflowState := "extracted"
	var completed any
	if len(items) == 0 {
		state = "completed"
		workflowState = "complete"
		completed = timestamp(now)
		if _, err := tx.ExecContext(ctx, `UPDATE recordings SET transcription = '' WHERE id = ?`, recordingID); err != nil {
			return fmt.Errorf("erase completed transcription: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE extraction_jobs
		SET state = ?, workflow_state = ?, lease_owner = NULL, lease_expires_at_ms = NULL,
			last_classification = ?, updated_at = ?, completed_at = ?
		WHERE recording_id = ?`, state, workflowState, OutcomeSuccess, timestamp(now), completed, recordingID); err != nil {
		return fmt.Errorf("complete extraction freeze: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit extraction freeze: %w", err)
	}
	return nil
}

func (s *Store) RetryExtraction(ctx context.Context, recordingID int64, owner string, classification OutcomeClassification, retryAt time.Time) error {
	if !validOutcome(classification) || classification == OutcomeSuccess || classification == OutcomeCreated || classification == OutcomeReconciled {
		return fmt.Errorf("retry classification is invalid")
	}
	return s.finishExtractionAttempt(ctx, recordingID, owner, classification, "retry", "retry_wait", retryAt)
}

func (s *Store) ReviewExtraction(ctx context.Context, recordingID int64, owner string, classification OutcomeClassification) error {
	if !validOutcome(classification) || classification == OutcomeSuccess || classification == OutcomeCreated || classification == OutcomeReconciled {
		return fmt.Errorf("review classification is invalid")
	}
	return s.finishExtractionAttempt(ctx, recordingID, owner, classification, "review", "needs_review", s.now())
}

func (s *Store) BlockExtraction(ctx context.Context, recordingID int64, owner string, classification OutcomeClassification, workflowState string) error {
	if classification != OutcomeRetryable && classification != OutcomeAuthentication && classification != OutcomeConfiguration && classification != OutcomeMalformed && classification != OutcomeReview {
		return fmt.Errorf("terminal extraction classification is invalid")
	}
	if workflowState != "blocked_auth" && workflowState != "needs_review" && workflowState != "dead_letter" {
		return fmt.Errorf("terminal extraction state is invalid")
	}
	return s.finishExtractionAttempt(ctx, recordingID, owner, classification, "review", workflowState, s.now())
}

func (s *Store) finishExtractionAttempt(ctx context.Context, recordingID int64, owner string, classification OutcomeClassification, state, workflowState string, retryAt time.Time) error {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin extraction outcome: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var attempt int
	err = tx.QueryRowContext(ctx, `
		SELECT attempt_count FROM extraction_jobs
		WHERE recording_id = ? AND state = 'leased' AND lease_owner = ?
			AND lease_expires_at_ms > ?`, recordingID, strings.TrimSpace(owner), now.UnixMilli()).Scan(&attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("verify extraction lease: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO extraction_attempts (recording_id, attempt_number, classification, created_at)
		VALUES (?, ?, ?, ?)`, recordingID, attempt, classification, timestamp(now)); err != nil {
		return fmt.Errorf("record extraction outcome: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE extraction_jobs
		SET state = ?, workflow_state = ?, next_attempt_at_ms = ?, lease_owner = NULL,
			lease_expires_at_ms = NULL, last_classification = ?, updated_at = ?
		WHERE recording_id = ?`, state, workflowState, retryAt.UTC().UnixMilli(), classification, timestamp(now), recordingID); err != nil {
		return fmt.Errorf("finish extraction outcome: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit extraction outcome: %w", err)
	}
	return nil
}

func (s *Store) ClaimDelivery(ctx context.Context, owner string, leaseDuration time.Duration) (*DeliveryClaim, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" || leaseDuration <= 0 {
		return nil, fmt.Errorf("owner and positive lease duration are required")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin delivery claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var claim DeliveryClaim
	var due sql.NullString
	var tagsJSON string
	err = tx.QueryRowContext(ctx, `
		SELECT t.id, t.recording_id, r.payload_fingerprint, t.task_index, t.item_kind,
			t.title, t.notes, t.due_at, t.all_day,
			t.priority, t.tags_json, COALESCE(t.project_alias, ''), t.marker,
			t.attempt_count + 1, t.cycle_attempt_count + 1, COALESCE(t.last_classification, ''),
			t.reconcile_attempt_count, t.state = 'leased'
		FROM delivery_tasks t
		JOIN recordings r ON r.id = t.recording_id
		WHERE (
			(t.state IN ('pending', 'retry') AND t.next_attempt_at_ms <= ?)
			OR (t.state = 'leased' AND t.lease_expires_at_ms <= ?)
		)
		ORDER BY t.next_attempt_at_ms, t.id
		LIMIT 1`, now.UnixMilli(), now.UnixMilli()).Scan(
		&claim.ID, &claim.RecordingID, &claim.RecordingHash, &claim.TaskIndex, &claim.Kind,
		&claim.Title, &claim.Notes, &due, &claim.AllDay,
		&claim.Priority, &tagsJSON, &claim.ProjectAlias, &claim.Marker,
		&claim.AttemptNumber, &claim.CycleAttemptNumber,
		&claim.LastClassification, &claim.ReconcileAttemptCount, &claim.RecoveredLease,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select delivery claim: %w", err)
	}
	if due.Valid {
		parsed, err := time.Parse(time.RFC3339, due.String)
		if err != nil {
			return nil, fmt.Errorf("decode queued due time: %w", err)
		}
		claim.Due = &parsed
	}
	if err := json.Unmarshal([]byte(tagsJSON), &claim.Tags); err != nil {
		return nil, fmt.Errorf("decode queued tags: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE delivery_tasks
		SET state = 'leased', workflow_state = 'creating', attempt_count = ?,
			cycle_attempt_count = ?, lease_owner = ?,
			lease_expires_at_ms = ?, updated_at = ?
		WHERE id = ? AND (
			(state IN ('pending', 'retry') AND next_attempt_at_ms <= ?)
			OR (state = 'leased' AND lease_expires_at_ms <= ?)
		)`, claim.AttemptNumber, claim.CycleAttemptNumber, owner,
		now.Add(leaseDuration).UnixMilli(), timestamp(now),
		claim.ID, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("claim delivery: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delivery claim: %w", err)
	}
	return &claim, nil
}

func (s *Store) CompleteDelivery(ctx context.Context, completion DeliveryCompletion) error {
	if completion.Classification != OutcomeCreated && completion.Classification != OutcomeReconciled {
		return fmt.Errorf("completion classification is invalid")
	}
	if !safeProviderIdentifier(completion.TickTickTaskID) || !safeProviderIdentifier(completion.TickTickProjectID) {
		return fmt.Errorf("TickTick identifiers are required")
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delivery completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	recordingID, attempt, err := activeDeliveryLease(ctx, tx, completion.TaskID, completion.LeaseOwner, now)
	if err != nil {
		return err
	}
	if err := insertDeliveryAttempt(ctx, tx, completion.TaskID, attempt, completion.Classification, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE delivery_tasks
		SET state = 'completed', workflow_state = 'complete', lease_owner = NULL, lease_expires_at_ms = NULL,
			last_classification = ?, ticktick_task_id = ?, ticktick_project_id = ?,
			updated_at = ?, completed_at = ?
		WHERE id = ?`, completion.Classification, strings.TrimSpace(completion.TickTickTaskID),
		strings.TrimSpace(completion.TickTickProjectID), timestamp(now), timestamp(now), completion.TaskID); err != nil {
		return fmt.Errorf("complete delivery: %w", err)
	}
	var remaining int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM delivery_tasks
		WHERE recording_id = ? AND state != 'completed'`, recordingID).Scan(&remaining); err != nil {
		return fmt.Errorf("count incomplete deliveries: %w", err)
	}
	if remaining == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE recordings SET transcription = '' WHERE id = ?`, recordingID); err != nil {
			return fmt.Errorf("erase completed transcription: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE extraction_jobs
			SET state = 'completed', workflow_state = 'complete', completed_at = ?, updated_at = ?
			WHERE recording_id = ?`, timestamp(now), timestamp(now), recordingID); err != nil {
			return fmt.Errorf("complete recording queue: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delivery completion: %w", err)
	}
	return nil
}

func (s *Store) RetryDelivery(ctx context.Context, taskID int64, owner string, classification OutcomeClassification, retryAt time.Time) error {
	if !validOutcome(classification) || classification == OutcomeSuccess || classification == OutcomeCreated || classification == OutcomeReconciled {
		return fmt.Errorf("retry classification is invalid")
	}
	return s.finishDeliveryAttempt(ctx, taskID, owner, classification, "retry", "retry_wait", retryAt, false)
}

func (s *Store) RetryDeliveryReconciliation(ctx context.Context, taskID int64, owner string, retryAt time.Time) error {
	return s.finishDeliveryAttempt(ctx, taskID, owner, OutcomeAmbiguous, "retry", "retry_wait", retryAt, true)
}

func (s *Store) ReviewDelivery(ctx context.Context, taskID int64, owner string, classification OutcomeClassification) error {
	if !validOutcome(classification) || classification == OutcomeSuccess || classification == OutcomeCreated || classification == OutcomeReconciled {
		return fmt.Errorf("review classification is invalid")
	}
	return s.finishDeliveryAttempt(ctx, taskID, owner, classification, "review", "needs_review", s.now(), false)
}

func (s *Store) BlockDelivery(ctx context.Context, taskID int64, owner string, classification OutcomeClassification, workflowState string) error {
	if classification != OutcomeRetryable && classification != OutcomeAuthentication && classification != OutcomeConfiguration && classification != OutcomeMalformed && classification != OutcomeAmbiguous && classification != OutcomeReview {
		return fmt.Errorf("terminal delivery classification is invalid")
	}
	if workflowState != "blocked_auth" && workflowState != "needs_review" && workflowState != "dead_letter" {
		return fmt.Errorf("terminal delivery state is invalid")
	}
	return s.finishDeliveryAttempt(ctx, taskID, owner, classification, "review", workflowState, s.now(), false)
}

func (s *Store) finishDeliveryAttempt(ctx context.Context, taskID int64, owner string, classification OutcomeClassification, state, workflowState string, retryAt time.Time, incrementReconcile bool) error {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delivery outcome: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, attempt, err := activeDeliveryLease(ctx, tx, taskID, owner, now)
	if err != nil {
		return err
	}
	if err := insertDeliveryAttempt(ctx, tx, taskID, attempt, classification, now); err != nil {
		return err
	}
	reconcileIncrement := 0
	if incrementReconcile {
		reconcileIncrement = 1
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE delivery_tasks
		SET state = ?, workflow_state = ?, next_attempt_at_ms = ?, lease_owner = NULL,
			lease_expires_at_ms = NULL, last_classification = ?, updated_at = ?
			, reconcile_attempt_count = reconcile_attempt_count + ?
		WHERE id = ?`, state, workflowState, retryAt.UTC().UnixMilli(), classification, timestamp(now), reconcileIncrement, taskID); err != nil {
		return fmt.Errorf("finish delivery outcome: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delivery outcome: %w", err)
	}
	return nil
}

func (s *Store) RecordingStatus(ctx context.Context, recordingID int64) (RecordingQueueStatus, error) {
	var status RecordingQueueStatus
	err := s.db.QueryRowContext(ctx, `
		SELECT j.recording_id, r.payload_fingerprint, j.workflow_state, j.attempt_count,
			COALESCE(j.last_classification, ''), COALESCE(e.provider, ''),
			COALESCE(e.model, ''), COALESCE(e.provider_response_id, ''),
			j.updated_at, j.next_attempt_at_ms
		FROM extraction_jobs j
		JOIN recordings r ON r.id = j.recording_id
		LEFT JOIN extractions e ON e.recording_id = j.recording_id
		WHERE j.recording_id = ?`, recordingID).Scan(
		&status.RecordingID, &status.RecordingHash, &status.State,
		&status.AttemptCount, &status.LastClassification, &status.Provider,
		&status.Model, &status.ProviderResponseID, &status.UpdatedAt, newUnixMillisTime(&status.NextAttemptAt),
	)
	if err != nil {
		return RecordingQueueStatus{}, fmt.Errorf("query recording status: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_index, item_kind, workflow_state, attempt_count,
			COALESCE(last_classification, ''), marker,
			COALESCE(ticktick_task_id, ''), COALESCE(ticktick_project_id, ''),
			updated_at, next_attempt_at_ms
		FROM delivery_tasks WHERE recording_id = ? ORDER BY task_index`, recordingID)
	if err != nil {
		return RecordingQueueStatus{}, fmt.Errorf("query delivery statuses: %w", err)
	}
	defer ignoreCloseError(rows)
	status.Tasks = make([]DeliveryStatus, 0)
	for rows.Next() {
		var task DeliveryStatus
		if err := rows.Scan(&task.ID, &task.TaskIndex, &task.Kind, &task.State, &task.AttemptCount,
			&task.LastClassification, &task.Marker, &task.TickTickTaskID, &task.TickTickProjectID,
			&task.UpdatedAt, newUnixMillisTime(&task.NextAttemptAt)); err != nil {
			return RecordingQueueStatus{}, fmt.Errorf("scan delivery status: %w", err)
		}
		status.Tasks = append(status.Tasks, task)
	}
	if err := rows.Err(); err != nil {
		return RecordingQueueStatus{}, fmt.Errorf("iterate delivery statuses: %w", err)
	}
	return status, nil
}

func (s *Store) RetryRecordingByID(ctx context.Context, recordingID int64) error {
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE extraction_jobs
		SET state = 'pending', workflow_state = 'received', cycle_attempt_count = 0,
			next_attempt_at_ms = ?, lease_owner = NULL, lease_expires_at_ms = NULL,
			last_classification = NULL, updated_at = ?, completed_at = NULL
		WHERE recording_id = ? AND workflow_state IN ('blocked_auth', 'needs_review', 'dead_letter')`,
		now.UnixMilli(), timestamp(now), recordingID)
	if err != nil {
		return fmt.Errorf("retry recording by ID: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return ErrManualRetryNotAllowed
	}
	return nil
}

func (s *Store) RetryDeliveryByID(ctx context.Context, taskID int64) error {
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `
		UPDATE delivery_tasks
		SET state = 'pending', workflow_state = 'extracted', cycle_attempt_count = 0,
			reconcile_attempt_count = 0, next_attempt_at_ms = ?, lease_owner = NULL,
			lease_expires_at_ms = NULL,
			last_classification = CASE
				WHEN last_classification = 'ambiguous' THEN last_classification
				ELSE NULL
			END,
			updated_at = ?, completed_at = NULL
		WHERE id = ? AND workflow_state IN ('blocked_auth', 'needs_review', 'dead_letter')`,
		now.UnixMilli(), timestamp(now), taskID)
	if err != nil {
		return fmt.Errorf("retry delivery by ID: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return ErrManualRetryNotAllowed
	}
	return nil
}

func activeDeliveryLease(ctx context.Context, tx *sql.Tx, taskID int64, owner string, now time.Time) (int64, int, error) {
	var recordingID int64
	var attempt int
	err := tx.QueryRowContext(ctx, `
		SELECT recording_id, attempt_count FROM delivery_tasks
		WHERE id = ? AND state = 'leased' AND lease_owner = ?
			AND lease_expires_at_ms > ?`, taskID, strings.TrimSpace(owner), now.UnixMilli()).Scan(&recordingID, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrLeaseLost
	}
	if err != nil {
		return 0, 0, fmt.Errorf("verify delivery lease: %w", err)
	}
	return recordingID, attempt, nil
}

func insertDeliveryAttempt(ctx context.Context, tx *sql.Tx, taskID int64, attempt int, classification OutcomeClassification, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO delivery_attempts (delivery_task_id, attempt_number, classification, created_at)
		VALUES (?, ?, ?, ?)`, taskID, attempt, classification, timestamp(now)); err != nil {
		return fmt.Errorf("record delivery outcome: %w", err)
	}
	return nil
}

func validateFrozenExtraction(frozen FrozenExtraction) error {
	if !safeProviderIdentifier(frozen.Provider) || !safeProviderIdentifier(frozen.Model) {
		return fmt.Errorf("provider and model are required")
	}
	if frozen.ProviderResponseID != "" && !safeProviderIdentifier(frozen.ProviderResponseID) {
		return fmt.Errorf("provider response identifier is invalid")
	}
	if len(frozen.Items) != 0 && len(frozen.Tasks) != 0 {
		return fmt.Errorf("extraction mixes items and legacy tasks")
	}
	if len(frozenItems(frozen)) > maxExtractionTasks {
		return fmt.Errorf("extraction has too many items")
	}
	for _, item := range frozen.Items {
		if item.Kind != ItemKindTask && item.Kind != ItemKindNote {
			return fmt.Errorf("item kind is invalid")
		}
		title := strings.TrimSpace(item.Title)
		if title == "" || len(title) > maxTickTickTitleBytes {
			return fmt.Errorf("item title is invalid")
		}
		if len(item.Content) > maxQueuedNotesBytes {
			return fmt.Errorf("item content is too large")
		}
		if item.Kind == ItemKindNote {
			if item.Due != nil || item.AllDay || item.Priority != 0 || len(item.Tags) != 0 || item.ProjectAlias != "" {
				return fmt.Errorf("note has task fields")
			}
			continue
		}
		if item.Due == nil && item.AllDay {
			return fmt.Errorf("all-day task requires a due date")
		}
		if !validTickTickPriority(item.Priority) {
			return fmt.Errorf("task priority is invalid")
		}
		if _, err := normalizeTickTickTags(item.Tags); err != nil {
			return fmt.Errorf("task tags are invalid")
		}
		if len(strings.TrimSpace(item.ProjectAlias)) > maxProjectAliasBytes {
			return fmt.Errorf("project alias is too large")
		}
	}
	for _, task := range frozen.Tasks {
		title := strings.TrimSpace(task.Title)
		if title == "" || len(title) > maxTickTickTitleBytes {
			return fmt.Errorf("task title is invalid")
		}
		if len(task.Notes) > maxQueuedNotesBytes {
			return fmt.Errorf("task notes are too large")
		}
		if task.Due == nil && task.AllDay {
			return fmt.Errorf("all-day task requires a due date")
		}
		if !validTickTickPriority(task.Priority) {
			return fmt.Errorf("task priority is invalid")
		}
		if _, err := normalizeTickTickTags(task.Tags); err != nil {
			return fmt.Errorf("task tags are invalid")
		}
		if len(strings.TrimSpace(task.ProjectAlias)) > maxProjectAliasBytes {
			return fmt.Errorf("project alias is too large")
		}
	}
	return nil
}

func frozenItems(frozen FrozenExtraction) []QueuedItem {
	if len(frozen.Items) != 0 || len(frozen.Tasks) == 0 {
		return frozen.Items
	}
	items := make([]QueuedItem, 0, len(frozen.Tasks))
	for _, task := range frozen.Tasks {
		items = append(items, QueuedItem{
			Kind:         ItemKindTask,
			Title:        task.Title,
			Content:      task.Notes,
			Due:          task.Due,
			AllDay:       task.AllDay,
			Priority:     task.Priority,
			Tags:         task.Tags,
			ProjectAlias: task.ProjectAlias,
		})
	}
	return items
}

func safeProviderIdentifier(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len(trimmed) > maxProviderIDBytes {
		return false
	}
	for _, character := range trimmed {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("-_.:/", character) {
			continue
		}
		return false
	}
	return true
}

func validOutcome(classification OutcomeClassification) bool {
	switch classification {
	case OutcomeSuccess, OutcomeRetryable, OutcomeAuthentication, OutcomeConfiguration,
		OutcomeMalformed, OutcomeAmbiguous, OutcomeReview, OutcomeCreated, OutcomeReconciled:
		return true
	default:
		return false
	}
}

func requireOneRow(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected row count: %w", err)
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

type unixMillisTime struct {
	destination *string
}

func newUnixMillisTime(destination *string) *unixMillisTime {
	return &unixMillisTime{destination: destination}
}

func (value *unixMillisTime) Scan(source any) error {
	millis, ok := source.(int64)
	if !ok {
		return fmt.Errorf("unix millisecond time has an invalid type")
	}
	if millis <= 0 {
		*value.destination = ""
		return nil
	}
	*value.destination = time.UnixMilli(millis).UTC().Format(time.RFC3339Nano)
	return nil
}
