package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

const maxEvaluationRetention = 365 * 24 * time.Hour

type EvaluationEvidence struct {
	Fingerprint      string               `json:"recording_fingerprint"`
	Transcript       string               `json:"transcript"`
	RecordedAtMillis int64                `json:"recorded_at_ms"`
	CapturedAt       time.Time            `json:"captured_at"`
	ExpiresAt        time.Time            `json:"expires_at"`
	Extraction       *FrozenExtraction    `json:"extraction,omitempty"`
	Deliveries       []EvaluationDelivery `json:"deliveries"`
}

type EvaluationDelivery struct {
	ItemIndex   int                    `json:"item_index"`
	TaskID      string                 `json:"task_id"`
	ProjectID   string                 `json:"project_id"`
	Kind        ItemKind               `json:"kind"`
	Title       string                 `json:"title"`
	Observation *EvaluationObservation `json:"observation,omitempty"`
}

type EvaluationObservation struct {
	TaskID              string    `json:"task_id"`
	ProjectID           string    `json:"project_id"`
	Marker              string    `json:"marker"`
	Kind                string    `json:"kind"`
	Title               string    `json:"title"`
	ObservedAt          time.Time `json:"observed_at"`
	ModifiedAt          string    `json:"modified_at"`
	Status              string    `json:"status"`
	LastKnownModifiedAt string    `json:"last_known_modified_at,omitempty"`
	LastKnownProjectID  string    `json:"last_known_project_id,omitempty"`
}

// ConfigureEvaluationCapture controls future capture. Zero disables capture.
// Existing evidence keeps its original expiry when this setting changes.
func (s *Store) ConfigureEvaluationCapture(retention time.Duration) error {
	if retention < 0 || retention > maxEvaluationRetention || (retention > 0 && retention < time.Millisecond) {
		return fmt.Errorf("evaluation retention must be zero or between one millisecond and 365 days")
	}
	s.evaluationRetention.Store(int64(retention))
	return nil
}

func (s *Store) captureEvaluationInput(ctx context.Context, tx *sql.Tx, recordingID int64, now time.Time) error {
	retention := time.Duration(s.evaluationRetention.Load())
	if retention == 0 {
		return nil
	}
	var fingerprint, transcript, received string
	var recorded int64
	var deadline sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT payload_fingerprint, transcription, recorded_at_ms, first_received_at, evaluation_expires_at_ms
		FROM recordings WHERE id = ?`, recordingID).Scan(&fingerprint, &transcript, &recorded, &received, &deadline); err != nil {
		return fmt.Errorf("read evaluation input: %w", err)
	}
	if transcript == "" {
		return nil
	}
	firstReceived, err := time.Parse(time.RFC3339Nano, received)
	if err != nil {
		return fmt.Errorf("read evaluation receipt time: %w", err)
	}
	// Keep the first deadline after evidence cleanup and retention changes.
	if !deadline.Valid {
		deadline = sql.NullInt64{Int64: firstReceived.Add(retention).UnixMilli(), Valid: true}
		if _, err := tx.ExecContext(ctx, `UPDATE recordings SET evaluation_expires_at_ms = ? WHERE id = ?`, deadline.Int64, recordingID); err != nil {
			return fmt.Errorf("save evaluation deadline: %w", err)
		}
	}
	expires := time.UnixMilli(deadline.Int64)
	if !expires.After(now) {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evaluation_evidence
		(recording_fingerprint, transcript, recorded_at_ms, captured_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(recording_fingerprint) DO NOTHING`,
		fingerprint, transcript, recorded, firstReceived.UnixMilli(), expires.UnixMilli()); err != nil {
		return fmt.Errorf("capture evaluation input: %w", err)
	}
	return nil
}

