package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

type extractionResult struct {
	value FrozenExtraction
	err   error
}

type fakeExtractor struct {
	results []extractionResult
	calls   int
}

func (provider *fakeExtractor) Extract(context.Context, string, []string) (FrozenExtraction, error) {
	provider.calls++
	index := provider.calls - 1
	if index >= len(provider.results) {
		index = len(provider.results) - 1
	}
	return provider.results[index].value, provider.results[index].err
}

type deliveryResult struct {
	created TickTickCreatedTask
	err     error
}

type reconciliationResult struct {
	result TickTickReconciliationResult
	err    error
}

type fakeDeliverer struct {
	createResults    []deliveryResult
	noteResults      []deliveryResult
	reconcileResults []reconciliationResult
	createCalls      []int
	noteCalls        []int
	taskInputs       []TickTickTaskInput
	noteInputs       []TickTickNoteInput
	taskAliases      []string
	reconcileCalls   []string
	reconcileKinds   []ItemKind
	reconcileAliases []string
	reconcileInputs  []TickTickReconciliationInput
}

func (provider *fakeDeliverer) CreateTask(_ context.Context, alias string, input TickTickTaskInput) (TickTickCreatedTask, error) {
	provider.createCalls = append(provider.createCalls, input.TaskIndex)
	provider.taskInputs = append(provider.taskInputs, input)
	provider.taskAliases = append(provider.taskAliases, alias)
	index := len(provider.createCalls) - 1
	if index >= len(provider.createResults) {
		index = len(provider.createResults) - 1
	}
	return provider.createResults[index].created, provider.createResults[index].err
}

func (provider *fakeDeliverer) CreateNote(_ context.Context, input TickTickNoteInput) (TickTickCreatedTask, error) {
	provider.noteCalls = append(provider.noteCalls, input.TaskIndex)
	provider.noteInputs = append(provider.noteInputs, input)
	index := len(provider.noteCalls) - 1
	if index >= len(provider.noteResults) {
		index = len(provider.noteResults) - 1
	}
	return provider.noteResults[index].created, provider.noteResults[index].err
}

func (provider *fakeDeliverer) ReconcileItem(_ context.Context, input TickTickReconciliationInput) (TickTickReconciliationResult, error) {
	provider.reconcileCalls = append(provider.reconcileCalls, input.Marker)
	provider.reconcileKinds = append(provider.reconcileKinds, input.Kind)
	provider.reconcileAliases = append(provider.reconcileAliases, input.ProjectAlias)
	provider.reconcileInputs = append(provider.reconcileInputs, input)
	index := len(provider.reconcileCalls) - 1
	if index >= len(provider.reconcileResults) {
		index = len(provider.reconcileResults) - 1
	}
	return provider.reconcileResults[index].result, provider.reconcileResults[index].err
}

func newTestWorker(t *testing.T, store *Store, extractor ExtractionProvider, deliverer DeliveryProvider) *Worker {
	return newTestWorkerWithTimeZone(t, store, extractor, deliverer, "")
}

