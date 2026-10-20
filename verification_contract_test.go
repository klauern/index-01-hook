package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestActiveVerificationEvidenceLeaseAndLifecycle checks active evidence persistence across lease and completion transitions.
func TestActiveVerificationEvidenceLeaseAndLifecycle(t *testing.T) {
	ctx := context.Background()
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "synthetic private transcript")
	claim, err := store.ClaimExtraction(ctx, "first", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	evidence := []TypeSafeVerificationEvidence{
		{Model: "fixture-model", PromptVersion: "fixture-v1", Decision: VerificationAccept},
		{Model: "fixture-model", PromptVersion: "fixture-v1", Decision: VerificationReject, Reason: "fixture-rejection"},
	}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", claim.AttemptNumber, evidence); err != nil {
		t.Fatal(err)
	}
	assertEvidence := func() {
		t.Helper()
		status, err := store.RecordingStatus(ctx, receipt.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(status.Verification, evidence) {
			t.Fatalf("evidence changed: %+v", status.Verification)
		}
	}
	assertEvidence()
	for _, owner := range []string{"wrong", ""} {
		if err := store.SaveExtractionVerification(ctx, receipt.ID, owner, claim.AttemptNumber, evidence[:1]); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("owner %q: %v", owner, err)
		}
		assertEvidence()
	}
	score := math.NaN()
	invalid := []TypeSafeVerificationEvidence{{Answers: map[string]TypeSafeAnswer{"invalid": {Score: &score}}}}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", claim.AttemptNumber, invalid); err == nil {
		t.Fatal("invalid evidence encoded")
	}
	assertEvidence()
	clock.Advance(time.Minute)
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", claim.AttemptNumber, evidence[:1]); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired lease: %v", err)
	}
	assertEvidence()
	if err := store.CompleteRejectedExtraction(ctx, receipt.ID, "first", claim.AttemptNumber); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired transition: %v", err)
	}
	assertEvidence()
	claim, err = store.ClaimExtraction(ctx, "second", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("reclaim: %v", err)
	}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", claim.AttemptNumber, evidence[:1]); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale owner: %v", err)
	}
	assertEvidence()
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "second", claim.AttemptNumber, evidence); err != nil {
		t.Fatal(err)
	}
	var extracted, delivered int
	if err := store.db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM extractions), (SELECT count(*) FROM delivery_tasks)").Scan(&extracted, &delivered); err != nil {
		t.Fatal(err)
	}
	if extracted != 0 || delivered != 0 {
		t.Fatalf("evidence write caused effects: %d/%d", extracted, delivered)
	}
	if err := store.CompleteRejectedExtraction(ctx, receipt.ID, "second", claim.AttemptNumber); err != nil {
		t.Fatal(err)
	}
	assertEvidence()
	var transcript string
	if err := store.db.QueryRowContext(ctx, "SELECT transcription FROM recordings WHERE id = ?", receipt.ID).Scan(&transcript); err != nil {
		t.Fatal(err)
	}
	if transcript != "" {
		t.Fatal("rejected completion retained operational transcript")
	}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "second", claim.AttemptNumber, evidence[:1]); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("completed lease: %v", err)
	}
	assertEvidence()
}

// TestActiveVerificationEmptyEvidenceIsNoOp checks the empty-evidence no-op before context and lease validation.
func TestActiveVerificationEmptyEvidenceIsNoOp(t *testing.T) {
	store, _ := newQueueStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.SaveExtractionVerification(ctx, -1, "", 0, nil); err != nil {
		t.Fatalf("empty evidence is not a no-op: %v", err)
	}
}

// TestOperationalAggregateReadsExcludeVerificationEvidenceAndDoNotWrite checks aggregate privacy and absence of database writes.
func TestOperationalAggregateReadsExcludeVerificationEvidenceAndDoNotWrite(t *testing.T) {
	ctx := context.Background()
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "synthetic-private-transcript-sentinel")
	claim, err := store.ClaimExtraction(ctx, "reader-fixture", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	evidence := []TypeSafeVerificationEvidence{{Model: "synthetic-private-model-sentinel", Decision: VerificationReview, Reason: "synthetic-private-evidence-sentinel"}}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "reader-fixture", claim.AttemptNumber, evidence); err != nil {
		t.Fatal(err)
	}
	startHealthyWorker(t, store, "reader-fixture")
	var before, after int
	if err := store.db.QueryRowContext(ctx, "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, "synthetic-token", 1<<20, nil)
	for range 2 {
		snapshot, err := store.OperationalStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/statusz", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("aggregate response status %d", response.Code)
		}
		for _, output := range []string{string(encoded), response.Body.String()} {
			if strings.Contains(output, "synthetic-private-") || strings.Contains(output, "verification") || strings.Contains(output, "transcription") {
				t.Fatal("aggregate exposed private evidence")
			}
		}
	}
	if err := store.db.QueryRowContext(ctx, "SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("aggregate read changed database: %d -> %d", before, after)
	}
}

