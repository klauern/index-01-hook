package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// operatorContractRows captures persisted fixture rows for exact before-and-after comparisons.
func operatorContractRows(t *testing.T, store *Store, query string, args ...any) string {
	t.Helper()
	rows, err := store.db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// TestManualRetryContractGuardsAndHistory checks every workflow state, exact IDs, and durable history in SQLite.
func TestManualRetryContractGuardsAndHistory(t *testing.T) {
	for _, domain := range []string{"recording", "delivery"} {
		states := []string{"blocked_auth", "needs_review", "dead_letter", "received", "extracting", "retry_wait", "extracted", "complete"}
		if domain == "delivery" {
			states = []string{"blocked_auth", "needs_review", "dead_letter", "extracted", "creating", "retry_wait", "complete"}
		}
		for _, state := range states {
			t.Run(domain+"/"+state, func(t *testing.T) {
				ctx := context.Background()
				store, clock := configureEvidenceStore(t, 90*24*time.Hour)
				receipt := saveQueueRecording(t, store, "synthetic private retry input")
				table, key, target, retry := "extraction_jobs", "recording_id", receipt.ID, store.RetryRecordingByID
				history, historyKey := "extraction_attempts", "recording_id"
				coarse := map[string]string{"received": "pending", "extracting": "leased", "retry_wait": "retry", "extracted": "frozen", "complete": "completed"}[state]
				if coarse == "" {
					coarse = "review"
				}
				classification := "review"
				marker := ""
				if domain == "delivery" {
					freezeQueueItems(t, store, "retry-contract", []QueuedItem{{Kind: ItemKindTask, Title: "synthetic completed sibling"}, {Kind: ItemKindTask, Title: "synthetic retry target"}})
					completeEvidenceDelivery(t, store)
					table, key, retry = "delivery_tasks", "id", store.RetryDeliveryByID
					history, historyKey = "delivery_attempts", "delivery_task_id"
					if err := store.db.QueryRow("SELECT id,marker FROM delivery_tasks WHERE task_index=1").Scan(&target, &marker); err != nil {
						t.Fatal(err)
					}
					coarse = map[string]string{"extracted": "pending", "creating": "leased", "retry_wait": "retry", "complete": "completed"}[state]
					if coarse == "" {
						coarse = "review"
					}
					if state == "needs_review" {
						classification = "ambiguous"
					}
				} else {
					if _, err := store.SaveRecording(ctx, RecordingInput{RecordedAtMillis: 1, Client: "synthetic", Transcription: "synthetic untouched sibling", Fingerprint: strings.Repeat("c", 64)}); err != nil {
						t.Fatal(err)
					}
				}
				// Seed historical counters and terminal metadata; the public retry performs the tested transition.
				if _, err := store.db.Exec("DELETE FROM "+table+" WHERE "+key+"=?", target); err != nil {
					t.Fatal(err)
				}
				old := timestamp(clock.Time().Add(-time.Hour))
				if domain == "recording" {
					_, err := store.db.Exec(`INSERT INTO extraction_jobs(recording_id,state,workflow_state,attempt_count,cycle_attempt_count,next_attempt_at_ms,last_classification,created_at,updated_at,completed_at,verification_json) VALUES(?,?,?,7,3,?,?,?, ?,?, '[]')`, target, coarse, state, clock.Time().Add(time.Hour).UnixMilli(), classification, old, old, old)
					if err != nil {
						t.Fatal(err)
					}
				} else {
					_, err := store.db.Exec(`INSERT INTO delivery_tasks(id,recording_id,task_index,title,priority,marker,state,workflow_state,attempt_count,cycle_attempt_count,reconcile_attempt_count,next_attempt_at_ms,last_classification,created_at,updated_at,completed_at,ticktick_task_id,ticktick_project_id) VALUES(?,?,1,'synthetic retry target',0,?,?,?,7,3,4,?,?,?, ?,?,'synthetic-remote','synthetic-project')`, target, receipt.ID, marker, coarse, state, clock.Time().Add(time.Hour).UnixMilli(), classification, old, old, old)
					if err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.db.Exec("INSERT INTO "+history+"("+historyKey+",attempt_number,classification,created_at) VALUES(?,7,?,?)", target, classification, old); err != nil {
					t.Fatal(err)
				}
				unchanged := map[string]string{}
				for _, name := range []string{"recordings", "evaluation_evidence", "extractions", "extraction_attempts", "delivery_attempts"} {
					unchanged[name] = operatorContractRows(t, store, "SELECT * FROM "+name+" ORDER BY rowid")
				}
				siblingQuery := "SELECT * FROM " + table + " WHERE " + key + "!=? ORDER BY " + key
				siblings := operatorContractRows(t, store, siblingQuery, target)
				targetQuery := "SELECT * FROM " + table + " WHERE " + key + "=?"
				before := operatorContractRows(t, store, targetQuery, target)
				for _, id := range []int64{-1, 0, target + 9999} {
					if err := retry(ctx, id); !errors.Is(err, ErrManualRetryNotAllowed) {
						t.Fatalf("invalid/missing ID %d: %v", id, err)
					}
				}
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				if err := retry(canceled, target); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled retry: %v", err)
				}
				if got := operatorContractRows(t, store, targetQuery, target); got != before {
					t.Fatal("missing or canceled retry changed target")
				}
				clock.Advance(time.Minute)
				allowed := state == "blocked_auth" || state == "needs_review" || state == "dead_letter"
				err := retry(ctx, target)
				if !allowed {
					if !errors.Is(err, ErrManualRetryNotAllowed) {
						t.Fatalf("state %s retry: %v", state, err)
					}
					if got := operatorContractRows(t, store, targetQuery, target); got != before {
						t.Fatal("rejected retry changed target")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					var gotState, workflow, last, updated string
					var total, cycle int
					var next int64
					var owner, expiry, completed any
					query := "SELECT state,workflow_state,attempt_count,cycle_attempt_count,next_attempt_at_ms,coalesce(last_classification,''),lease_owner,lease_expires_at_ms,completed_at,updated_at FROM " + table + " WHERE " + key + "=?"
					if err := store.db.QueryRow(query, target).Scan(&gotState, &workflow, &total, &cycle, &next, &last, &owner, &expiry, &completed, &updated); err != nil {
						t.Fatal(err)
					}
					wantWorkflow, wantLast := "received", ""
					if domain == "delivery" {
						wantWorkflow = "extracted"
						if classification == "ambiguous" {
							wantLast = "ambiguous"
						}
					}
					if gotState != "pending" || workflow != wantWorkflow || total != 7 || cycle != 0 || next != clock.Time().UnixMilli() || last != wantLast || owner != nil || expiry != nil || completed != nil || updated != timestamp(clock.Time()) {
						t.Fatalf("invalid reset: %s/%s total%d cycle%d next%d last%s owner%v expiry%v completed%v updated%s", gotState, workflow, total, cycle, next, last, owner, expiry, completed, updated)
					}
					if domain == "delivery" {
						var reconciles int
						var gotMarker, remote, project string
						if err := store.db.QueryRow("SELECT reconcile_attempt_count,marker,ticktick_task_id,ticktick_project_id FROM delivery_tasks WHERE id=?", target).Scan(&reconciles, &gotMarker, &remote, &project); err != nil {
							t.Fatal(err)
						}
						if reconciles != 0 || gotMarker != marker || remote != "synthetic-remote" || project != "synthetic-project" {
							t.Fatal("delivery reset lost identity or reconciliation reset")
						}
					}
				}
				if got := operatorContractRows(t, store, siblingQuery, target); got != siblings {
					t.Fatal("exact-ID retry changed sibling")
				}
				for name, before := range unchanged {
					if got := operatorContractRows(t, store, "SELECT * FROM "+name+" ORDER BY rowid"); got != before {
						t.Fatalf("retry changed %s", name)
					}
				}
			})
		}
	}
}

// TestManualRetryContractReconcilesAmbiguousWrite checks scripted provider reconciliation after an exact-ID retry.
func TestManualRetryContractReconcilesAmbiguousWrite(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, 90*24*time.Hour)
	receipt := saveQueueRecording(t, store, "synthetic ambiguous retry evidence")
	initial := readEvidence(t, store)[0]
	freezeQueueItems(t, store, "retry-contract", []QueuedItem{{Kind: ItemKindTask, Title: "synthetic completed sibling"}, {Kind: ItemKindTask, Title: "synthetic ambiguous target"}})
	completeEvidenceDelivery(t, store)
	claim, err := store.ClaimDelivery(ctx, "ambiguous-worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %+v, %v", claim, err)
	}
	if err := store.BlockDelivery(ctx, claim.ID, "ambiguous-worker", OutcomeAmbiguous, "needs_review"); err != nil {
		t.Fatal(err)
	}
	sibling := operatorContractRows(t, store, "SELECT * FROM delivery_tasks WHERE task_index=0")
	clock.Advance(time.Hour)
	if err := store.RetryDeliveryByID(ctx, claim.ID); err != nil {
		t.Fatal(err)
	}
	provider := &fakeDeliverer{reconcileResults: []reconciliationResult{{result: TickTickReconciliationResult{Status: TickTickReconciliationConfirmed, TaskID: "synthetic-existing", ProjectID: "synthetic-project"}}}, createResults: []deliveryResult{{err: errors.New("create must not run")}}}
	worker := newTestWorker(t, store, &fakeExtractor{}, provider)
	if !runWorkerOnce(t, worker) {
		t.Fatal("retried delivery was not processed")
	}
	status, err := store.RecordingStatus(ctx, receipt.ID)
	if err != nil || status.State != "complete" || len(status.Tasks) != 2 {
		t.Fatalf("status: %+v, %v", status, err)
	}
	if len(provider.createCalls) != 0 || len(provider.noteCalls) != 0 || len(provider.reconcileCalls) != 1 || provider.reconcileCalls[0] != claim.Marker {
		t.Fatalf("provider calls: creates%v notes%v reconciles%v", provider.createCalls, provider.noteCalls, provider.reconcileCalls)
	}
	if status.Tasks[1].LastClassification != "reconciled" || status.Tasks[1].TickTickTaskID != "synthetic-existing" || status.Tasks[1].AttemptCount != 2 {
		t.Fatalf("retried task: %+v", status.Tasks[1])
	}
	if got := operatorContractRows(t, store, "SELECT * FROM delivery_tasks WHERE task_index=0"); got != sibling {
		t.Fatal("completed sibling changed")
	}
	if got := readEvidence(t, store); len(got) != 1 || !got[0].ExpiresAt.Equal(initial.ExpiresAt) {
		t.Fatal("retry renewed original evidence expiry")
	}
	var transcript string
	if err := store.db.QueryRow("SELECT transcription FROM recordings WHERE id=?", receipt.ID).Scan(&transcript); err != nil {
		t.Fatal(err)
	}
	if transcript != "" {
		t.Fatal("completed retry retained operational transcript")
	}
}