func newTestWorkerWithTimeZone(t *testing.T, store *Store, extractor ExtractionProvider, deliverer DeliveryProvider, timeZone string) *Worker {
	t.Helper()
	worker, err := NewWorker(store, extractor, deliverer, WorkerConfig{
		Owner: "test-worker", TimeZone: timeZone,
		LeaseDuration: time.Minute, PollInterval: time.Millisecond,
		RetryBase: time.Minute, RetryMaximum: 8 * time.Minute,
		ExtractionMaxAttempts: 3, DeliveryMaxAttempts: 3, ReconcileMaxAttempts: 2,
		ProjectAliases: []string{"work"}, Jitter: func(time.Duration) time.Duration { return 0 },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	return worker
}

func runWorkerOnce(t *testing.T, worker *Worker) bool {
	t.Helper()
	worked, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	return worked
}

func TestNewWorkerValidatesTimeZone(t *testing.T) {
	tests := []struct {
		name       string
		timeZone   string
		wantError  bool
		wantStored string
	}{
		{name: "blank defaults to UTC", wantStored: "UTC"},
		{name: "trimmed custom zone", timeZone: "  America/Los_Angeles  ", wantStored: "America/Los_Angeles"},
		{name: "local zone", timeZone: "Local", wantError: true},
		{name: "unknown zone", timeZone: "Mars/Olympus", wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newQueueStore(t)
			config := WorkerConfig{
				Owner: "test-worker", TimeZone: test.timeZone,
				LeaseDuration: time.Minute, PollInterval: time.Millisecond,
				RetryBase: time.Minute, RetryMaximum: 8 * time.Minute,
				ExtractionMaxAttempts: 3, DeliveryMaxAttempts: 3, ReconcileMaxAttempts: 2,
				ProjectAliases: []string{"work"}, Jitter: func(time.Duration) time.Duration { return 0 },
				Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			worker, err := NewWorker(store, &fakeExtractor{}, &fakeDeliverer{}, config)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "time zone") {
					t.Fatalf("NewWorker() error = %v, want time-zone validation error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewWorker() error = %v", err)
			}
			if worker.config.TimeZone != test.wantStored {
				t.Fatalf("stored time zone = %q, want %q", worker.config.TimeZone, test.wantStored)
			}
		})
	}
}

func TestWorkerUsesConfiguredTimeZoneForTaskDelivery(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "Task with custom time zone")
	due := time.Date(2026, time.August, 12, 9, 0, 0, 0, time.UTC)
	extractor := &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{
		Provider: "deepseek", Model: "deepseek-custom-v1", Items: []QueuedItem{{
			Kind: ItemKindTask, Title: "Task", Due: &due, AllDay: false,
		}},
	}}}}
	deliverer := &fakeDeliverer{createResults: []deliveryResult{{created: TickTickCreatedTask{ID: "task-one", ProjectID: "project"}}}}
	worker := newTestWorkerWithTimeZone(t, store, extractor, deliverer, "America/Los_Angeles")
	runWorkerOnce(t, worker)
	runWorkerOnce(t, worker)
	if len(deliverer.taskInputs) != 1 || deliverer.taskInputs[0].TimeZone != "America/Los_Angeles" {
		t.Fatalf("task inputs = %+v, want configured time zone", deliverer.taskInputs)
	}
	recorded, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if recorded.Model != "deepseek-custom-v1" {
		t.Fatalf("RecordingStatus().Model = %q, want deepseek-custom-v1", recorded.Model)
	}
}

func TestWorkerPersistsConfiguredModelInRecordingStatus(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "Custom model status")
	worker := newTestWorker(t, store, &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{
		Provider: "deepseek", Model: "deepseek-custom-v1",
	}}}}, &fakeDeliverer{})
	runWorkerOnce(t, worker)
	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.Model != "deepseek-custom-v1" {
		t.Fatalf("RecordingStatus().Model = %q, want deepseek-custom-v1", status.Model)
	}
}

func TestWorkerTransitionTablesRejectIllegalChanges(t *testing.T) {
	store, _ := newQueueStore(t)
	extractionStates := []string{"received", "extracting", "extracted", "retry_wait", "blocked_auth", "needs_review", "dead_letter", "complete"}
	deliveryStates := []string{"extracted", "creating", "retry_wait", "blocked_auth", "needs_review", "dead_letter", "complete"}
	assertTransitionMatrix(t, store, "extraction", extractionStates)
	assertTransitionMatrix(t, store, "delivery", deliveryStates)
}

func assertTransitionMatrix(t *testing.T, store *Store, domain string, states []string) {
	t.Helper()
	baseID := 10000
	if domain == "delivery" {
		baseID = 20000
	}
	for fromIndex, from := range states {
		for toIndex, to := range states {
			expected := transitionAllowed(domain, from, to)
			recordingID := int64(baseID + fromIndex*100 + toIndex)
			hash := strings.Repeat("c", 60) + fmt.Sprintf("%04x", recordingID)
			_, err := store.db.Exec(`
				INSERT INTO recordings (
					id, recorded_at_ms, client, transcription, payload_fingerprint,
					first_received_at, last_received_at
				) VALUES (?, ?, 'test', '', ?, 'now', 'now')`, recordingID, recordingID, hash)
			if err != nil {
				t.Fatalf("insert transition recording: %v", err)
			}
			if domain == "extraction" {
				_, err = store.db.Exec(`
					INSERT INTO extraction_jobs (
						recording_id, state, next_attempt_at_ms, created_at, updated_at, workflow_state
					) VALUES (?, 'pending', 0, 'now', 'now', ?)`, recordingID, from)
				if err == nil {
					_, err = store.db.Exec(`UPDATE extraction_jobs SET workflow_state = ? WHERE recording_id = ?`, to, recordingID)
				}
			} else {
				_, err = store.db.Exec(`
					INSERT INTO delivery_tasks (
						recording_id, task_index, title, priority, marker, state,
						next_attempt_at_ms, created_at, updated_at, workflow_state
					) VALUES (?, 0, 'test', 0, ?, 'pending', 0, 'now', 'now', ?)`,
					recordingID, "[index01:"+hash+":0]", from)
				if err == nil {
					_, err = store.db.Exec(`UPDATE delivery_tasks SET workflow_state = ? WHERE recording_id = ?`, to, recordingID)
				}
			}
			if (err == nil) != expected {
				t.Errorf("%s transition %s to %s error = %v, allowed = %v", domain, from, to, err, expected)
			}
		}
	}
}

