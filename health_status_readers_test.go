package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOperationalStatusReadFailures(t *testing.T) {
	tests := []struct{ name, statement, prefix string }{
		{"worker query first", "DROP TABLE worker_health; DROP TABLE recordings", "query worker health:"},
		{"heartbeat before intake", "UPDATE worker_health SET heartbeat_at = 'invalid'; DROP TABLE recordings", "parse worker heartbeat:"},
		{"intake totals before queue", "DROP TABLE recordings; DROP TABLE extraction_jobs", "query intake totals:"},
		{"latest intake", "ALTER TABLE recordings RENAME COLUMN last_received_at TO hidden_received_at", "query latest intake:"},
		{"queue query", "DROP TABLE extraction_jobs", "query queue health:"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newQueueStore(t)
			startHealthyWorker(t, store, "health-worker")
			if _, err := store.db.ExecContext(context.Background(), test.statement); err != nil {
				t.Fatal(err)
			}
			status, err := store.OperationalStatus(context.Background())
			if err == nil || !strings.HasPrefix(err.Error(), test.prefix) {
				t.Fatalf("error = %v, want prefix %q", err, test.prefix)
			}
			if !reflect.DeepEqual(status, OperationalStatus{}) {
				t.Fatal("failed read returned a partial status")
			}
		})
	}
}

func TestOperationalStatusRejectsInvalidQueueTimestamp(t *testing.T) {
	store, _ := newQueueStore(t)
	startHealthyWorker(t, store, "health-worker")
	saveQueueRecording(t, store, "synthetic")
	if _, err := store.db.Exec("UPDATE extraction_jobs SET created_at = 'invalid'"); err != nil {
		t.Fatal(err)
	}
	status, err := store.OperationalStatus(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "parse oldest queue time:") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(status, OperationalStatus{}) {
		t.Fatal("invalid timestamp returned a partial snapshot")
	}
}

func TestOperationalStatusUsesSingleClockSnapshot(t *testing.T) {
	store, clock := newQueueStore(t)
	startHealthyWorker(t, store, "health-worker")
	saveQueueRecording(t, store, "synthetic")
	now := clock.Time()
	calls := 0
	store.now = func() time.Time { calls++; return now.Add(time.Duration(calls-1) * time.Hour) }
	status, err := store.OperationalStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("clock calls = %d, want 1", calls)
	}
	if status.CheckedAt != timestamp(now) {
		t.Fatal("snapshot checked time differs")
	}
	if status.Worker.Stale {
		t.Fatal("snapshot worker used a later time")
	}
	if status.Queue.OldestActiveAgeSeconds != 0 {
		t.Fatal("snapshot queue used a later time")
	}
}

func TestOperationalStatusPreservesTypeSafeLatency(t *testing.T) {
	store, clock := newQueueStore(t)
	startHealthyWorker(t, store, "health-worker")
	if err := store.RecordProviderLatency(context.Background(), "health-worker", "typesafe", 1800*time.Millisecond, true); err != nil {
		t.Fatal(err)
	}
	status, err := store.OperationalStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := ProviderLatencyStatus{LastLatencyMilliseconds: 1800, LastObservedAt: timestamp(clock.Time()), LastFailed: true}
	if status.Providers.TypeSafe != want {
		t.Fatalf("TypeSafe status = %+v, want %+v", status.Providers.TypeSafe, want)
	}
}
