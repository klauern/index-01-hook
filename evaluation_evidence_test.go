package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func configureEvidenceStore(t *testing.T, retention time.Duration) (*Store, *adjustableClock) {
	t.Helper()
	store, clock := newQueueStore(t)
	if err := store.ConfigureEvaluationCapture(retention); err != nil {
		t.Fatal(err)
	}
	return store, clock
}

func readEvidence(t *testing.T, store *Store) []EvaluationEvidence {
	t.Helper()
	entries, err := store.ListEvaluationEvidence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func completeEvidenceDelivery(t *testing.T, store *Store) {
	t.Helper()
	claim, err := store.ClaimDelivery(context.Background(), "delivery-worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim delivery = %v, %v", claim, err)
	}
	if err := store.CompleteDelivery(context.Background(), DeliveryCompletion{
		TaskID: claim.ID, LeaseOwner: "delivery-worker", TickTickTaskID: "task-1", TickTickProjectID: "project-1", Classification: OutcomeCreated,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEvaluationEvidenceCaptureDisabledAndRetentionValidation(t *testing.T) {
	store, _ := newQueueStore(t)
	for _, retention := range []time.Duration{-time.Hour, time.Nanosecond, maxEvaluationRetention + time.Hour} {
		if err := store.ConfigureEvaluationCapture(retention); err == nil {
			t.Fatalf("accepted invalid retention %s", retention)
		}
	}
	saveQueueRecording(t, store, "Original input")
	freezeQueueItems(t, store, "worker", []QueuedItem{{Kind: ItemKindTask, Title: "Task"}})
	completeEvidenceDelivery(t, store)
	if got := readEvidence(t, store); len(got) != 0 {
		t.Fatalf("disabled capture returned %d entries", len(got))
	}
}

func TestEvaluationEvidenceSurvivesCompletionAndQueueRetention(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, 90*24*time.Hour)
	receipt := saveQueueRecording(t, store, "  Original input with whitespace.\n")
	initial := readEvidence(t, store)
	if len(initial) != 1 || initial[0].Transcript != "  Original input with whitespace.\n" || initial[0].Extraction != nil {
		t.Fatalf("initial evidence = %+v", initial)
	}
	claim, err := store.ClaimExtraction(ctx, "worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim = %v, %v", claim, err)
	}
	frozen := FrozenExtraction{
		Provider: "deepseek", Model: "model", ProviderResponseID: "response-1",
		Items: []QueuedItem{{Kind: ItemKindTask, Title: "Task", Content: "Details"}},
		Evidence: &ExtractionEvidence{Clock: clock.Time(), TimeZone: "UTC", SystemPrompt: "Test prompt",
			Schema: json.RawMessage(`{"type":"object"}`), SavedOutput: json.RawMessage(`{"items":[]}`)},
	}
	if err := store.FreezeExtraction(ctx, claim.RecordingID, "worker", frozen); err != nil {
		t.Fatal(err)
	}
	completeEvidenceDelivery(t, store)
	var transcript string
	if err := store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript); err != nil || transcript != "" {
		t.Fatalf("queue transcript = %q, %v", transcript, err)
	}
	clock.Advance(terminalRecordRetention + time.Hour)
	if deleted, err := store.PurgeExpiredRecordings(ctx, terminalRecordRetention); err != nil || deleted != 1 {
		t.Fatalf("queue purge = %d, %v", deleted, err)
	}
	got := readEvidence(t, store)
	if len(got) != 1 || got[0].Transcript != initial[0].Transcript || !reflect.DeepEqual(got[0].Extraction, &frozen) {
		t.Fatalf("retained evidence = %+v", got)
	}
	if want := []EvaluationDelivery{{ItemIndex: 0, TaskID: "task-1", ProjectID: "project-1", Kind: ItemKindTask, Title: "Task"}}; !reflect.DeepEqual(got[0].Deliveries, want) {
		t.Fatalf("deliveries = %+v, want %+v", got[0].Deliveries, want)
	}
	if !got[0].ExpiresAt.Equal(initial[0].ExpiresAt) {
		t.Fatal("completion renewed evidence expiry")
	}
}