func transitionAllowed(domain, from, to string) bool {
	if from == to {
		return true
	}
	allowed := map[string]map[string]bool{
		"extraction": {
			"received>extracting": true, "retry_wait>extracting": true,
			"extracting>extracted": true, "extracting>retry_wait": true,
			"extracting>blocked_auth": true, "extracting>needs_review": true,
			"extracting>dead_letter": true, "extracting>complete": true,
			"blocked_auth>received": true, "needs_review>received": true,
			"dead_letter>received": true, "extracted>complete": true,
		},
		"delivery": {
			"extracted>creating": true, "retry_wait>creating": true,
			"creating>retry_wait": true, "creating>blocked_auth": true,
			"creating>needs_review": true, "creating>dead_letter": true,
			"creating>complete": true, "blocked_auth>extracted": true,
			"needs_review>extracted": true, "dead_letter>extracted": true,
		},
	}
	return allowed[domain][from+">"+to]
}

func TestWorkerCompletesZeroTaskExtraction(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "No action")
	extractor := &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{
		Provider: "deepseek", Model: deepSeekModel, ProviderResponseID: "response-zero",
	}}}}
	deliverer := &fakeDeliverer{}
	worker := newTestWorker(t, store, extractor, deliverer)
	if !runWorkerOnce(t, worker) {
		t.Fatal("RunOnce() did no work")
	}
	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.State != "complete" || len(status.Tasks) != 0 || len(deliverer.createCalls) != 0 {
		t.Fatalf("zero-task status = %+v, create calls = %v", status, deliverer.createCalls)
	}
}

func TestWorkerLegacyTaskBatchRetryKeepsCompletedSibling(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "Create two tasks")
	extractor := &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{
		Provider: "deepseek", Model: deepSeekModel,
		Tasks: []QueuedTask{{Title: "First", Priority: 0}, {Title: "Second", Priority: 3}},
	}}}}
	deliverer := &fakeDeliverer{createResults: []deliveryResult{
		{created: TickTickCreatedTask{ID: "task-one", ProjectID: "project"}},
		{err: &TickTickError{Kind: TickTickErrorRetryable, Operation: "create task"}},
		{created: TickTickCreatedTask{ID: "task-two", ProjectID: "project"}},
	}}
	worker := newTestWorker(t, store, extractor, deliverer)
	runWorkerOnce(t, worker)
	runWorkerOnce(t, worker)
	runWorkerOnce(t, worker)
	clock.Advance(time.Minute)
	runWorkerOnce(t, worker)
	status, _ := store.RecordingStatus(context.Background(), receipt.ID)
	if status.State != "complete" || len(deliverer.createCalls) != 3 || deliverer.createCalls[0] != 0 || deliverer.createCalls[1] != 1 || deliverer.createCalls[2] != 1 ||
		len(deliverer.noteCalls) != 0 || status.Tasks[0].Kind != ItemKindTask || status.Tasks[1].Kind != ItemKindTask {
		t.Fatalf("legacy status = %+v, task calls = %v, note calls = %v", status, deliverer.createCalls, deliverer.noteCalls)
	}
}

