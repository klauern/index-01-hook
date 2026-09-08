package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

type evaluationExportObservation struct {
	TaskID              string         `json:"task_id"`
	ObservedAt          time.Time      `json:"observed_at"`
	Candidate           map[string]any `json:"candidate"`
	LastKnownModifiedAt string         `json:"last_known_modified_at,omitempty"`
}

type evaluationExportEvent struct {
	ID            string                      `json:"event_id"`
	Type          string                      `json:"type"`
	From          evaluationExportObservation `json:"from"`
	To            evaluationExportObservation `json:"to"`
	Actor         string                      `json:"actor"`
	Review        map[string]string           `json:"review"`
	ExpectedRoute map[string]string           `json:"expected_route"`
}

type evaluationExportFinding struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type evaluationExportLedger struct {
	Version      int                           `json:"format_version"`
	Type         string                        `json:"type"`
	CollectedAt  time.Time                     `json:"collected_at"`
	Digest       string                        `json:"latest_snapshot_digest"`
	Incomplete   bool                          `json:"incomplete"`
	Failures     []string                      `json:"collection_failures"`
	Reviews      map[string]json.RawMessage    `json:"reviews"`
	Observations []evaluationExportObservation `json:"observations"`
	Events       []evaluationExportEvent       `json:"events"`
	Findings     []evaluationExportFinding     `json:"findings"`
	Archive      []EvaluationEvidence          `json:"evidence_archive"`
}

type evaluationExportStatus struct {
	State                  string `json:"state"`
	Recordings             int    `json:"recordings"`
	OriginalInputs         int    `json:"original_inputs"`
	FrozenExtractions      int    `json:"frozen_extractions"`
	ZeroItemExtractions    int    `json:"zero_item_extractions"`
	DeliveryItems          int    `json:"delivery_items"`
	VerifiedObservations   int    `json:"verified_observations"`
	UnresolvedObservations int    `json:"unresolved_observations"`
	MoveLabels             int    `json:"move_labels"`
	EligibleExamples       int    `json:"eligible_examples"`
	ImportedExamples       int    `json:"imported_examples"`
}

func runEvaluationOperator(ctx context.Context, args []string, getenv func(string) string, output io.Writer) error {
	path, err := normalizeDatabasePath(getenv("INDEX01_DB_PATH"))
	if err != nil {
		return err
	}
	if args[0] == "evaluation-collect" {
		client, err := NewTickTickClient(tickTickAPIBaseURL, getenv("INDEX01_TICKTICK_TOKEN"), &http.Client{Timeout: 30 * time.Second})
		if err != nil {
			return err
		}
		store, err := OpenStore(ctx, path)
		if err != nil {
			return err
		}
		defer ignoreCloseError(store)
		pollCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		count, err := collectEvaluationObservations(pollCtx, store, client)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(struct {
			State        string `json:"state"`
			Observations int    `json:"observations"`
		}{"collected", count})
	}
	db, err := openDashboardDatabase(ctx, path)
	if err != nil {
		return err
	}
	store := &Store{db: db, now: time.Now}
	defer ignoreCloseError(store)
	evidence, err := store.ListEvaluationEvidence(ctx)
	if err != nil {
		return err
	}
	ledger, status, err := buildEvaluationExport(evidence, time.Now().UTC())
	if err != nil {
		return err
	}
	if args[0] == "evaluation-export" {
		if err := writeEvaluationExport(args[1], ledger); err != nil {
			return err
		}
		status.State = "exported"
	}
	return json.NewEncoder(output).Encode(status)
}

