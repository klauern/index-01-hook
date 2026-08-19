package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPurgeExpiredRecordingsDeletesOnlyExpiredTerminalWork(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*testing.T, *Store, *adjustableClock) int64
		wantCount int64
	}{
		{
			name: "completed extraction",
			prepare: func(t *testing.T, store *Store, clock *adjustableClock) int64 {
				receipt := saveQueueRecording(t, store, "completed private transcript")
				freezeQueueItems(t, store, "worker", nil)
				clock.Advance(terminalRecordRetention + time.Hour)
				return receipt.ID
			},
			wantCount: 1,
		},
		{
			name: "terminal extraction",
			prepare: func(t *testing.T, store *Store, clock *adjustableClock) int64 {
				receipt := saveQueueRecording(t, store, "terminal private transcript")
				claimQueueExtraction(t, store, receipt.ID)
				if err := store.BlockExtraction(context.Background(), receipt.ID, "queue-worker", OutcomeMalformed, "dead_letter"); err != nil {
					t.Fatalf("BlockExtraction() error = %v", err)
				}
				clock.Advance(terminalRecordRetention + time.Hour)
				return receipt.ID
			},
			wantCount: 1,
		},
		{
			name: "terminal delivery",
			prepare: func(t *testing.T, store *Store, clock *adjustableClock) int64 {
				receipt := saveQueueRecording(t, store, "terminal delivery transcript")
				freezeQueueItems(t, store, "worker", []QueuedItem{{
					Kind: ItemKindTask, Title: "Private title", Content: "Private content", Priority: 0,
				}})
				claim, err := store.ClaimDelivery(context.Background(), "delivery-worker", time.Minute)
				if err != nil || claim == nil {
					t.Fatalf("ClaimDelivery() = %+v, %v", claim, err)
				}
				if err := store.BlockDelivery(context.Background(), claim.ID, "delivery-worker", OutcomeMalformed, "dead_letter"); err != nil {
					t.Fatalf("BlockDelivery() error = %v", err)
				}
				clock.Advance(terminalRecordRetention + time.Hour)
				return receipt.ID
			},
			wantCount: 1,
		},
		{
			name: "audio only",
			prepare: func(t *testing.T, store *Store, clock *adjustableClock) int64 {
				receipt, err := store.SaveRecording(context.Background(), RecordingInput{
					RecordedAtMillis: 1760000000000, Client: "test", AudioFilename: "private.wav",
					AudioByteCount: 7, Fingerprint: strings.Repeat("a", 64),
				})
				if err != nil {
					t.Fatalf("SaveRecording() error = %v", err)
				}
				clock.Advance(terminalRecordRetention + time.Hour)
				return receipt.ID
			},
			wantCount: 1,
		},
		{
			name: "active extraction",
			prepare: func(t *testing.T, store *Store, clock *adjustableClock) int64 {
				receipt := saveQueueRecording(t, store, "active private transcript")
				clock.Advance(terminalRecordRetention + time.Hour)
				return receipt.ID
			},
			wantCount: 0,
		},
		{
			name: "recent terminal state",
			prepare: func(t *testing.T, store *Store, clock *adjustableClock) int64 {
				clock.Advance(terminalRecordRetention + time.Hour)
				receipt := saveQueueRecording(t, store, "recent terminal transcript")
				claimQueueExtraction(t, store, receipt.ID)
				if err := store.BlockExtraction(context.Background(), receipt.ID, "queue-worker", OutcomeMalformed, "dead_letter"); err != nil {
					t.Fatalf("BlockExtraction() error = %v", err)
				}
				return receipt.ID
			},
			wantCount: 0,
		},
		{
			name: "recent duplicate receipt",
			prepare: func(t *testing.T, store *Store, clock *adjustableClock) int64 {
				receipt := saveQueueRecording(t, store, "duplicate terminal transcript")
				claimQueueExtraction(t, store, receipt.ID)
				if err := store.BlockExtraction(context.Background(), receipt.ID, "queue-worker", OutcomeMalformed, "dead_letter"); err != nil {
					t.Fatalf("BlockExtraction() error = %v", err)
				}
				clock.Advance(terminalRecordRetention + time.Hour)
				duplicate := saveQueueRecording(t, store, "duplicate terminal transcript")
				if !duplicate.Duplicate || duplicate.ID != receipt.ID {
					t.Fatalf("duplicate receipt = %+v", duplicate)
				}
				return receipt.ID
			},
			wantCount: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newQueueStore(t)
			recordingID := test.prepare(t, store, clock)
			count, err := store.PurgeExpiredRecordings(context.Background(), terminalRecordRetention)
			if err != nil {
				t.Fatalf("PurgeExpiredRecordings() error = %v", err)
			}
			if count != test.wantCount {
				t.Fatalf("purged count = %d, want %d", count, test.wantCount)
			}
			var remaining int
			if err := store.db.QueryRow(`SELECT count(*) FROM recordings WHERE id = ?`, recordingID).Scan(&remaining); err != nil {
				t.Fatalf("count recording: %v", err)
			}
			if remaining != 1-int(test.wantCount) {
				t.Fatalf("remaining recording count = %d", remaining)
			}
			if test.wantCount == 1 {
				queries := map[string]string{
					"dispatches":          `SELECT count(*) FROM dispatches WHERE recording_id = ?`,
					"extraction_jobs":     `SELECT count(*) FROM extraction_jobs WHERE recording_id = ?`,
					"extractions":         `SELECT count(*) FROM extractions WHERE recording_id = ?`,
					"extraction_attempts": `SELECT count(*) FROM extraction_attempts WHERE recording_id = ?`,
					"delivery_tasks":      `SELECT count(*) FROM delivery_tasks WHERE recording_id = ?`,
					"delivery_attempts": `SELECT count(*)
						FROM delivery_attempts a
						JOIN delivery_tasks d ON d.id = a.delivery_task_id
						WHERE d.recording_id = ?`,
				}
				for table, query := range queries {
					if err := store.db.QueryRow(query, recordingID).Scan(&remaining); err != nil {
						t.Fatalf("count %s for recording %d: %v", table, recordingID, err)
					}
					if remaining != 0 {
						t.Fatalf("%s rows remain for recording %d = %d", table, recordingID, remaining)
					}
				}
				if err := store.db.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&remaining); err != nil {
					t.Fatalf("check foreign keys: %v", err)
				}
				if remaining != 0 {
					t.Fatalf("foreign-key violations = %d", remaining)
				}
			}
		})
	}

	store, _ := newQueueStore(t)
	if _, err := store.PurgeExpiredRecordings(context.Background(), 0); err == nil {
		t.Fatal("PurgeExpiredRecordings() accepted zero retention")
	}
}