func TestWorkerCreatesMixedTaskAndNoteItems(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private mixed transcript")
	extractor := &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{
		Provider: "deepseek", Model: deepSeekModel,
		Items: []QueuedItem{
			{Kind: ItemKindTask, Title: "Task", Content: "Task content", Priority: 0, ProjectAlias: "work"},
			{Kind: ItemKindNote, Title: "Note", Content: "Note content"},
		},
	}}}}
	deliverer := &fakeDeliverer{createResults: []deliveryResult{{
		created: TickTickCreatedTask{ID: "task-one", ProjectID: "project"},
	}}, noteResults: []deliveryResult{{
		created: TickTickCreatedTask{ID: "note-one", ProjectID: "note-project"},
	}}}
	worker := newTestWorker(t, store, extractor, deliverer)
	for cycle := 1; cycle <= 3; cycle++ {
		if !runWorkerOnce(t, worker) {
			t.Fatalf("worker stopped during mixed batch cycle %d", cycle)
		}
	}
	if runWorkerOnce(t, worker) {
		t.Fatal("worker found unexpected work")
	}
	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.State != "complete" || len(status.Tasks) != 2 ||
		status.Tasks[0].Kind != ItemKindTask || status.Tasks[0].State != "complete" ||
		status.Tasks[1].Kind != ItemKindNote || status.Tasks[1].State != "complete" ||
		len(deliverer.createCalls) != 1 || deliverer.createCalls[0] != 0 ||
		len(deliverer.noteCalls) != 1 || deliverer.noteCalls[0] != 1 {
		t.Fatalf("mixed status = %+v, task calls = %v, note calls = %v", status, deliverer.createCalls, deliverer.noteCalls)
	}
	if deliverer.taskAliases[0] != "work" || deliverer.taskInputs[0].Content != "Task content" ||
		deliverer.noteInputs[0].Content != "Note content" {
		t.Fatalf("mixed inputs = tasks:%+v notes:%+v", deliverer.taskInputs, deliverer.noteInputs)
	}
	for index, item := range status.Tasks {
		want, err := tickTickMarker(queueTestHash, index)
		if err != nil {
			t.Fatalf("build marker: %v", err)
		}
		if item.TaskIndex != index || item.Marker != want {
			t.Errorf("item %d = index:%d marker:%q", index, item.TaskIndex, item.Marker)
		}
	}
}

func TestWorkerRetriesEachMixedItemWithoutRecreatingItsSibling(t *testing.T) {
	tests := []struct {
		name          string
		items         []QueuedItem
		createResults []deliveryResult
		noteResults   []deliveryResult
		wantTaskCalls []int
		wantNoteCalls []int
	}{
		{
			name: "note fails after task",
			items: []QueuedItem{
				{Kind: ItemKindTask, Title: "Task", Priority: 0},
				{Kind: ItemKindNote, Title: "Note", Content: "Note content"},
			},
			createResults: []deliveryResult{{created: TickTickCreatedTask{ID: "task-one", ProjectID: "project"}}},
			noteResults: []deliveryResult{
				{err: &TickTickError{Kind: TickTickErrorRetryable, Operation: "create note"}},
				{created: TickTickCreatedTask{ID: "note-one", ProjectID: "note-project"}},
			},
			wantTaskCalls: []int{0}, wantNoteCalls: []int{1, 1},
		},
		{
			name: "task fails after note",
			items: []QueuedItem{
				{Kind: ItemKindNote, Title: "Note", Content: "Note content"},
				{Kind: ItemKindTask, Title: "Task", Priority: 0},
			},
			createResults: []deliveryResult{
				{err: &TickTickError{Kind: TickTickErrorRetryable, Operation: "create task"}},
				{created: TickTickCreatedTask{ID: "task-one", ProjectID: "project"}},
			},
			noteResults:   []deliveryResult{{created: TickTickCreatedTask{ID: "note-one", ProjectID: "note-project"}}},
			wantTaskCalls: []int{1, 1}, wantNoteCalls: []int{0},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "private mixed transcript")
			extractor := &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{
				Provider: "deepseek", Model: deepSeekModel, Items: test.items,
			}}}}
			deliverer := &fakeDeliverer{createResults: test.createResults, noteResults: test.noteResults}
			worker := newTestWorker(t, store, extractor, deliverer)
			runWorkerOnce(t, worker)
			runWorkerOnce(t, worker)
			runWorkerOnce(t, worker)

			status, err := store.RecordingStatus(context.Background(), receipt.ID)
			if err != nil {
				t.Fatalf("RecordingStatus() error = %v", err)
			}
			if status.State != "extracted" || status.Tasks[0].State != "complete" || status.Tasks[1].State != "retry_wait" {
				t.Fatalf("partial status = %+v", status)
			}
			var transcript string
			if err := store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript); err != nil {
				t.Fatalf("query transcript: %v", err)
			}
			if transcript != "private mixed transcript" {
				t.Fatalf("partial transcript = %q", transcript)
			}

			clock.Advance(time.Minute)
			runWorkerOnce(t, worker)
			status, _ = store.RecordingStatus(context.Background(), receipt.ID)
			if status.State != "complete" || status.Tasks[0].State != "complete" || status.Tasks[1].State != "complete" {
				t.Fatalf("final status = %+v", status)
			}
			if fmt.Sprint(deliverer.createCalls) != fmt.Sprint(test.wantTaskCalls) ||
				fmt.Sprint(deliverer.noteCalls) != fmt.Sprint(test.wantNoteCalls) {
				t.Fatalf("calls = tasks:%v notes:%v", deliverer.createCalls, deliverer.noteCalls)
			}
			if err := store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript); err != nil {
				t.Fatalf("query completed transcript: %v", err)
			}
			if transcript != "" {
				t.Fatalf("completed transcript = %q", transcript)
			}
		})
	}
}