func buildEvaluationExport(evidence []EvaluationEvidence, now time.Time) (evaluationExportLedger, evaluationExportStatus, error) {
	if evidence == nil {
		evidence = []EvaluationEvidence{}
	}
	archive, err := json.Marshal(evidence)
	if err != nil {
		return evaluationExportLedger{}, evaluationExportStatus{}, fmt.Errorf("encode private evaluation archive: %w", err)
	}
	ledger := evaluationExportLedger{Version: 1, Type: "routing_feedback_ledger", CollectedAt: now.UTC(),
		Digest: evidenceHash(archive), Failures: []string{}, Reviews: map[string]json.RawMessage{},
		Observations: []evaluationExportObservation{}, Events: []evaluationExportEvent{}, Findings: []evaluationExportFinding{}, Archive: evidence}
	status := evaluationExportStatus{State: "available", Recordings: len(evidence)}
	for _, recording := range evidence {
		if recording.Transcript != "" {
			status.OriginalInputs++
		}
		if recording.Extraction == nil {
			continue
		}
		status.FrozenExtractions++
		if len(frozenItems(*recording.Extraction)) == 0 {
			status.ZeroItemExtractions++
		}
		for _, delivery := range recording.Deliveries {
			status.DeliveryItems++
			original := evaluationExportCandidate(recording, delivery, delivery.ProjectID, "")
			from := evaluationExportObservation{TaskID: delivery.TaskID, ObservedAt: recording.CapturedAt, Candidate: original}
			latest := from
			finding := evaluationExportFinding{TaskID: delivery.TaskID, Status: "not_observed", Reason: "no_verified_remote_observation"}
			observation := delivery.Observation
			kind := evaluationExportKind(delivery.Kind)
			marker, markerErr := tickTickMarker(recording.Fingerprint, delivery.ItemIndex)
			verified := observation != nil && observation.Status == "verified" && observation.TaskID == delivery.TaskID &&
				markerErr == nil && observation.Marker == marker && safeProviderIdentifier(observation.ProjectID) &&
				(observation.Kind == "" || observation.Kind == kind) && !observation.ObservedAt.After(now) && observation.ObservedAt.After(recording.CapturedAt)
			if verified {
				status.VerifiedObservations++
				latest = evaluationExportObservation{TaskID: delivery.TaskID, ObservedAt: observation.ObservedAt,
					Candidate:           evaluationExportCandidate(recording, delivery, observation.ProjectID, observation.ModifiedAt),
					LastKnownModifiedAt: observation.LastKnownModifiedAt}
				finding.Status, finding.Reason = "unchanged", ""
				if canonicalEvaluationProject(delivery.ProjectID) != canonicalEvaluationProject(observation.ProjectID) {
					finding.Status = "observed_move"
					identity := delivery.TaskID + "\n" + marker + "\n" + canonicalEvaluationProject(observation.ProjectID)
					ledger.Events = append(ledger.Events, evaluationExportEvent{
						ID: "move-" + evidenceHash([]byte(identity)), Type: "observed_move", From: from, To: latest, Actor: "unknown",
						Review:        map[string]string{"status": "inferred", "basis": "owner_default", "interpretation": "original_routing_error"},
						ExpectedRoute: map[string]string{"project_id": canonicalEvaluationProject(observation.ProjectID)},
					})
					status.MoveLabels++
				}
			} else {
				status.UnresolvedObservations++
				if observation != nil && observation.Status != "missing" {
					finding.Status, finding.Reason = "requires_review", "remote_identity_or_chronology_unresolved"
				}
			}
			ledger.Observations = append(ledger.Observations, latest)
			ledger.Findings = append(ledger.Findings, finding)
		}
	}
	if len(ledger.Observations) > 0 {
		data, err := json.Marshal(ledger)
		if err != nil {
			return ledger, status, fmt.Errorf("encode evaluation ledger: %w", err)
		}
		corpus, err := evalcorpus.Import(data)
		if err != nil {
			return ledger, status, fmt.Errorf("validate evaluation export: %w", err)
		}
		status.ImportedExamples = len(corpus.Examples)
		for _, example := range corpus.Examples {
			if len(evalcorpus.Eligibility(corpus, example)) == 0 {
				status.EligibleExamples++
			}
		}
	}
	return ledger, status, nil
}

func evaluationExportCandidate(recording EvaluationEvidence, delivery EvaluationDelivery, project, modified string) map[string]any {
	input := map[string]any{"transcript": recording.Transcript, "transcript_provenance": "original", "model": recording.Extraction.Model}
	if captured := recording.Extraction.Evidence; captured != nil {
		input["routing"] = canonicalEvaluationRouting(captured.Routing)
		input["saved_output"] = captured.SavedOutput
		input["prompt_sha256"] = captured.PromptSHA256
	}
	return map[string]any{
		"candidate_id": "ticktick-" + delivery.TaskID,
		"source": map[string]any{"provider": "TickTick", "task_id": delivery.TaskID, "modified_at": modified,
			"markers": []any{map[string]any{"marker": evaluationMarker(recording.Fingerprint, delivery.ItemIndex), "recording_fingerprint": recording.Fingerprint, "item_index": delivery.ItemIndex}}},
		"observed_current":    map[string]any{"project_id": canonicalEvaluationProject(project), "title": delivery.Title, "kind": evaluationExportKind(delivery.Kind)},
		"input":               input,
		"historical_delivery": map[string]any{"original_project_id": delivery.ProjectID, "expected_item_count": len(frozenItems(*recording.Extraction))},
		"review":              map[string]string{"status": "unreviewed"},
	}
}

func evaluationExportKind(kind ItemKind) string {
	if kind == ItemKindNote {
		return "NOTE"
	}
	return "TEXT"
}

func canonicalEvaluationProject(project string) string {
	if !strings.HasPrefix(project, "inbox") {
		return project
	}
	for _, char := range strings.TrimPrefix(project, "inbox") {
		if char < '0' || char > '9' {
			return project
		}
	}
	return "inbox"
}

func canonicalEvaluationRouting(source *evalcorpus.RoutingConfig) *evalcorpus.RoutingConfig {
	if source == nil {
		return nil
	}
	routing := *source
	routing.Aliases = make(map[string]string, len(source.Aliases))
	for alias, project := range source.Aliases {
		routing.Aliases[alias] = canonicalEvaluationProject(project)
	}
	routing.DefaultProjectID, routing.NoteProjectID = canonicalEvaluationProject(source.DefaultProjectID), canonicalEvaluationProject(source.NoteProjectID)
	return &routing
}

func writeEvaluationExport(path string, ledger evaluationExportLedger) error {
	if strings.TrimSpace(path) == "" || path == "-" {
		return fmt.Errorf("evaluation export requires a private file path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create evaluation export directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create evaluation export file: %w", err)
	}
	success := false
	defer func() {
		_ = file.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(ledger); err != nil {
		return fmt.Errorf("write evaluation export: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync evaluation export: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close evaluation export: %w", err)
	}
	success = true
	return nil
}