func TestEvaluationEvidenceZeroItemsAndFailedLease(t *testing.T) {
	store, clock := configureEvidenceStore(t, 24*time.Hour)
	receipt := saveQueueRecording(t, store, "There is nothing to do.")
	claim, err := store.ClaimExtraction(context.Background(), "worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim = %v, %v", claim, err)
	}
	frozen := FrozenExtraction{Provider: "deepseek", Model: "model", Items: []QueuedItem{}}
	if err := store.FreezeExtraction(context.Background(), receipt.ID, "wrong-owner", frozen); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong-owner freeze = %v", err)
	}
	if got := readEvidence(t, store); len(got) != 1 || got[0].Extraction != nil {
		t.Fatalf("failed lease changed evidence: %+v", got)
	}
	clock.Advance(time.Second)
	if err := store.FreezeExtraction(context.Background(), receipt.ID, "worker", frozen); err != nil {
		t.Fatal(err)
	}
	got := readEvidence(t, store)
	if len(got) != 1 || got[0].Extraction == nil || len(frozenItems(*got[0].Extraction)) != 0 || got[0].Transcript != "There is nothing to do." {
		t.Fatalf("zero-item evidence = %+v", got)
	}
}

func TestEvaluationEvidenceFailedCaptureRollsBackFreeze(t *testing.T) {
	store, _ := configureEvidenceStore(t, 24*time.Hour)
	receipt := saveQueueRecording(t, store, "Original input")
	claim, err := store.ClaimExtraction(context.Background(), "worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim = %v, %v", claim, err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_capture BEFORE UPDATE ON evaluation_evidence
		BEGIN SELECT RAISE(ABORT, 'capture failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.FreezeExtraction(context.Background(), receipt.ID, "worker", FrozenExtraction{Provider: "deepseek", Model: "model"}); err == nil {
		t.Fatal("freeze succeeded after capture failure")
	}
	var count int
	var transcript string
	if err := store.db.QueryRow(`SELECT count(*) FROM extractions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("frozen rows = %d, %v", count, err)
	}
	if err := store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript); err != nil || transcript != "Original input" {
		t.Fatalf("transcript = %q, %v", transcript, err)
	}
}

func TestEvaluationEvidenceExpiryDoesNotRenewOnDuplicateOrLateExtraction(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, time.Hour)
	saveQueueRecording(t, store, "Original input")
	initial := readEvidence(t, store)[0]
	clock.Advance(30 * time.Minute)
	saveQueueRecording(t, store, "Original input")
	if got := readEvidence(t, store)[0]; !got.ExpiresAt.Equal(initial.ExpiresAt) {
		t.Fatal("duplicate renewed expiry")
	}
	clock.Advance(30 * time.Minute)
	if got := readEvidence(t, store); len(got) != 0 {
		t.Fatal("export included expired evidence")
	}
	if deleted, err := store.PurgeExpiredEvaluationEvidence(ctx); err != nil || deleted != 1 {
		t.Fatalf("evidence purge = %d, %v", deleted, err)
	}
	freezeQueueItems(t, store, "worker", nil)
	if got := readEvidence(t, store); len(got) != 0 {
		t.Fatal("late extraction recreated expired evidence")
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM recordings`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("queue rows after evidence expiry = %d, %v", count, err)
	}
}

func TestEvaluationEvidenceExpiryDoesNotRenewAfterRetentionIncrease(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, 24*time.Hour)
	saveQueueRecording(t, store, "Synthetic input awaiting extraction.")
	clock.Advance(24 * time.Hour)
	if deleted, err := store.PurgeExpiredEvaluationEvidence(ctx); err != nil || deleted != 1 {
		t.Fatalf("evidence purge = %d, %v", deleted, err)
	}
	if err := store.ConfigureEvaluationCapture(90 * 24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	freezeQueueItems(t, store, "worker", nil)
	if got := readEvidence(t, store); len(got) != 0 {
		t.Fatal("retention increase recreated expired evidence during extraction")
	}
}

func TestEvaluationEvidenceExpirySurvivesVersionSevenUpgrade(t *testing.T) {
	for _, hasEvidence := range []bool{true, false} {
		name := "absent evidence stays excluded"
		if hasEvidence {
			name = "existing evidence keeps its deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			clock := &adjustableClock{now: time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)}
			path := filepath.Join(t.TempDir(), "version-seven.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if _, err := db.Exec(`CREATE TABLE schema_migrations (
				version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			definitions, err := migrationDefinitions()
			if err != nil {
				t.Fatal(err)
			}
			for _, definition := range definitions {
				if definition.version <= 7 {
					if _, err := db.Exec(string(definition.script)); err != nil {
						t.Fatalf("apply migration %d: %v", definition.version, err)
					}
				}
			}
			received := clock.Time().Format(time.RFC3339Nano)
			for _, definition := range definitions {
				if definition.version <= 7 {
					if _, err := db.Exec(`INSERT INTO schema_migrations
						(version, name, applied_at, checksum) VALUES (?, ?, ?, ?)`,
						definition.version, definition.name, received, definition.checksum); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := db.Exec(`INSERT INTO recordings (
				id, recorded_at_ms, client, transcription, payload_fingerprint,
				first_received_at, last_received_at)
				VALUES (1, ?, 'synthetic', 'Synthetic pending input.', ?, ?, ?)`,
				clock.Time().UnixMilli(), queueTestHash, received, received); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO extraction_jobs
				(recording_id, state, created_at, updated_at) VALUES (1, 'pending', ?, ?)`, received, received); err != nil {
				t.Fatal(err)
			}
			var wantDeadline int64
			if hasEvidence {
				wantDeadline = clock.Time().Add(24 * time.Hour).UnixMilli()
				if _, err := db.Exec(`INSERT INTO evaluation_evidence
					(recording_fingerprint, transcript, recorded_at_ms, captured_at_ms, expires_at_ms)
					VALUES (?, 'Synthetic pending input.', ?, ?, ?)`,
					queueTestHash, clock.Time().UnixMilli(), clock.Time().UnixMilli(), wantDeadline); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := openStore(ctx, path, clock.Time)
			if err != nil {
				t.Fatalf("upgrade version seven: %v", err)
			}
			var deadline int64
			if err := store.db.QueryRow(`SELECT evaluation_expires_at_ms FROM recordings WHERE id = 1`).Scan(&deadline); err != nil {
				t.Fatal(err)
			}
			if deadline != wantDeadline {
				t.Fatalf("migrated deadline = %d, want %d", deadline, wantDeadline)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = openStore(ctx, path, clock.Time)
			if err != nil {
				t.Fatal(err)
			}
			defer ignoreCloseError(store)
			clock.Advance(48 * time.Hour)
			if _, err := store.PurgeExpiredEvaluationEvidence(ctx); err != nil {
				t.Fatal(err)
			}
			if err := store.ConfigureEvaluationCapture(90 * 24 * time.Hour); err != nil {
				t.Fatal(err)
			}
			freezeQueueItems(t, store, "worker", nil)
			if got := readEvidence(t, store); len(got) != 0 {
				t.Fatal("upgrade and retention increase recreated excluded evidence")
			}
		})
	}
}

func TestEvaluationEvidenceObservationsAreAtomicAndOrdered(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, time.Hour)
	saveQueueRecording(t, store, "Move this task")
	freezeQueueItems(t, store, "worker", []QueuedItem{{Kind: ItemKindTask, Title: "Task"}})
	completeEvidenceDelivery(t, store)
	marker, _ := tickTickMarker(queueTestHash, 0)
	observation := EvaluationObservation{TaskID: "task-1", ProjectID: "project-2", Marker: marker, ObservedAt: clock.Time(), Status: "verified"}
	if err := store.SaveEvaluationObservations(ctx, []EvaluationObservation{observation}); err != nil {
		t.Fatal(err)
	}
	older := observation
	older.ObservedAt = observation.ObservedAt.Add(-time.Minute)
	older.ProjectID = "project-1"
	if err := store.SaveEvaluationObservations(ctx, []EvaluationObservation{older}); err != nil {
		t.Fatal(err)
	}
	got := readEvidence(t, store)[0].Deliveries[0]
	if got.ProjectID != "project-1" || !reflect.DeepEqual(got.Observation, &observation) {
		t.Fatalf("original and observed routes = %+v", got)
	}
	newer := observation
	newer.ObservedAt = observation.ObservedAt.Add(time.Minute)
	newer.ProjectID = "project-3"
	bad := EvaluationObservation{TaskID: "task-2", ObservedAt: clock.Time(), Status: "invalid"}
	if err := store.SaveEvaluationObservations(ctx, []EvaluationObservation{newer, bad}); err == nil {
		t.Fatal("invalid batch succeeded")
	}
	if got := readEvidence(t, store)[0].Deliveries[0].Observation; !reflect.DeepEqual(got, &observation) {
		t.Fatal("invalid batch changed stored observation")
	}
	newer.Marker = "wrong-marker"
	if err := store.SaveEvaluationObservations(ctx, []EvaluationObservation{newer}); err == nil {
		t.Fatal("verified observation accepted wrong marker")
	}
	newer.Status, newer.Marker, newer.ProjectID = "missing", "", ""
	if err := store.SaveEvaluationObservations(ctx, []EvaluationObservation{newer}); err != nil {
		t.Fatal(err)
	}
	if got := readEvidence(t, store)[0].Deliveries[0].Observation; got.Status != "missing" {
		t.Fatalf("missing observation = %+v", got)
	}
	clock.Advance(time.Hour)
	if deleted, err := store.PurgeExpiredEvaluationEvidence(ctx); err != nil || deleted != 1 {
		t.Fatalf("purge = %d, %v", deleted, err)
	}
	for _, table := range []string{"evaluation_deliveries", "evaluation_observations"} {
		var count int
		if err := store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s rows after expiry = %d, %v", table, count, err)
		}
	}
}

func TestEvaluationEvidencePreservesObservationChronologyAcrossGaps(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, time.Hour)
	saveQueueRecording(t, store, "Move this task")
	freezeQueueItems(t, store, "worker", []QueuedItem{{Kind: ItemKindTask, Title: "Task"}})
	completeEvidenceDelivery(t, store)
	marker, _ := tickTickMarker(queueTestHash, 0)
	known := "2026-08-12T11:30:00+0000"
	for index, step := range []struct {
		project, modified, status, wantStatus, wantKnown string
	}{
		{"project-2", known, "verified", "verified", known},
		{"", "", "missing", "missing", known},
		{"project-1", "2026-08-12T11:00:00+0000", "verified", "ambiguous", known},
		{"project-1", known, "verified", "ambiguous", known},
		{"project-1", "invalid-time", "verified", "ambiguous", known},
		{"project-2", "", "verified", "verified", known},
		{"project-1", "2026-08-12T11:45:00.123+0000", "verified", "verified", "2026-08-12T11:45:00.123+0000"},
	} {
		clock.Advance(time.Minute)
		observation := EvaluationObservation{TaskID: "task-1", ProjectID: step.project,
			ModifiedAt: step.modified, ObservedAt: clock.Time(), Status: step.status, Marker: marker,
			LastKnownModifiedAt: "2099-01-01T00:00:00Z", LastKnownProjectID: "caller-value"}
		if err := store.SaveEvaluationObservations(ctx, []EvaluationObservation{observation}); err != nil {
			t.Fatalf("step %d: %v", index, err)
		}
		got := readEvidence(t, store)[0].Deliveries[0].Observation
		if got.Status != step.wantStatus || got.LastKnownModifiedAt != step.wantKnown || got.LastKnownProjectID == "caller-value" {
			t.Fatalf("step %d: status=%s watermark=%s project=%s", index, got.Status, got.LastKnownModifiedAt, got.LastKnownProjectID)
		}
	}
}

func TestEvaluationCollectionTargetsExcludePrivateExtractionData(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, time.Hour)
	saveQueueRecording(t, store, "Private original transcript")
	freezeQueueItems(t, store, "worker", []QueuedItem{{Kind: ItemKindTask, Title: "Private title"}})
	completeEvidenceDelivery(t, store)
	marker, _ := tickTickMarker(queueTestHash, 0)
	observation := EvaluationObservation{TaskID: "task-1", ProjectID: "project-2", Marker: marker, ObservedAt: clock.Time(), Status: "verified"}
	if err := store.SaveEvaluationObservations(ctx, []EvaluationObservation{observation}); err != nil {
		t.Fatal(err)
	}
	targets, err := store.ListEvaluationCollectionTargets(ctx)
	if err != nil || len(targets) != 1 || len(targets[0].Deliveries) != 1 {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	if targets[0].Transcript != "" || targets[0].Extraction != nil || targets[0].RecordedAtMillis != 0 || targets[0].Deliveries[0].Title != "" {
		t.Fatal("collection loaded private extraction data")
	}
	if targets[0].Fingerprint != queueTestHash || targets[0].Deliveries[0].TaskID != "task-1" || !reflect.DeepEqual(targets[0].Deliveries[0].Observation, &observation) {
		t.Fatal("collection omitted delivery identity or previous observation")
	}
	clock.Advance(time.Hour)
	if targets, err := store.ListEvaluationCollectionTargets(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("expired targets = %+v, %v", targets, err)
	}
}