func TestWorkerRecoversExpiredCreateLeaseByReconciliation(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "Crash after create")
	freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Only", Priority: 0}})
	claim, err := store.ClaimDelivery(context.Background(), "crashed-worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimDelivery() = %+v, %v", claim, err)
	}
	clock.Advance(time.Minute + time.Millisecond)
	deliverer := &fakeDeliverer{
		reconcileResults: []reconciliationResult{{result: TickTickReconciliationResult{
			Status: TickTickReconciliationConfirmed, TaskID: "existing-task", ProjectID: "project",
		}}},
		createResults: []deliveryResult{{err: errors.New("create must not run")}},
	}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
	runWorkerOnce(t, worker)
	status, _ := store.RecordingStatus(context.Background(), receipt.ID)
	if status.State != "complete" || len(deliverer.createCalls) != 0 || len(deliverer.reconcileCalls) != 1 || status.Tasks[0].TickTickTaskID != "existing-task" {
		t.Fatalf("restart status = %+v, creates = %v, reconciles = %v", status, deliverer.createCalls, deliverer.reconcileCalls)
	}
}

func TestWorkerRestartsOnlyTheUnfinishedNote(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private restart transcript")
	freezeQueueItems(t, store, "extractor", []QueuedItem{
		{Kind: ItemKindTask, Title: "Task", Priority: 0},
		{Kind: ItemKindNote, Title: "Note", Content: "Note content"},
	})
	task, err := store.ClaimDelivery(context.Background(), "task-worker", time.Minute)
	if err != nil || task == nil || task.Kind != ItemKindTask {
		t.Fatalf("task claim = %+v, %v", task, err)
	}
	if err := store.CompleteDelivery(context.Background(), DeliveryCompletion{
		TaskID: task.ID, LeaseOwner: "task-worker", Classification: OutcomeCreated,
		TickTickTaskID: "task-one", TickTickProjectID: "project",
	}); err != nil {
		t.Fatalf("CompleteDelivery(task) error = %v", err)
	}
	note, err := store.ClaimDelivery(context.Background(), "crashed-worker", time.Minute)
	if err != nil || note == nil || note.Kind != ItemKindNote {
		t.Fatalf("note claim = %+v, %v", note, err)
	}
	clock.Advance(time.Minute + time.Millisecond)
	deliverer := &fakeDeliverer{reconcileResults: []reconciliationResult{{
		result: TickTickReconciliationResult{
			Status: TickTickReconciliationConfirmed, TaskID: "note-one", ProjectID: "note-project",
		},
	}}}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
	runWorkerOnce(t, worker)

	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.State != "complete" || status.Tasks[0].TickTickTaskID != "task-one" ||
		status.Tasks[1].TickTickTaskID != "note-one" || status.Tasks[1].TickTickProjectID != "note-project" {
		t.Fatalf("restart status = %+v", status)
	}
	if len(deliverer.createCalls) != 0 || len(deliverer.noteCalls) != 0 ||
		fmt.Sprint(deliverer.reconcileKinds) != fmt.Sprint([]ItemKind{ItemKindNote}) {
		t.Fatalf("restart calls = tasks:%v notes:%v reconcile:%v",
			deliverer.createCalls, deliverer.noteCalls, deliverer.reconcileKinds)
	}
}

func TestWorkerRecoversExpiredExtractionLease(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "Crash before extraction result")
	claim, err := store.ClaimExtraction(context.Background(), "crashed-worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	clock.Advance(time.Minute + time.Millisecond)
	extractor := &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{
		Provider: "deepseek", Model: deepSeekModel,
	}}}}
	worker := newTestWorker(t, store, extractor, &fakeDeliverer{})
	runWorkerOnce(t, worker)
	status, _ := store.RecordingStatus(context.Background(), receipt.ID)
	if status.State != "complete" || status.AttemptCount != 2 || extractor.calls != 1 {
		t.Fatalf("recovered extraction status = %+v, calls = %d", status, extractor.calls)
	}
}