// TestRetentionContractMixedPrecisionBoundaries checks chronological retention across optional RFC3339Nano fractions.
func TestRetentionContractMixedPrecisionBoundaries(t *testing.T) {
	for _, domain := range []string{"receipt", "extraction", "delivery"} {
		for _, test := range []struct {
			name                 string
			offset, cutoffOffset time.Duration
			want                 int64
		}{
			{"older-whole-second", 0, time.Nanosecond, 1},
			{"newer-fractional-second", time.Nanosecond, 0, 0},
			{"equal-whole-second", 0, 0, 0},
			{"equal-fractional-second", time.Nanosecond, time.Nanosecond, 0},
		} {
			t.Run(domain+"/"+test.name, func(t *testing.T) {
				ctx := context.Background()
				store, clock := newQueueStore(t)
				base := clock.Time()
				offset, cutoffOffset, want := test.offset, test.cutoffOffset, test.want
				input := RecordingInput{RecordedAtMillis: base.UnixMilli(), Client: "synthetic", Fingerprint: strings.Repeat("a", 64)}
				if domain == "receipt" {
					clock.Advance(offset)
					input.AudioByteCount = 1
				} else {
					clock.Advance(-time.Hour)
					input.Transcription = "synthetic retention boundary"
				}
				receipt, err := store.SaveRecording(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				switch domain {
				case "extraction":
					clock.Advance(time.Hour + offset)
					freezeQueueItems(t, store, "retention-contract", nil)
				case "delivery":
					freezeQueueItems(t, store, "retention-contract", []QueuedItem{{Kind: ItemKindTask, Title: "synthetic"}})
					clock.Advance(time.Hour + offset)
					claim, err := store.ClaimDelivery(ctx, "retention-contract", time.Minute)
					if err != nil || claim == nil {
						t.Fatalf("claim: %+v, %v", claim, err)
					}
					if err := store.BlockDelivery(ctx, claim.ID, "retention-contract", OutcomeReview, "needs_review"); err != nil {
						t.Fatal(err)
					}
				}
				clock.Advance(base.Add(terminalRecordRetention + cutoffOffset).Sub(clock.Time()))
				count, err := store.PurgeExpiredRecordings(ctx, terminalRecordRetention)
				if err != nil || count != want {
					t.Fatalf("purge: %d, %v; want %d", count, err, want)
				}
				var remaining int64
				if err := store.db.QueryRow("SELECT count(*) FROM recordings WHERE id=?", receipt.ID).Scan(&remaining); err != nil {
					t.Fatal(err)
				}
				if remaining != 1-want {
					t.Fatalf("remaining %d; want %d", remaining, 1-want)
				}
			})
		}
	}
}

// TestRetentionContractStrictCutoffAndIndependentEvidence checks equality, purge counts, and separate evidence expiry.
func TestRetentionContractStrictCutoffAndIndependentEvidence(t *testing.T) {
	ctx := context.Background()
	store, clock := configureEvidenceStore(t, 90*24*time.Hour)
	base := clock.Time()
	receipt := saveQueueRecording(t, store, "synthetic private retention evidence")
	freezeQueueItems(t, store, "retention-contract", nil)
	initial := readEvidence(t, store)
	if len(initial) != 1 {
		t.Fatalf("evidence count: %d", len(initial))
	}
	clock.Advance(terminalRecordRetention)
	if count, err := store.PurgeExpiredRecordings(ctx, terminalRecordRetention); err != nil || count != 0 {
		t.Fatalf("equal cutoff purge: %d, %v", count, err)
	}
	clock.Advance(time.Nanosecond)
	if count, err := store.PurgeExpiredRecordings(ctx, terminalRecordRetention); err != nil || count != 1 {
		t.Fatalf("past cutoff purge: %d, %v", count, err)
	}
	if count, err := store.PurgeExpiredRecordings(ctx, terminalRecordRetention); err != nil || count != 0 {
		t.Fatalf("repeated purge: %d, %v", count, err)
	}
	retained := readEvidence(t, store)
	if len(retained) != 1 || !retained[0].ExpiresAt.Equal(initial[0].ExpiresAt) || retained[0].Transcript != initial[0].Transcript {
		t.Fatalf("independent evidence changed: %+v", retained)
	}
	for _, table := range []string{"recordings", "extraction_jobs", "extractions", "extraction_attempts"} {
		var count int
		query := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s=?", table, "recording_id")
		if table == "recordings" {
			query = "SELECT count(*) FROM recordings WHERE id=?"
		}
		if err := store.db.QueryRow(query, receipt.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows", table, count)
		}
	}
	clock.Advance(initial[0].ExpiresAt.Sub(clock.Time()))
	if !clock.Time().Equal(base.Add(90 * 24 * time.Hour)) {
		t.Fatal("fixture expiry changed")
	}
	if count, err := store.PurgeExpiredEvaluationEvidence(ctx); err != nil || count != 1 {
		t.Fatalf("evidence equality purge: %d, %v", count, err)
	}
}