func (s *Store) captureEvaluationExtraction(ctx context.Context, tx *sql.Tx, recordingID int64, frozen FrozenExtraction, now time.Time) error {
	if s.evaluationRetention.Load() == 0 {
		return nil
	}
	if err := s.captureEvaluationInput(ctx, tx, recordingID, now); err != nil {
		return err
	}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		return fmt.Errorf("encode evaluation extraction: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE evaluation_evidence SET extraction_json = ?
		WHERE recording_fingerprint = (SELECT payload_fingerprint FROM recordings WHERE id = ?)
		AND extraction_json IS NULL AND expires_at_ms > ?`, string(encoded), recordingID, now.UnixMilli()); err != nil {
		return fmt.Errorf("capture evaluation extraction: %w", err)
	}
	return nil
}

func (s *Store) captureEvaluationDelivery(ctx context.Context, tx *sql.Tx, taskID int64, now time.Time) error {
	if s.evaluationRetention.Load() == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evaluation_deliveries
		(recording_fingerprint, item_index, task_id, project_id, item_kind, title)
		SELECT r.payload_fingerprint, d.task_index, d.ticktick_task_id, d.ticktick_project_id, d.item_kind, d.title
		FROM delivery_tasks d JOIN recordings r ON r.id = d.recording_id
		JOIN evaluation_evidence e ON e.recording_fingerprint = r.payload_fingerprint
		WHERE d.id = ? AND d.state = 'completed' AND e.expires_at_ms > ?
		ON CONFLICT(recording_fingerprint, item_index) DO NOTHING`, taskID, now.UnixMilli()); err != nil {
		return fmt.Errorf("capture evaluation delivery: %w", err)
	}
	return nil
}