func TestWorkerBoundsAmbiguousReconciliation(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "Ambiguous create")
	freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Only", Priority: 0}})
	deliverer := &fakeDeliverer{
		createResults: []deliveryResult{{err: &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create task"}}},
		reconcileResults: []reconciliationResult{
			{result: TickTickReconciliationResult{Status: TickTickReconciliationReview}},
			{result: TickTickReconciliationResult{
				Status: TickTickReconciliationConfirmed, TaskID: "task-one", ProjectID: "project",
			}},
		},
	}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
	runWorkerOnce(t, worker)
	clock.Advance(time.Minute)
	runWorkerOnce(t, worker)
	status, _ := store.RecordingStatus(context.Background(), receipt.ID)
	if status.Tasks[0].State != "needs_review" || len(deliverer.createCalls) != 1 || len(deliverer.reconcileCalls) != 1 {
		t.Fatalf("ambiguous status = %+v, creates = %v, reconciles = %v", status, deliverer.createCalls, deliverer.reconcileCalls)
	}
	if err := store.RetryDeliveryByID(context.Background(), status.Tasks[0].ID); err != nil {
		t.Fatalf("RetryDeliveryByID() error = %v", err)
	}
	status, _ = store.RecordingStatus(context.Background(), receipt.ID)
	if status.Tasks[0].State != "extracted" || status.Tasks[0].LastClassification != string(OutcomeAmbiguous) {
		t.Fatalf("manual delivery retry status = %+v", status.Tasks[0])
	}
	runWorkerOnce(t, worker)
	status, _ = store.RecordingStatus(context.Background(), receipt.ID)
	if status.Tasks[0].State != "complete" || len(deliverer.createCalls) != 1 || len(deliverer.reconcileCalls) != 2 {
		t.Fatalf("manual reconciliation status = %+v, creates = %v, reconciles = %v", status.Tasks[0], deliverer.createCalls, deliverer.reconcileCalls)
	}
}

func TestWorkerReconcilesFinalUncertainCreateBeforeReview(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "Ambiguous create loop")
	freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Only", Priority: 0}})
	deliverer := &fakeDeliverer{
		createResults: []deliveryResult{
			{err: &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create task"}},
			{err: &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create task"}},
		},
		reconcileResults: []reconciliationResult{
			{result: TickTickReconciliationResult{Status: TickTickReconciliationUnconfirmed}},
			{result: TickTickReconciliationResult{Status: TickTickReconciliationUnconfirmed}},
		},
	}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)

	runWorkerOnce(t, worker)
	clock.Advance(time.Minute)
	runWorkerOnce(t, worker)
	runWorkerOnce(t, worker)
	clock.Advance(4 * time.Minute)
	runWorkerOnce(t, worker)

	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.Tasks[0].State != "needs_review" || len(deliverer.createCalls) != 2 || len(deliverer.reconcileCalls) != 2 {
		t.Fatalf("bounded status = %+v, creates = %v, reconciles = %v", status.Tasks[0], deliverer.createCalls, deliverer.reconcileCalls)
	}
}

func TestWorkerReconcilesAmbiguousNoteCreationByStoredKind(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private ambiguous note transcript")
	freezeQueueItems(t, store, "extractor", []QueuedItem{{
		Kind: ItemKindNote, Title: "Note", Content: "private note content",
	}})
	deliverer := &fakeDeliverer{
		noteResults: []deliveryResult{{err: &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create note"}}},
		reconcileResults: []reconciliationResult{{result: TickTickReconciliationResult{
			Status: TickTickReconciliationConfirmed, TaskID: "note-one", ProjectID: "note-project",
		}}},
	}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
	runWorkerOnce(t, worker)
	status, _ := store.RecordingStatus(context.Background(), receipt.ID)
	if status.Tasks[0].State != "retry_wait" || status.Tasks[0].LastClassification != string(OutcomeAmbiguous) {
		t.Fatalf("ambiguous note status = %+v", status.Tasks[0])
	}

	clock.Advance(time.Minute)
	runWorkerOnce(t, worker)
	status, _ = store.RecordingStatus(context.Background(), receipt.ID)
	if status.State != "complete" || status.Tasks[0].TickTickTaskID != "note-one" ||
		status.Tasks[0].TickTickProjectID != "note-project" {
		t.Fatalf("reconciled note status = %+v", status)
	}
	if len(deliverer.createCalls) != 0 || fmt.Sprint(deliverer.noteCalls) != "[0]" ||
		fmt.Sprint(deliverer.reconcileKinds) != "[note]" {
		t.Fatalf("note calls = tasks:%v notes:%v reconcile:%v",
			deliverer.createCalls, deliverer.noteCalls, deliverer.reconcileKinds)
	}
}

func TestWorkerRetryScheduleUsesBoundedInjectedJitter(t *testing.T) {
	store, clock := newQueueStore(t)
	worker := newTestWorker(t, store, &fakeExtractor{}, &fakeDeliverer{})
	worker.config.Jitter = func(limit time.Duration) time.Duration {
		return limit
	}
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 1, want: time.Minute + 15*time.Second},
		{attempt: 2, want: 2*time.Minute + 30*time.Second},
		{attempt: 3, want: 5 * time.Minute},
		{attempt: 9, want: 8 * time.Minute},
	}
	for _, test := range tests {
		if got := worker.retryAt(test.attempt).Sub(clock.Time()); got != test.want {
			t.Errorf("retryAt(%d) delay = %s, want %s", test.attempt, got, test.want)
		}
	}
	worker.config.Jitter = func(limit time.Duration) time.Duration { return limit + 1 }
	if got := worker.retryAt(1).Sub(clock.Time()); got != time.Minute {
		t.Errorf("out-of-range jitter delay = %s, want 1m", got)
	}
}

func TestWorkerMapsProviderFailuresAndManualRetry(t *testing.T) {
	tests := []struct {
		name  string
		err   error
		state string
	}{
		{name: "authentication", err: &DeepSeekError{Kind: DeepSeekErrorAuthentication, Operation: "extract"}, state: "blocked_auth"},
		{name: "review", err: &DeepSeekError{Kind: DeepSeekErrorReview, Operation: "extract"}, state: "needs_review"},
		{name: "malformed", err: &DeepSeekError{Kind: DeepSeekErrorMalformed, Operation: "extract"}, state: "dead_letter"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "private transcript")
			worker := newTestWorker(t, store, &fakeExtractor{results: []extractionResult{{err: test.err}}}, &fakeDeliverer{})
			runWorkerOnce(t, worker)
			status, _ := store.RecordingStatus(context.Background(), receipt.ID)
			if status.State != test.state {
				t.Fatalf("state = %q, want %q", status.State, test.state)
			}
			if err := store.RetryRecordingByID(context.Background(), receipt.ID); err != nil {
				t.Fatalf("RetryRecordingByID() error = %v", err)
			}
			status, _ = store.RecordingStatus(context.Background(), receipt.ID)
			if status.State != "received" {
				t.Fatalf("manual retry state = %q, want received", status.State)
			}
		})
	}
}