// TestExtractionClaimAttemptFencesSameOwnerReclaim rejects every stale extraction write after same-owner renewal.
func TestExtractionClaimAttemptFencesSameOwnerReclaim(t *testing.T) {
	for _, operation := range []string{"evidence", "freeze", "retry", "review", "block", "reject"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			store, clock := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "synthetic fenced transcript")
			first, err := store.ClaimExtraction(ctx, "same-owner", time.Minute)
			if err != nil || first == nil {
				t.Fatalf("first claim = %v, %v", first, err)
			}
			clock.Advance(time.Minute)
			second, err := store.ClaimExtraction(ctx, "same-owner", time.Minute)
			if err != nil || second == nil || second.AttemptNumber <= first.AttemptNumber {
				t.Fatalf("renewed claim = %v, %v", second, err)
			}
			fresh := []TypeSafeVerificationEvidence{{Model: "current-claim", Decision: VerificationAccept}}
			if err := store.SaveExtractionVerification(ctx, receipt.ID, "same-owner", second.AttemptNumber, fresh); err != nil {
				t.Fatal(err)
			}
			snapshot := func() any {
				t.Helper()
				status, err := store.RecordingStatus(ctx, receipt.ID)
				if err != nil {
					t.Fatal(err)
				}
				var transcript string
				if err := store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript); err != nil {
					t.Fatal(err)
				}
				var counts [3]int
				if err := store.db.QueryRow(`SELECT (SELECT count(*) FROM extractions), (SELECT count(*) FROM delivery_tasks), (SELECT count(*) FROM extraction_attempts)`).Scan(&counts[0], &counts[1], &counts[2]); err != nil {
					t.Fatal(err)
				}
				return struct {
					Status     RecordingQueueStatus
					Transcript string
					Counts     [3]int
				}{status, transcript, counts}
			}
			apply := func(attempt int) error {
				switch operation {
				case "evidence":
					return store.SaveExtractionVerification(ctx, receipt.ID, "same-owner", attempt, []TypeSafeVerificationEvidence{{Model: "replacement", Decision: VerificationReject}})
				case "freeze":
					return store.FreezeExtraction(ctx, receipt.ID, "same-owner", attempt, FrozenExtraction{Provider: "fixture", Model: "fixture"})
				case "retry":
					return store.RetryExtraction(ctx, receipt.ID, "same-owner", attempt, OutcomeRetryable, clock.Time().Add(time.Minute))
				case "review":
					return store.ReviewExtraction(ctx, receipt.ID, "same-owner", attempt, OutcomeReview)
				case "block":
					return store.BlockExtraction(ctx, receipt.ID, "same-owner", attempt, OutcomeMalformed, "dead_letter")
				default:
					return store.CompleteRejectedExtraction(ctx, receipt.ID, "same-owner", attempt)
				}
			}
			before := snapshot()
			if err := apply(first.AttemptNumber); !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("stale %s = %v, want lease loss", operation, err)
			}
			if after := snapshot(); !reflect.DeepEqual(before, after) {
				t.Fatalf("stale %s mutated persisted state", operation)
			}
			if err := apply(second.AttemptNumber); err != nil {
				t.Fatalf("current %s = %v", operation, err)
			}
		})
	}
}

// TestWorkerExtractionCarriesClaimAttempt checks that stale worker results cannot freeze a renewed same-owner claim.
func TestWorkerExtractionCarriesClaimAttempt(t *testing.T) {
	ctx := context.Background()
	store, clock := newQueueStore(t)
	saveQueueRecording(t, store, "synthetic worker fencing")
	fixture := extractionResult{value: FrozenExtraction{Provider: "fixture", Model: "fixture"}}
	worker := newTestWorker(t, store, &fakeExtractor{results: []extractionResult{fixture, fixture}}, &fakeDeliverer{})
	first, err := store.ClaimExtraction(ctx, worker.config.Owner, worker.config.LeaseDuration)
	if err != nil || first == nil {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	clock.Advance(worker.config.LeaseDuration)
	second, err := store.ClaimExtraction(ctx, worker.config.Owner, worker.config.LeaseDuration)
	if err != nil || second == nil {
		t.Fatalf("second claim = %v, %v", second, err)
	}
	if err := worker.processExtraction(ctx, first); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker = %v", err)
	}
	if err := worker.processExtraction(ctx, second); err != nil {
		t.Fatalf("current worker = %v", err)
	}
	status, err := store.RecordingStatus(ctx, second.RecordingID)
	if err != nil || status.State != "complete" {
		t.Fatalf("current zero-item worker state = %+v, %v, want complete", status, err)
	}
	var extractions, attempts, completedAttempt int
	if err := store.db.QueryRow(`SELECT (SELECT count(*) FROM extractions), count(*), coalesce(max(attempt_number), 0) FROM extraction_attempts`).Scan(&extractions, &attempts, &completedAttempt); err != nil {
		t.Fatal(err)
	}
	if extractions != 1 || attempts != 1 || completedAttempt != second.AttemptNumber {
		t.Fatalf("current freeze counts = extractions:%d attempts:%d attempt:%d", extractions, attempts, completedAttempt)
	}
}

// TestExtractionFenceSurvivesOperatorRetry checks that cycle resets cannot revive an old claim fence.
func TestExtractionFenceSurvivesOperatorRetry(t *testing.T) {
	ctx := context.Background()
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "synthetic operator retry")
	first, err := store.ClaimExtraction(ctx, "same-owner", time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first claim = %v, %v", first, err)
	}
	if err := store.ReviewExtraction(ctx, receipt.ID, "same-owner", first.AttemptNumber, OutcomeReview); err != nil {
		t.Fatal(err)
	}
	if err := store.RetryRecordingByID(ctx, receipt.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimExtraction(ctx, "same-owner", time.Minute)
	if err != nil || second == nil || second.AttemptNumber <= first.AttemptNumber || second.CycleAttemptNumber != 1 {
		t.Fatalf("retry claim = %v, %v", second, err)
	}
	evidence := []TypeSafeVerificationEvidence{{Model: "fixture", Decision: VerificationAccept}}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "same-owner", first.AttemptNumber, evidence); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old cycle fence = %v", err)
	}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "same-owner", second.AttemptNumber, evidence); err != nil {
		t.Fatal(err)
	}
}
