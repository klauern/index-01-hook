package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/application"
)

// TestApplicationFailureAdapterCompatibility preserves root error fields, diagnostics and metadata.
func TestApplicationFailureAdapterCompatibility(t *testing.T) {
	d := &DeepSeekError{Kind: DeepSeekErrorRetryable, Operation: "extract items", StatusCode: 429, Detail: "safe"}
	k := &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create task", StatusCode: 504, Detail: "safe"}
	for _, tt := range []struct {
		err               error
		jsonText, message string
		kind              application.FailureKind
	}{
		{d, `{"Kind":"retryable","Operation":"extract items","StatusCode":429,"Detail":"safe"}`, "deepseek extract items failed: retryable (HTTP 429): safe", application.Retryable},
		{k, `{"Kind":"ambiguous","Operation":"create task","StatusCode":504,"Detail":"safe"}`, "ticktick create task failed: ambiguous (HTTP 504): safe", application.AmbiguousWrite},
	} {
		encoded, e := json.Marshal(tt.err)
		if e != nil || string(encoded) != tt.jsonText || tt.err.Error() != tt.message {
			t.Fatalf("root contract changed: %s %v", encoded, e)
		}
		got, ok := application.FailureOf(fmt.Errorf("fixture: %w", tt.err))
		if !ok || got.Kind != tt.kind || got.Operation == "" || got.StatusCode == 0 {
			t.Fatalf("neutral metadata=%+v found=%v", got, ok)
		}
	}
	private := &DeepSeekError{Kind: DeepSeekErrorMalformed, Operation: "extract items", Detail: "private provider body"}
	got, ok := application.FailureOf(private)
	if !ok || got.Reason != "client_validation" || strings.Contains(fmt.Sprintf("%+v", got), "private") {
		t.Fatal("provider detail escaped through failure port")
	}
}

// TestApplicationFailureWorkerExtraction covers scripted failures without provider error classes.
func TestApplicationFailureWorkerExtraction(t *testing.T) {
	for _, tt := range []struct {
		kind  application.FailureKind
		state string
	}{
		{application.Authentication, "blocked_auth"}, {application.ReviewRequired, "needs_review"}, {application.Malformed, "dead_letter"}, {application.Terminal, "dead_letter"}, {application.Retryable, "retry_wait"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			store, _ := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "synthetic extraction fixture")
			err := fmt.Errorf("fixture wrapper: %w", application.Failure{Kind: tt.kind, Operation: "extract items"})
			worker := newTestWorker(t, store, &fakeExtractor{results: []extractionResult{{err: err}}}, &fakeDeliverer{})
			runWorkerOnce(t, worker)
			status, e := store.RecordingStatus(context.Background(), receipt.ID)
			if e != nil || status.State != tt.state {
				t.Fatalf("state=%q err=%v want %q", status.State, e, tt.state)
			}
		})
	}
}

// TestApplicationFailureWorkerDelivery preserves decisions from neutral delivery failures.
func TestApplicationFailureWorkerDelivery(t *testing.T) {
	for _, tt := range []struct {
		kind  application.FailureKind
		state string
	}{
		{application.Authentication, "blocked_auth"}, {application.Configuration, "needs_review"}, {application.Malformed, "dead_letter"}, {application.Retryable, "retry_wait"}, {application.AmbiguousWrite, "retry_wait"},
	} {
		t.Run(string(tt.kind), func(t *testing.T) {
			store, _ := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "synthetic delivery fixture")
			freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Task"}})
			deliverer := &fakeDeliverer{createResults: []deliveryResult{{err: fmt.Errorf("fixture wrapper: %w", application.Failure{Kind: tt.kind, Operation: "create task"})}}}
			worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
			runWorkerOnce(t, worker)
			status, e := store.RecordingStatus(context.Background(), receipt.ID)
			if e != nil || len(status.Tasks) != 1 || status.Tasks[0].State != tt.state {
				t.Fatalf("status=%+v err=%v want %q", status, e, tt.state)
			}
		})
	}
}