func TestWorkerLogsSafeDeepSeekFailureReason(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private transcript sentinel")
	providerError := &DeepSeekError{
		Kind: DeepSeekErrorTerminal, Operation: "extract items", StatusCode: http.StatusBadRequest,
		Detail: "private provider body sentinel",
	}
	worker := newTestWorker(t, store, &fakeExtractor{results: []extractionResult{{err: providerError}}}, &fakeDeliverer{})
	var logs strings.Builder
	worker.config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	runWorkerOnce(t, worker)

	for _, expected := range []string{
		`"msg":"deepseek extraction failed"`,
		`"recording_id":` + fmt.Sprint(receipt.ID),
		`"classification":"terminal"`,
		`"reason_code":"provider_http_error"`,
		`"status_code":400`,
	} {
		if !strings.Contains(logs.String(), expected) {
			t.Errorf("failure log %q does not contain %q", logs.String(), expected)
		}
	}
	for _, privateValue := range []string{"private transcript sentinel", "private provider body sentinel"} {
		if strings.Contains(logs.String(), privateValue) {
			t.Errorf("failure log contains private value %q", privateValue)
		}
	}
}

func TestWorkerMapsDeliveryFailures(t *testing.T) {
	tests := []struct {
		name  string
		kind  TickTickErrorKind
		state string
	}{
		{name: "authentication", kind: TickTickErrorAuthentication, state: "blocked_auth"},
		{name: "configuration", kind: TickTickErrorConfiguration, state: "needs_review"},
		{name: "malformed", kind: TickTickErrorMalformed, state: "dead_letter"},
		{name: "transient", kind: TickTickErrorRetryable, state: "retry_wait"},
		{name: "timeout", kind: TickTickErrorRetryable, state: "retry_wait"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "private transcript")
			freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Task", Priority: 0}})
			deliverer := &fakeDeliverer{createResults: []deliveryResult{{err: &TickTickError{
				Kind: test.kind, Operation: "create task",
			}}}}
			worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
			runWorkerOnce(t, worker)
			status, _ := store.RecordingStatus(context.Background(), receipt.ID)
			if status.Tasks[0].State != test.state {
				t.Fatalf("state = %q, want %q", status.Tasks[0].State, test.state)
			}
		})
	}
}