func TestPurgeExpiredOperatorRequiresExactConfirmation(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "retention.db")
	past := time.Now().UTC().Add(-terminalRecordRetention - time.Hour)
	store, err := openStore(context.Background(), databasePath, func() time.Time { return past })
	if err != nil {
		t.Fatalf("openStore() error = %v", err)
	}
	if _, err := store.SaveRecording(context.Background(), RecordingInput{
		RecordedAtMillis: 1760000000000, Client: "test", AudioFilename: "private.wav",
		AudioByteCount: 7, Fingerprint: strings.Repeat("b", 64),
	}); err != nil {
		t.Fatalf("SaveRecording() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	getenv := func(name string) string {
		if name == "INDEX01_DB_PATH" {
			return databasePath
		}
		return ""
	}
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if err := execute(logger, []string{"purge-expired"}, getenv, &bytes.Buffer{}); err == nil {
		t.Fatal("purge-expired accepted a missing confirmation")
	}

	confirmed := func(name string) string {
		if name == "INDEX01_PURGE_CONFIRM" {
			return "purge-expired-recordings"
		}
		return getenv(name)
	}
	var output bytes.Buffer
	if err := execute(logger, []string{"purge-expired"}, confirmed, &output); err != nil {
		t.Fatalf("execute(purge-expired) error = %v", err)
	}
	var result purgeResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode purge result: %v", err)
	}
	if result.State != "purged" || result.RecordingsDeleted != 1 || result.RetentionDays != 30 {
		t.Fatalf("purge result = %+v", result)
	}
}