// TestApplicationFailureAmbiguitySurvivesReopen requires reconciliation after a durable restart.
func TestApplicationFailureAmbiguitySurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failure.db")
	clock := &adjustableClock{now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	store, e := openStore(context.Background(), path, clock.Time)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = store.Close() })
	receipt := saveQueueRecording(t, store, "synthetic ambiguous fixture")
	freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Task"}})
	deliverer := &fakeDeliverer{createResults: []deliveryResult{{err: application.Failure{Kind: application.AmbiguousWrite, Operation: "create task"}}}, reconcileResults: []reconciliationResult{{result: TickTickReconciliationResult{Status: TickTickReconciliationConfirmed, TaskID: "confirmed", ProjectID: "fixture"}}}}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
	runWorkerOnce(t, worker)
	status, e := store.RecordingStatus(context.Background(), receipt.ID)
	if e != nil || status.Tasks[0].LastClassification != string(OutcomeAmbiguous) {
		t.Fatalf("ambiguity lost: %+v %v", status, e)
	}
	if e := store.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := openStore(context.Background(), path, clock.Time)
	if e != nil {
		t.Fatal(e)
	}
	store = reopened
	clock.Advance(time.Minute)
	worker = newTestWorker(t, store, &fakeExtractor{}, deliverer)
	runWorkerOnce(t, worker)
	status, e = store.RecordingStatus(context.Background(), receipt.ID)
	if e != nil || status.State != "complete" || len(deliverer.createCalls) != 1 || len(deliverer.reconcileCalls) != 1 || status.Tasks[0].LastClassification != string(OutcomeReconciled) {
		t.Fatalf("restart duplicated or lost work: %+v %v create=%v reconcile=%v", status, e, deliverer.createCalls, deliverer.reconcileCalls)
	}
}

// TestApplicationJoinedFailureAmbiguitySurvivesReopen prevents a second create after a joined ambiguous outcome.
func TestApplicationJoinedFailureAmbiguitySurvivesReopen(t *testing.T) {
	ambiguous := application.Failure{Kind: application.AmbiguousWrite, Operation: "create task"}
	retryable := application.Failure{Kind: application.Retryable, Operation: "create task"}
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"retryable_first", errors.Join(retryable, ambiguous)},
		{"ambiguous_first", errors.Join(ambiguous, retryable)},
		{"wrapped_join", fmt.Errorf("fixture: %w", errors.Join(retryable, ambiguous))},
		{"nested_join", errors.Join(retryable, fmt.Errorf("fixture: %w", errors.Join(errors.New("unrelated"), ambiguous)))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "joined-failure.db")
			clock := &adjustableClock{now: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)}
			store, err := openStore(context.Background(), path, clock.Time)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			receipt := saveQueueRecording(t, store, "synthetic joined outcome")
			freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Task"}})
			deliverer := &fakeDeliverer{
				createResults: []deliveryResult{{err: tt.err}},
				reconcileResults: []reconciliationResult{{result: TickTickReconciliationResult{
					Status: TickTickReconciliationConfirmed, TaskID: "confirmed", ProjectID: "fixture",
				}}},
			}
			runWorkerOnce(t, newTestWorker(t, store, &fakeExtractor{}, deliverer))
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = openStore(context.Background(), path, clock.Time)
			if err != nil {
				t.Fatal(err)
			}
			clock.Advance(time.Minute)
			runWorkerOnce(t, newTestWorker(t, store, &fakeExtractor{}, deliverer))
			status, err := store.RecordingStatus(context.Background(), receipt.ID)
			if err != nil || status.State != "complete" || len(status.Tasks) != 1 ||
				status.Tasks[0].LastClassification != string(OutcomeReconciled) ||
				len(deliverer.createCalls) != 1 || len(deliverer.reconcileCalls) != 1 {
				t.Fatalf("joined outcome was not reconciled after reopen: status=%+v err=%v create=%d reconcile=%d",
					status, err, len(deliverer.createCalls), len(deliverer.reconcileCalls))
			}
		})
	}
}

type opaqueWorkflowFailure struct{ metadata application.Failure }

// Error detects accidental formatting of a transport cause in workflow policy.
func (opaqueWorkflowFailure) Error() string { panic("workflow formatted opaque cause") }

// ApplicationFailure supplies metadata through the consumed port.
func (e opaqueWorkflowFailure) ApplicationFailure() application.Failure { return e.metadata }

// TestApplicationFailureWorkerRedaction checks reason allowlisting and unknown-category fallback.
func TestApplicationFailureWorkerRedaction(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private transcript sentinel")
	err := opaqueWorkflowFailure{application.Failure{Kind: "private-kind", Operation: "private-operation", Reason: "private-reason"}}
	worker := newTestWorker(t, store, &fakeExtractor{results: []extractionResult{{err: err}}}, &fakeDeliverer{})
	var logs strings.Builder
	worker.config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	runWorkerOnce(t, worker)
	status, e := store.RecordingStatus(context.Background(), receipt.ID)
	if e != nil || status.State != "dead_letter" || strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "private transcript") || !strings.Contains(logs.String(), "unexpected_error") {
		t.Fatalf("unsafe fallback/log: state=%q err=%v logs=%s", status.State, e, logs.String())
	}
}

// TestApplicationFailureWorkerBoundary rejects concrete provider error structs in worker policy.
func TestApplicationFailureWorkerBoundary(t *testing.T) {
	tree, e := parser.ParseFile(token.NewFileSet(), "worker.go", nil, 0)
	if e != nil {
		t.Fatal(e)
	}
	ast.Inspect(tree, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && (id.Name == "DeepSeekError" || id.Name == "TickTickError") {
			t.Errorf("worker retains concrete provider error %s", id.Name)
		}
		return true
	})
}