func TestWorkerDeadLettersUnknownStoredKindWithoutProviderCalls(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private unknown transcript")
	freezeQueueItems(t, store, "extractor", []QueuedItem{{Kind: ItemKindTask, Title: "Task", Priority: 0}})
	if _, err := store.db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatalf("enable test constraint override: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE delivery_tasks SET item_kind = 'unknown' WHERE recording_id = ?`, receipt.ID); err != nil {
		t.Fatalf("store unknown item kind: %v", err)
	}
	if _, err := store.db.Exec(`PRAGMA ignore_check_constraints = OFF`); err != nil {
		t.Fatalf("restore constraints: %v", err)
	}
	deliverer := &fakeDeliverer{}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
	runWorkerOnce(t, worker)

	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.Tasks[0].Kind != ItemKind("unknown") || status.Tasks[0].State != "dead_letter" ||
		status.Tasks[0].LastClassification != string(OutcomeMalformed) {
		t.Fatalf("unknown item status = %+v", status.Tasks[0])
	}
	if len(deliverer.createCalls)+len(deliverer.noteCalls)+len(deliverer.reconcileCalls) != 0 {
		t.Fatalf("unknown item used provider: %+v", deliverer)
	}
	var transcript string
	if err := store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript); err != nil {
		t.Fatalf("query transcript: %v", err)
	}
	if transcript != "private unknown transcript" {
		t.Fatalf("unknown item transcript = %q", transcript)
	}
}

func TestWorkerRedactsNoteProviderFailure(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private redaction transcript")
	freezeQueueItems(t, store, "extractor", []QueuedItem{{
		Kind: ItemKindNote, Title: "private redaction title", Content: "private redaction content",
	}})
	deliverer := &fakeDeliverer{noteResults: []deliveryResult{{
		err: errors.New("private provider body"),
	}}}
	worker := newTestWorker(t, store, &fakeExtractor{}, deliverer)
	worked, err := worker.RunOnce(context.Background())
	if err != nil || !worked {
		t.Fatalf("RunOnce() = %t, %v", worked, err)
	}
	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	for _, privateValue := range []string{
		"private redaction transcript", "private redaction title",
		"private redaction content", "private provider body",
	} {
		if strings.Contains(string(encoded), privateValue) {
			t.Errorf("status contains private value %q: %s", privateValue, encoded)
		}
	}
	if status.Tasks[0].State != "dead_letter" || status.Tasks[0].LastClassification != string(OutcomeMalformed) {
		t.Fatalf("redacted note status = %+v", status.Tasks[0])
	}
}

func TestWorkerDeadLettersAfterBoundedRetry(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "retry transcript")
	extractor := &fakeExtractor{results: []extractionResult{{err: &DeepSeekError{Kind: DeepSeekErrorRetryable, Operation: "extract"}}}}
	worker := newTestWorker(t, store, extractor, &fakeDeliverer{})
	for attempt := 1; attempt <= 3; attempt++ {
		runWorkerOnce(t, worker)
		if attempt < 3 {
			clock.Advance(time.Duration(1<<(attempt-1)) * time.Minute)
		}
	}
	status, _ := store.RecordingStatus(context.Background(), receipt.ID)
	if status.State != "dead_letter" || status.AttemptCount != 3 {
		t.Fatalf("bounded retry status = %+v", status)
	}
}

func TestWorkerStatusIsRedactedAndGracefulShutdownLeavesLease(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private transcript")
	claim, err := store.ClaimExtraction(context.Background(), "stopping-worker", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.State != "extracting" || status.RecordingHash != queueTestHash || status.UpdatedAt == "" || status.AttemptCount != 1 {
		t.Fatalf("leased status = %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	for _, privateValue := range []string{"private transcript", "token-value", "raw provider body"} {
		if strings.Contains(string(encoded), privateValue) {
			t.Errorf("status contains private value %q: %s", privateValue, encoded)
		}
	}
	if err := store.RetryRecordingByID(context.Background(), receipt.ID); !errors.Is(err, ErrManualRetryNotAllowed) {
		t.Fatalf("RetryRecordingByID(active) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := newTestWorker(t, store, &fakeExtractor{}, &fakeDeliverer{})
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Run(canceled) error = %v", err)
	}
}
