package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const queueTestHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type adjustableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *adjustableClock) Time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *adjustableClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func newQueueStore(t *testing.T) (*Store, *adjustableClock) {
	t.Helper()
	clock := &adjustableClock{now: time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)}
	store, err := openStore(context.Background(), filepath.Join(t.TempDir(), "queue.db"), clock.Time)
	if err != nil {
		t.Fatalf("openStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, clock
}

func saveQueueRecording(t *testing.T, store *Store, transcript string) Receipt {
	t.Helper()
	receipt, err := store.SaveRecording(context.Background(), RecordingInput{
		RecordedAtMillis: 1760000000000,
		Client:           "test",
		Transcription:    transcript,
		Fingerprint:      queueTestHash,
	})
	if err != nil {
		t.Fatalf("SaveRecording() error = %v", err)
	}
	return receipt
}

func freezeQueueTasks(t *testing.T, store *Store, owner string, tasks []QueuedTask) int64 {
	t.Helper()
	claim, err := store.ClaimExtraction(context.Background(), owner, time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	if err := store.FreezeExtraction(context.Background(), claim.RecordingID, owner, FrozenExtraction{
		Provider:           "deepseek",
		Model:              "deepseek-v4-flash",
		ProviderResponseID: "response-1",
		Tasks:              tasks,
	}); err != nil {
		t.Fatalf("FreezeExtraction() error = %v", err)
	}
	return claim.RecordingID
}

func freezeQueueItems(t *testing.T, store *Store, owner string, items []QueuedItem) int64 {
	t.Helper()
	claim, err := store.ClaimExtraction(context.Background(), owner, time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	if err := store.FreezeExtraction(context.Background(), claim.RecordingID, owner, FrozenExtraction{
		Provider:           "deepseek",
		Model:              "deepseek-v4-flash",
		ProviderResponseID: "response-1",
		Items:              items,
	}); err != nil {
		t.Fatalf("FreezeExtraction() error = %v", err)
	}
	return claim.RecordingID
}

func TestDeliveryQueueFreshDatabaseAndLegacyUpgrade(t *testing.T) {
	t.Run("fresh database", func(t *testing.T) {
		store, _ := newQueueStore(t)
		var version int
		if err := store.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
			t.Fatalf("query migration version: %v", err)
		}
		if version != 6 {
			t.Fatalf("migration version = %d, want 6", version)
		}
		for _, table := range []string{"extraction_jobs", "extractions", "extraction_attempts", "delivery_tasks", "delivery_attempts", "worker_health"} {
			var count int
			if err := store.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
				t.Fatalf("query table %q: %v", table, err)
			}
			if count != 1 {
				t.Errorf("table %q count = %d, want 1", table, count)
			}
		}
	})

	t.Run("upgrade version one", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "legacy.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open legacy database: %v", err)
		}
		initial, err := os.ReadFile("migrations/001_initial.sql")
		if err != nil {
			t.Fatalf("read initial migration: %v", err)
		}
		if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
			t.Fatalf("create migration registry: %v", err)
		}
		if _, err := db.Exec(string(initial)); err != nil {
			t.Fatalf("apply legacy schema: %v", err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (1, '001_initial.sql', '2026-08-12T00:00:00Z')`); err != nil {
			t.Fatalf("record legacy migration: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO recordings (
				id, recorded_at_ms, client, transcription, payload_fingerprint,
				first_received_at, last_received_at
			) VALUES (7, 1760000000000, 'legacy', 'preserve me', ?, 'old', 'old')`, queueTestHash); err != nil {
			t.Fatalf("insert legacy recording: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO dispatches (recording_id, destination, status, attempt_count, created_at)
			VALUES (7, 'deepseek', 'pending', 0, 'old')`); err != nil {
			t.Fatalf("insert legacy dispatch: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close legacy database: %v", err)
		}

		clock := &adjustableClock{now: time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)}
		store, err := openStore(ctx, path, clock.Time)
		if err != nil {
			t.Fatalf("upgrade database: %v", err)
		}
		defer ignoreCloseError(store)
		var transcript, state string
		if err := store.db.QueryRow(`
			SELECT r.transcription, j.state
			FROM recordings r JOIN extraction_jobs j ON j.recording_id = r.id
			WHERE r.id = 7`).Scan(&transcript, &state); err != nil {
			t.Fatalf("query upgraded data: %v", err)
		}
		if transcript != "preserve me" || state != "pending" {
			t.Errorf("upgraded data = (%q, %q), want preserved pending data", transcript, state)
		}
		var legacyDispatches int
		_ = store.db.QueryRow(`SELECT count(*) FROM dispatches WHERE recording_id = 7`).Scan(&legacyDispatches)
		if legacyDispatches != 1 {
			t.Errorf("legacy dispatch count = %d, want 1", legacyDispatches)
		}
	})

	t.Run("upgrade version three preserves delivery", func(t *testing.T) {
		ctx := context.Background()
		path := filepath.Join(t.TempDir(), "version-three.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open version three database: %v", err)
		}
		if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
			t.Fatalf("create migration registry: %v", err)
		}
		for version, name := range []string{"001_initial.sql", "002_delivery_queue.sql", "003_worker_state.sql"} {
			script, err := os.ReadFile(filepath.Join("migrations", name))
			if err != nil {
				t.Fatalf("read migration %q: %v", name, err)
			}
			if _, err := db.Exec(string(script)); err != nil {
				t.Fatalf("apply migration %q: %v", name, err)
			}
			if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (?, ?, '2026-08-12T00:00:00Z')`, version+1, name); err != nil {
				t.Fatalf("record migration %q: %v", name, err)
			}
		}
		if _, err := db.Exec(`
			INSERT INTO recordings (
				id, recorded_at_ms, client, transcription, payload_fingerprint,
				first_received_at, last_received_at
			) VALUES (7, 1760000000000, 'legacy', 'preserve transcript', ?, 'old', 'old')`, queueTestHash); err != nil {
			t.Fatalf("insert recording: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO extraction_jobs (
				recording_id, state, attempt_count, next_attempt_at_ms,
				created_at, updated_at, workflow_state
			) VALUES (7, 'frozen', 2, 0, 'old', 'old', 'extracted')`); err != nil {
			t.Fatalf("insert extraction job: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO delivery_tasks (
				id, recording_id, task_index, title, notes, priority, tags_json,
				marker, state, attempt_count, next_attempt_at_ms,
				created_at, updated_at, workflow_state
			) VALUES (9, 7, 3, 'legacy title', 'legacy notes', 3, '["legacy"]',
				?, 'retry', 2, 1234, 'old', 'old', 'retry_wait')`,
			"[index01:"+queueTestHash+":3]"); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO delivery_tasks (
				id, recording_id, task_index, title, notes, priority, tags_json,
				marker, state, attempt_count, next_attempt_at_ms, last_classification,
				ticktick_task_id, ticktick_project_id, created_at, updated_at,
				completed_at, workflow_state, reconcile_attempt_count, cycle_attempt_count
			) VALUES (10, 7, 4, 'completed title', 'completed notes', 5, '["complete"]',
				?, 'completed', 2, 0, 'reconciled', 'ticktick-task-10',
				'ticktick-project-10', 'created-value', 'updated-value',
				'completed-value', 'complete', 1, 2)`,
			"[index01:"+queueTestHash+":4]"); err != nil {
			t.Fatalf("insert completed delivery: %v", err)
		}
		if _, err := db.Exec(`
			INSERT INTO delivery_attempts (
				delivery_task_id, attempt_number, classification, created_at
			) VALUES
				(10, 1, 'ambiguous', 'attempt-one'),
				(10, 2, 'reconciled', 'attempt-two')`); err != nil {
			t.Fatalf("insert completed delivery attempts: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close version three database: %v", err)
		}

		store, err := openStore(ctx, path, time.Now)
		if err != nil {
			t.Fatalf("upgrade version three database: %v", err)
		}
		defer ignoreCloseError(store)
		var kind, title, notes, state, marker string
		var taskIndex, attemptCount int
		if err := store.db.QueryRow(`
			SELECT item_kind, task_index, title, notes, state, attempt_count, marker
			FROM delivery_tasks WHERE id = 9`).Scan(
			&kind, &taskIndex, &title, &notes, &state, &attemptCount, &marker,
		); err != nil {
			t.Fatalf("query upgraded delivery: %v", err)
		}
		if kind != "task" || taskIndex != 3 || title != "legacy title" || notes != "legacy notes" ||
			state != "retry" || attemptCount != 2 || marker != "[index01:"+queueTestHash+":3]" {
			t.Fatalf("upgraded delivery changed: kind=%q index=%d title=%q notes=%q state=%q attempts=%d marker=%q",
				kind, taskIndex, title, notes, state, attemptCount, marker)
		}
		var tickTickTaskID, tickTickProjectID, completedAt, lastClassification string
		var reconcileAttemptCount, cycleAttemptCount int
		if err := store.db.QueryRow(`
			SELECT item_kind, task_index, state, attempt_count, cycle_attempt_count,
				reconcile_attempt_count, last_classification, marker,
				ticktick_task_id, ticktick_project_id, completed_at
			FROM delivery_tasks WHERE id = 10`).Scan(
			&kind, &taskIndex, &state, &attemptCount, &cycleAttemptCount,
			&reconcileAttemptCount, &lastClassification, &marker,
			&tickTickTaskID, &tickTickProjectID, &completedAt,
		); err != nil {
			t.Fatalf("query upgraded completed delivery: %v", err)
		}
		if kind != "task" || taskIndex != 4 || state != "completed" ||
			attemptCount != 2 || cycleAttemptCount != 2 || reconcileAttemptCount != 1 ||
			lastClassification != "reconciled" || marker != "[index01:"+queueTestHash+":4]" ||
			tickTickTaskID != "ticktick-task-10" || tickTickProjectID != "ticktick-project-10" ||
			completedAt != "completed-value" {
			t.Fatalf("upgraded completed delivery changed: kind=%q index=%d state=%q attempts=%d cycle=%d reconcile=%d classification=%q marker=%q task=%q project=%q completed=%q",
				kind, taskIndex, state, attemptCount, cycleAttemptCount, reconcileAttemptCount,
				lastClassification, marker, tickTickTaskID, tickTickProjectID, completedAt)
		}
		rows, err := store.db.Query(`
			SELECT attempt_number, classification, created_at
			FROM delivery_attempts WHERE delivery_task_id = 10 ORDER BY attempt_number`)
		if err != nil {
			t.Fatalf("query upgraded delivery attempts: %v", err)
		}
		defer ignoreCloseError(rows)
		wantAttempts := []struct {
			number         int
			classification string
			createdAt      string
		}{
			{number: 1, classification: "ambiguous", createdAt: "attempt-one"},
			{number: 2, classification: "reconciled", createdAt: "attempt-two"},
		}
		attemptIndex := 0
		for rows.Next() {
			if attemptIndex >= len(wantAttempts) {
				t.Fatalf("upgraded delivery has more than %d attempts", len(wantAttempts))
			}
			var number int
			var classification, createdAt string
			if err := rows.Scan(&number, &classification, &createdAt); err != nil {
				t.Fatalf("scan upgraded delivery attempt: %v", err)
			}
			want := wantAttempts[attemptIndex]
			if number != want.number || classification != want.classification || createdAt != want.createdAt {
				t.Errorf("upgraded delivery attempt %d = (%d, %q, %q), want (%d, %q, %q)",
					attemptIndex, number, classification, createdAt,
					want.number, want.classification, want.createdAt)
			}
			attemptIndex++
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate upgraded delivery attempts: %v", err)
		}
		if attemptIndex != len(wantAttempts) {
			t.Fatalf("upgraded delivery attempt count = %d, want %d", attemptIndex, len(wantAttempts))
		}
	})
}

func TestConcurrentIdenticalIntakeCreatesOneRecordingAndQueueItem(t *testing.T) {
	store, _ := newQueueStore(t)
	const workers = 12
	var wait sync.WaitGroup
	errorsCh := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := store.SaveRecording(context.Background(), RecordingInput{
				RecordedAtMillis: 1760000000000,
				Client:           "test",
				Transcription:    "same transcript",
				Fingerprint:      queueTestHash,
			})
			errorsCh <- err
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent SaveRecording() error = %v", err)
		}
	}
	var recordings, jobs, receives int
	_ = store.db.QueryRow(`SELECT count(*), max(receive_count) FROM recordings`).Scan(&recordings, &receives)
	_ = store.db.QueryRow(`SELECT count(*) FROM extraction_jobs`).Scan(&jobs)
	if recordings != 1 || jobs != 1 || receives != workers {
		t.Fatalf("counts = recordings:%d jobs:%d receives:%d, want 1, 1, %d", recordings, jobs, receives, workers)
	}
}

func TestExtractionLeaseRejectsActiveAndPermitsExpiredClaim(t *testing.T) {
	store, clock := newQueueStore(t)
	saveQueueRecording(t, store, "private transcript")
	first, err := store.ClaimExtraction(context.Background(), "worker-one", time.Minute)
	if err != nil || first == nil || first.AttemptNumber != 1 {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	blocked, err := store.ClaimExtraction(context.Background(), "worker-two", time.Minute)
	if err != nil || blocked != nil {
		t.Fatalf("active lease claim = %+v, %v, want nil", blocked, err)
	}
	clock.Advance(time.Minute + time.Millisecond)
	second, err := store.ClaimExtraction(context.Background(), "worker-two", time.Minute)
	if err != nil || second == nil || second.RecordingID != first.RecordingID || second.AttemptNumber != 2 {
		t.Fatalf("expired lease claim = %+v, %v", second, err)
	}
	if err := store.FreezeExtraction(context.Background(), first.RecordingID, "worker-one", FrozenExtraction{
		Provider: "deepseek", Model: "model",
	}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale FreezeExtraction() error = %v, want ErrLeaseLost", err)
	}
}

func TestFreezeExtractionCommitsTasksAtomically(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "exact transcript")
	claim, err := store.ClaimExtraction(context.Background(), "extractor", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	invalid := make([]QueuedTask, maxExtractionTasks+1)
	if err := store.FreezeExtraction(context.Background(), receipt.ID, "extractor", FrozenExtraction{
		Provider: "deepseek", Model: "model", Tasks: invalid,
	}); err == nil {
		t.Fatal("FreezeExtraction() accepted too many tasks")
	}
	var extractions, tasks int
	_ = store.db.QueryRow(`SELECT count(*) FROM extractions`).Scan(&extractions)
	_ = store.db.QueryRow(`SELECT count(*) FROM delivery_tasks`).Scan(&tasks)
	if extractions != 0 || tasks != 0 {
		t.Fatalf("partial freeze counts = extractions:%d tasks:%d, want 0, 0", extractions, tasks)
	}

	due := time.Date(2026, time.August, 13, 14, 0, 0, 0, time.UTC)
	err = store.FreezeExtraction(context.Background(), receipt.ID, "extractor", FrozenExtraction{
		Provider: "deepseek", Model: "deepseek-v4-flash", ProviderResponseID: "response-1",
		Tasks: []QueuedTask{
			{Title: "First", Notes: "one", Priority: 3, Tags: []string{"voice"}},
			{Title: "Second", Notes: "two", Due: &due, Priority: 5, ProjectAlias: "work"},
		},
	})
	if err != nil {
		t.Fatalf("FreezeExtraction() error = %v", err)
	}
	_ = store.db.QueryRow(`SELECT count(*) FROM extractions`).Scan(&extractions)
	_ = store.db.QueryRow(`SELECT count(*) FROM delivery_tasks`).Scan(&tasks)
	if extractions != 1 || tasks != 2 {
		t.Fatalf("frozen counts = extractions:%d tasks:%d, want 1, 2", extractions, tasks)
	}
	var attemptClass string
	_ = store.db.QueryRow(`SELECT classification FROM extraction_attempts`).Scan(&attemptClass)
	if attemptClass != string(OutcomeSuccess) {
		t.Errorf("extraction classification = %q, want success", attemptClass)
	}
}

func TestFreezeExtractionPersistsAndClaimsMixedItems(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private mixed transcript")
	due := time.Date(2026, time.August, 13, 14, 0, 0, 0, time.UTC)
	freezeQueueItems(t, store, "extractor", []QueuedItem{
		{
			Kind: ItemKindTask, Title: "private task title", Content: "private task content",
			Due: &due, Priority: 3, Tags: []string{"voice"}, ProjectAlias: "work",
		},
		{Kind: ItemKindNote, Title: "private note title", Content: "private note content"},
	})

	rows, err := store.db.Query(`
		SELECT item_kind, task_index, notes, marker
		FROM delivery_tasks WHERE recording_id = ? ORDER BY task_index`, receipt.ID)
	if err != nil {
		t.Fatalf("query frozen items: %v", err)
	}
	defer ignoreCloseError(rows)
	wantKinds := []string{"task", "note"}
	wantContent := []string{"private task content", "private note content"}
	index := 0
	for rows.Next() {
		var kind, content, marker string
		var taskIndex int
		if err := rows.Scan(&kind, &taskIndex, &content, &marker); err != nil {
			t.Fatalf("scan frozen item: %v", err)
		}
		wantMarker, err := tickTickMarker(queueTestHash, index)
		if err != nil {
			t.Fatalf("build expected marker: %v", err)
		}
		if kind != wantKinds[index] || taskIndex != index || content != wantContent[index] || marker != wantMarker {
			t.Errorf("frozen item %d = kind:%q index:%d content:%q marker:%q", index, kind, taskIndex, content, marker)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate frozen items: %v", err)
	}
	if index != 2 {
		t.Fatalf("frozen item count = %d, want 2", index)
	}
	var itemCount int
	if err := store.db.QueryRow(`SELECT task_count FROM extractions WHERE recording_id = ?`, receipt.ID).Scan(&itemCount); err != nil {
		t.Fatalf("query extraction item count: %v", err)
	}
	if itemCount != 2 {
		t.Errorf("extraction item count = %d, want 2", itemCount)
	}

	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if len(status.Tasks) != 2 || status.Tasks[0].Kind != ItemKindTask || status.Tasks[1].Kind != ItemKindNote {
		t.Fatalf("mixed item status = %+v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal mixed status: %v", err)
	}
	for _, secret := range []string{"private mixed transcript", "private task title", "private task content", "private note title", "private note content"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("status contains private value %q: %s", secret, encoded)
		}
	}

	claim, err := store.ClaimDelivery(context.Background(), "delivery-one", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimDelivery() = %+v, %v", claim, err)
	}
	if claim.Kind != ItemKindTask || claim.TaskIndex != 0 || claim.Notes != "private task content" {
		t.Fatalf("task claim = %+v", claim)
	}
	if err := store.RetryDelivery(context.Background(), claim.ID, "delivery-one", OutcomeAmbiguous, clock.Time()); err != nil {
		t.Fatalf("RetryDelivery() error = %v", err)
	}
	retry, err := store.ClaimDelivery(context.Background(), "delivery-two", time.Minute)
	if err != nil || retry == nil {
		t.Fatalf("retry ClaimDelivery() = %+v, %v", retry, err)
	}
	if retry.Kind != ItemKindTask || retry.LastClassification != OutcomeAmbiguous || retry.ReconcileAttemptCount != 0 {
		t.Fatalf("reconciliation claim = %+v", retry)
	}
	if err := store.CompleteDelivery(context.Background(), DeliveryCompletion{
		TaskID: retry.ID, LeaseOwner: "delivery-two", Classification: OutcomeReconciled,
		TickTickTaskID: "ticktick-task", TickTickProjectID: "ticktick-project",
	}); err != nil {
		t.Fatalf("CompleteDelivery() error = %v", err)
	}
	note, err := store.ClaimDelivery(context.Background(), "delivery-three", time.Minute)
	if err != nil || note == nil {
		t.Fatalf("note delivery claim = %+v, %v", note, err)
	}
	if note.Kind != ItemKindNote || note.TaskIndex != 1 || note.Notes != "private note content" {
		t.Fatalf("note claim = %+v", note)
	}
	if err := store.CompleteDelivery(context.Background(), DeliveryCompletion{
		TaskID: note.ID, LeaseOwner: "delivery-three", Classification: OutcomeCreated,
		TickTickTaskID: "ticktick-note", TickTickProjectID: "ticktick-note-project",
	}); err != nil {
		t.Fatalf("CompleteDelivery(note) error = %v", err)
	}
	status, err = store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("final RecordingStatus() error = %v", err)
	}
	if status.State != "complete" || status.Tasks[0].State != "complete" || status.Tasks[1].State != "complete" {
		t.Fatalf("completed mixed status = %+v", status)
	}
	var transcript string
	if err := store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript); err != nil {
		t.Fatalf("query completed transcript: %v", err)
	}
	if transcript != "" {
		t.Fatalf("completed transcript = %q", transcript)
	}
}

func TestFreezeExtractionRejectsInvalidItemKindsAtomically(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "invalid item kind")
	claim, err := store.ClaimExtraction(context.Background(), "extractor", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	err = store.FreezeExtraction(context.Background(), receipt.ID, "extractor", FrozenExtraction{
		Provider: "deepseek", Model: "model",
		Items: []QueuedItem{
			{Kind: ItemKindTask, Title: "valid", Priority: 0},
			{Kind: ItemKind("private-invalid-kind"), Title: "invalid"},
		},
	})
	if err == nil || strings.Contains(err.Error(), "private-invalid-kind") {
		t.Fatalf("invalid item kind error = %v", err)
	}
	var extractions, items int
	_ = store.db.QueryRow(`SELECT count(*) FROM extractions`).Scan(&extractions)
	_ = store.db.QueryRow(`SELECT count(*) FROM delivery_tasks`).Scan(&items)
	if extractions != 0 || items != 0 {
		t.Fatalf("partial invalid freeze = extractions:%d items:%d", extractions, items)
	}
	if _, err := store.db.Exec(`
		INSERT INTO delivery_tasks (
			recording_id, task_index, item_kind, title, priority, marker, state,
			next_attempt_at_ms, created_at, updated_at, workflow_state
		) VALUES (?, 0, 'invalid', 'invalid', 0, ?, 'pending', 0, 'now', 'now', 'extracted')`,
		receipt.ID, "[index01:"+queueTestHash+":0]"); err == nil {
		t.Fatal("delivery_tasks accepted an invalid item kind")
	}
}

func TestDeliveryRetryPreservesCompletedSiblingAndErasesTranscriptAtCompletion(t *testing.T) {
	store, clock := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "private transcript body")
	freezeQueueTasks(t, store, "extractor", []QueuedTask{
		{Title: "Private first title", Notes: "private first notes", Priority: 0},
		{Title: "Private second title", Notes: "private second notes", Priority: 3},
	})

	first, err := store.ClaimDelivery(context.Background(), "delivery-one", time.Minute)
	if err != nil || first == nil || first.TaskIndex != 0 || first.Notes != "private first notes" {
		t.Fatalf("first ClaimDelivery() = %+v, %v", first, err)
	}
	blocked, err := store.ClaimDelivery(context.Background(), "delivery-two", time.Minute)
	if err != nil || blocked == nil || blocked.TaskIndex != 1 {
		t.Fatalf("sibling ClaimDelivery() = %+v, %v", blocked, err)
	}
	if err := store.CompleteDelivery(context.Background(), DeliveryCompletion{
		TaskID: first.ID, LeaseOwner: "delivery-one", Classification: OutcomeCreated,
		TickTickTaskID: "ticktick-one", TickTickProjectID: "project-one",
	}); err != nil {
		t.Fatalf("CompleteDelivery(first) error = %v", err)
	}
	retryAt := clock.Time().Add(10 * time.Minute)
	if err := store.RetryDelivery(context.Background(), blocked.ID, "delivery-two", OutcomeRetryable, retryAt); err != nil {
		t.Fatalf("RetryDelivery() error = %v", err)
	}
	var firstState, secondState, transcript string
	_ = store.db.QueryRow(`SELECT state FROM delivery_tasks WHERE id = ?`, first.ID).Scan(&firstState)
	_ = store.db.QueryRow(`SELECT state FROM delivery_tasks WHERE id = ?`, blocked.ID).Scan(&secondState)
	_ = store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript)
	if firstState != "completed" || secondState != "retry" || transcript != "private transcript body" {
		t.Fatalf("partial states = (%q, %q, %q)", firstState, secondState, transcript)
	}
	if claim, err := store.ClaimDelivery(context.Background(), "too-early", time.Minute); err != nil || claim != nil {
		t.Fatalf("early retry claim = %+v, %v, want nil", claim, err)
	}
	clock.Advance(10 * time.Minute)
	retry, err := store.ClaimDelivery(context.Background(), "delivery-three", time.Minute)
	if err != nil || retry == nil || retry.ID != blocked.ID || retry.AttemptNumber != 2 {
		t.Fatalf("eligible retry claim = %+v, %v", retry, err)
	}
	if err := store.CompleteDelivery(context.Background(), DeliveryCompletion{
		TaskID: retry.ID, LeaseOwner: "delivery-three", Classification: OutcomeReconciled,
		TickTickTaskID: "ticktick-two", TickTickProjectID: "project-one",
	}); err != nil {
		t.Fatalf("CompleteDelivery(retry) error = %v", err)
	}
	_ = store.db.QueryRow(`SELECT transcription FROM recordings WHERE id = ?`, receipt.ID).Scan(&transcript)
	if transcript != "" {
		t.Errorf("completed transcription = %q, want erased", transcript)
	}
	status, err := store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	for _, secret := range []string{
		"private transcript body", "Private first title", "private first notes",
		"token-value", "raw provider body",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("status contains private value %q: %s", secret, encoded)
		}
	}
	if status.State != "complete" || len(status.Tasks) != 2 || status.Tasks[0].State != "complete" || status.Tasks[1].State != "complete" {
		t.Errorf("final status = %+v", status)
	}
}

func TestZeroTaskExtractionErasesTranscript(t *testing.T) {
	store, _ := newQueueStore(t)
	receipt := saveQueueRecording(t, store, "nothing actionable")
	freezeQueueTasks(t, store, "extractor", nil)
	var transcript, state string
	if err := store.db.QueryRow(`
		SELECT r.transcription, j.state
		FROM recordings r JOIN extraction_jobs j ON j.recording_id = r.id
		WHERE r.id = ?`, receipt.ID).Scan(&transcript, &state); err != nil {
		t.Fatalf("query completed empty extraction: %v", err)
	}
	if transcript != "" || state != "completed" {
		t.Errorf("empty extraction = transcript:%q state:%q", transcript, state)
	}
}

func TestDeliveryLeaseExpiresAndClassificationsAreConstrained(t *testing.T) {
	store, clock := newQueueStore(t)
	saveQueueRecording(t, store, "lease transcript")
	freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Task", Priority: 0}})
	first, err := store.ClaimDelivery(context.Background(), "worker-one", time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first ClaimDelivery() = %+v, %v", first, err)
	}
	if claim, err := store.ClaimDelivery(context.Background(), "worker-two", time.Minute); err != nil || claim != nil {
		t.Fatalf("active delivery lease claim = %+v, %v", claim, err)
	}
	clock.Advance(time.Minute + time.Millisecond)
	second, err := store.ClaimDelivery(context.Background(), "worker-two", time.Minute)
	if err != nil || second == nil || second.ID != first.ID || second.AttemptNumber != 2 {
		t.Fatalf("expired delivery claim = %+v, %v", second, err)
	}
	if err := store.RetryDelivery(context.Background(), second.ID, "worker-two", OutcomeClassification("raw provider body"), clock.Time()); err == nil {
		t.Fatal("RetryDelivery() accepted an unsafe classification")
	}
	if _, err := store.db.Exec(`UPDATE delivery_tasks SET last_classification = 'raw provider body' WHERE id = ?`, second.ID); err == nil {
		t.Fatal("delivery_tasks accepted an unsafe stored classification")
	}
}