// ListEvaluationEvidence returns private, unexpired evidence in a consistent snapshot.
func (s *Store) ListEvaluationEvidence(ctx context.Context) ([]EvaluationEvidence, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin evaluation export: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UTC().UnixMilli()
	rows, err := tx.QueryContext(ctx, `SELECT recording_fingerprint, transcript, recorded_at_ms,
		captured_at_ms, expires_at_ms, extraction_json FROM evaluation_evidence
		WHERE expires_at_ms > ? ORDER BY captured_at_ms, recording_fingerprint`, now)
	if err != nil {
		return nil, fmt.Errorf("read evaluation evidence: %w", err)
	}
	defer rows.Close()
	evidence := []EvaluationEvidence{}
	positions := map[string]int{}
	for rows.Next() {
		entry := EvaluationEvidence{Deliveries: []EvaluationDelivery{}}
		var captured, expires int64
		var extraction sql.NullString
		if err := rows.Scan(&entry.Fingerprint, &entry.Transcript, &entry.RecordedAtMillis, &captured, &expires, &extraction); err != nil {
			return nil, fmt.Errorf("scan evaluation evidence: %w", err)
		}
		entry.CapturedAt, entry.ExpiresAt = time.UnixMilli(captured).UTC(), time.UnixMilli(expires).UTC()
		if extraction.Valid {
			if err := json.Unmarshal([]byte(extraction.String), &entry.Extraction); err != nil {
				return nil, fmt.Errorf("decode evaluation extraction: %w", err)
			}
		}
		positions[entry.Fingerprint] = len(evidence)
		evidence = append(evidence, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read evaluation evidence rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close evaluation evidence rows: %w", err)
	}
	deliveries, err := tx.QueryContext(ctx, `SELECT d.recording_fingerprint, d.item_index, d.task_id, d.project_id, d.item_kind, d.title,
		o.observation_json
		FROM evaluation_deliveries d JOIN evaluation_evidence e ON e.recording_fingerprint = d.recording_fingerprint
		LEFT JOIN evaluation_observations o ON o.recording_fingerprint = d.recording_fingerprint AND o.item_index = d.item_index
		WHERE e.expires_at_ms > ? ORDER BY d.recording_fingerprint, d.item_index`, now)
	if err != nil {
		return nil, fmt.Errorf("read evaluation deliveries: %w", err)
	}
	defer deliveries.Close()
	for deliveries.Next() {
		var fingerprint string
		var delivery EvaluationDelivery
		var observation sql.NullString
		if err := deliveries.Scan(&fingerprint, &delivery.ItemIndex, &delivery.TaskID, &delivery.ProjectID, &delivery.Kind, &delivery.Title, &observation); err != nil {
			return nil, fmt.Errorf("scan evaluation delivery: %w", err)
		}
		if observation.Valid {
			if err := json.Unmarshal([]byte(observation.String), &delivery.Observation); err != nil {
				return nil, fmt.Errorf("decode evaluation observation: %w", err)
			}
		}
		position, ok := positions[fingerprint]
		if !ok {
			return nil, fmt.Errorf("evaluation delivery has no evidence")
		}
		evidence[position].Deliveries = append(evidence[position].Deliveries, delivery)
	}
	if err := deliveries.Err(); err != nil {
		return nil, fmt.Errorf("read evaluation delivery rows: %w", err)
	}
	if err := deliveries.Close(); err != nil {
		return nil, fmt.Errorf("close evaluation delivery rows: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("finish evaluation export: %w", err)
	}
	return evidence, nil
}

// SaveEvaluationObservations stores one complete collection batch atomically.
// Older observations cannot replace newer evidence.
func (s *Store) SaveEvaluationObservations(ctx context.Context, observations []EvaluationObservation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin evaluation observations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UTC().UnixMilli()
	seen := map[string]bool{}
	for _, observation := range observations {
		if !safeProviderIdentifier(observation.TaskID) || observation.ObservedAt.IsZero() || seen[observation.TaskID] {
			return fmt.Errorf("evaluation observation requires a unique task and observation time")
		}
		seen[observation.TaskID] = true
		if observation.Status != "verified" && observation.Status != "missing" && observation.Status != "ambiguous" {
			return fmt.Errorf("evaluation observation status is invalid")
		}
		rows, err := tx.QueryContext(ctx, `SELECT d.recording_fingerprint, d.item_index
			FROM evaluation_deliveries d JOIN evaluation_evidence e ON e.recording_fingerprint = d.recording_fingerprint
			WHERE d.task_id = ? AND e.expires_at_ms > ?`, observation.TaskID, now)
		if err != nil {
			return fmt.Errorf("find evaluation observation delivery: %w", err)
		}
		var fingerprint string
		var itemIndex, count int
		for rows.Next() {
			if err := rows.Scan(&fingerprint, &itemIndex); err != nil {
				_ = rows.Close()
				return fmt.Errorf("read evaluation observation delivery: %w", err)
			}
			count++
		}
		rowErr := rows.Err()
		closeErr := rows.Close()
		if rowErr != nil {
			return fmt.Errorf("read evaluation observation deliveries: %w", rowErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close evaluation observation deliveries: %w", closeErr)
		}
		if count == 0 {
			continue
		}
		if count != 1 {
			return fmt.Errorf("evaluation observation task has multiple deliveries")
		}
		if observation.Status == "verified" {
			marker, err := tickTickMarker(fingerprint, itemIndex)
			if err != nil || observation.Marker != marker || !safeProviderIdentifier(observation.ProjectID) {
				return fmt.Errorf("verified evaluation observation requires its delivery marker and project")
			}
		}
		var previousJSON string
		err = tx.QueryRowContext(ctx, `SELECT observation_json FROM evaluation_observations
			WHERE recording_fingerprint = ? AND item_index = ?`, fingerprint, itemIndex).Scan(&previousJSON)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("read prior evaluation observation: %w", err)
		}
		var previous EvaluationObservation
		if err == nil {
			if err := json.Unmarshal([]byte(previousJSON), &previous); err != nil {
				return fmt.Errorf("decode prior evaluation observation: %w", err)
			}
			if observation.ObservedAt.UnixMilli() <= previous.ObservedAt.UnixMilli() {
				continue
			}
		}
		observation = retainEvaluationChronology(previous, observation)
		encoded, err := json.Marshal(observation)
		if err != nil {
			return fmt.Errorf("encode evaluation observation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO evaluation_observations
			(recording_fingerprint, item_index, observed_at_ms, observation_json) VALUES (?, ?, ?, ?)
			ON CONFLICT(recording_fingerprint, item_index) DO UPDATE SET
			observed_at_ms = excluded.observed_at_ms, observation_json = excluded.observation_json
			WHERE excluded.observed_at_ms > evaluation_observations.observed_at_ms`,
			fingerprint, itemIndex, observation.ObservedAt.UTC().UnixMilli(), string(encoded)); err != nil {
			return fmt.Errorf("save evaluation observation: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit evaluation observations: %w", err)
	}
	return nil
}

func retainEvaluationChronology(previous, current EvaluationObservation) EvaluationObservation {
	// Watermarks come from verified stored observations, never from caller fields.
	current.LastKnownModifiedAt = previous.LastKnownModifiedAt
	current.LastKnownProjectID = previous.LastKnownProjectID
	if current.LastKnownModifiedAt == "" && previous.Status == "verified" {
		if _, ok := evaluationModifiedTime(previous.ModifiedAt); ok {
			current.LastKnownModifiedAt = previous.ModifiedAt
			current.LastKnownProjectID = previous.ProjectID
		}
	}
	if current.Status != "verified" || current.ModifiedAt == "" {
		return current
	}
	modified, ok := evaluationModifiedTime(current.ModifiedAt)
	if !ok {
		current.Status = "ambiguous"
		return current
	}
	if known, ok := evaluationModifiedTime(current.LastKnownModifiedAt); ok {
		if modified.Before(known) || (modified.Equal(known) && current.LastKnownProjectID != "" && current.ProjectID != current.LastKnownProjectID) {
			current.Status = "ambiguous"
			return current
		}
	}
	current.LastKnownModifiedAt, current.LastKnownProjectID = current.ModifiedAt, current.ProjectID
	return current
}

func evaluationModifiedTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999-0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// ListEvaluationCollectionTargets reads only delivery metadata for remote collection.
func (s *Store) ListEvaluationCollectionTargets(ctx context.Context) ([]EvaluationEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.recording_fingerprint, d.item_index, d.task_id, d.project_id, d.item_kind,
		o.observation_json FROM evaluation_evidence e
		JOIN evaluation_deliveries d ON d.recording_fingerprint = e.recording_fingerprint
		LEFT JOIN evaluation_observations o ON o.recording_fingerprint = d.recording_fingerprint AND o.item_index = d.item_index
		WHERE e.expires_at_ms > ? ORDER BY e.recording_fingerprint, d.item_index`, s.now().UTC().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("read evaluation collection targets: %w", err)
	}
	defer rows.Close()
	entries := []EvaluationEvidence{}
	for rows.Next() {
		var fingerprint string
		var delivery EvaluationDelivery
		var observation sql.NullString
		if err := rows.Scan(&fingerprint, &delivery.ItemIndex, &delivery.TaskID, &delivery.ProjectID, &delivery.Kind, &observation); err != nil {
			return nil, fmt.Errorf("scan evaluation collection target: %w", err)
		}
		if observation.Valid {
			if err := json.Unmarshal([]byte(observation.String), &delivery.Observation); err != nil {
				return nil, fmt.Errorf("decode evaluation collection observation: %w", err)
			}
		}
		if len(entries) == 0 || entries[len(entries)-1].Fingerprint != fingerprint {
			entries = append(entries, EvaluationEvidence{Fingerprint: fingerprint, Deliveries: []EvaluationDelivery{}})
		}
		entry := &entries[len(entries)-1]
		entry.Deliveries = append(entry.Deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read evaluation collection target rows: %w", err)
	}
	return entries, nil
}
