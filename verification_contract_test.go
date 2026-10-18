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
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", evidence); err != nil {
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
		if err := store.SaveExtractionVerification(ctx, receipt.ID, owner, evidence[:1]); !errors.Is(err, ErrLeaseLost) {
			t.Fatalf("owner %q: %v", owner, err)
		}
		assertEvidence()
	}
	score := math.NaN()
	invalid := []TypeSafeVerificationEvidence{{Answers: map[string]TypeSafeAnswer{"invalid": {Score: &score}}}}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", invalid); err == nil {
		t.Fatal("invalid evidence encoded")
	}
	assertEvidence()
	clock.Advance(time.Minute)
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", evidence[:1]); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired lease: %v", err)
	}
	assertEvidence()
	if err := store.CompleteRejectedExtraction(ctx, receipt.ID, "first"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired transition: %v", err)
	}
	assertEvidence()
	claim, err = store.ClaimExtraction(ctx, "second", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("reclaim: %v", err)
	}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "first", evidence[:1]); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale owner: %v", err)
	}
	assertEvidence()
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "second", evidence); err != nil {
		t.Fatal(err)
	}
	var extracted, delivered int
	if err := store.db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM extractions), (SELECT count(*) FROM delivery_tasks)").Scan(&extracted, &delivered); err != nil {
		t.Fatal(err)
	}
	if extracted != 0 || delivered != 0 {
		t.Fatalf("evidence write caused effects: %d/%d", extracted, delivered)
	}
	if err := store.CompleteRejectedExtraction(ctx, receipt.ID, "second"); err != nil {
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
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "second", evidence[:1]); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("completed lease: %v", err)
	}
	assertEvidence()
}

func TestActiveVerificationEmptyEvidenceIsNoOp(t *testing.T) {
	store, _ := newQueueStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.SaveExtractionVerification(ctx, -1, "", nil); err != nil {
		t.Fatalf("empty evidence is not a no-op: %v", err)
	}
}

func TestOperationalAggregateReadsExcludeVerificationEvidenceAndDoNotWrite(t *testing.T) {
	ctx := context.Background()
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "synthetic-private-transcript-sentinel")
	claim, err := store.ClaimExtraction(ctx, "reader-fixture", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("claim: %v", err)
	}
	evidence := []TypeSafeVerificationEvidence{{Model: "synthetic-private-model-sentinel", Decision: VerificationReview, Reason: "synthetic-private-evidence-sentinel"}}
	if err := store.SaveExtractionVerification(ctx, receipt.ID, "reader-fixture", evidence); err != nil {
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
