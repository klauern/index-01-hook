package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/application"
)

// TestApplicationDateAdapterCompatibility preserves date aliases and provider diagnostics.
func TestApplicationDateAdapterCompatibility(t *testing.T) {
	var parts TypeSafeDateParts = application.DateParts{Mode: "relative", DayAnchor: "tomorrow", WeekOffset: 1, Confidence: map[string]float64{"mode": 0.9}}
	encoded, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"Mode":"relative","Month":0,"Day":0,"Year":0,"DayAnchor":"tomorrow","Weekday":"","WeekOffset":1,"Confidence":{"mode":0.9}}`
	if string(encoded) != want {
		t.Fatalf("date wire shape=%s", encoded)
	}
	var roundTrip application.DateParts
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || roundTrip.DayAnchor != parts.DayAnchor {
		t.Fatalf("round trip=%+v %v", roundTrip, err)
	}
	client := newDeepSeekFixture(t, time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC), func(*http.Request) (*http.Response, error) { t.Fatal("date parsing attempted HTTP"); return nil, nil })
	for _, tc := range []struct {
		raw    *string
		allDay bool
		detail string
	}{
		{nil, true, string(application.DueMissing)},
		{func() *string { s := "synthetic-private-date"; return &s }(), false, string(application.DueTimeInvalid)},
		{func() *string { s := "2026-02-30"; return &s }(), true, string(application.DueDateInvalid)},
		{func() *string { s := "2026-07-01T09:00:00-06:00"; return &s }(), false, string(application.DueOffsetInvalid)},
	} {
		due, err := client.parseDue(tc.raw, tc.allDay)
		var provider *DeepSeekError
		if due != nil || !errors.As(err, &provider) || provider.Kind != DeepSeekErrorMalformed || provider.Operation != "extract items" || provider.Detail != tc.detail {
			t.Fatalf("due=%v err=%+v", due, err)
		}
		if strings.Contains(err.Error(), "synthetic-private-date") {
			t.Fatal("adapter exposed supplied text")
		}
	}
}

// TestApplicationDueFrozenAcrossRetryAndReopen retains the first parsed date through a durable retry.
func TestApplicationDueFrozenAcrossRetryAndReopen(t *testing.T) {
	location, err := time.LoadLocation(deepSeekTestTimeZone)
	if err != nil {
		t.Fatal(err)
	}
	clock := &adjustableClock{now: time.Date(2026, 3, 7, 23, 30, 0, 0, location)}
	path := filepath.Join(t.TempDir(), "dates.db")
	store, err := openStore(context.Background(), path, clock.Time)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	receipt := saveQueueRecording(t, store, "Synthetic tomorrow deadline")
	requests := 0
	client := newDeepSeekFixture(t, clock.Time(), func(*http.Request) (*http.Response, error) {
		requests++
		return deepSeekFixtureOutput("date-response", `{"items":[{"kind":"task","title":"Due tomorrow","content":"","due_at":"2026-03-08","all_day":true,"priority":0,"tags":[],"project_alias":null}]}`), nil
	})
	client.now = clock.Time
	delivery := &fakeDeliverer{createResults: []deliveryResult{{err: &TickTickError{Kind: TickTickErrorRetryable, Operation: "create task"}}, {created: TickTickCreatedTask{ID: "created", ProjectID: "project"}}}}
	worker := newTestWorkerWithTimeZone(t, store, client, delivery, deepSeekTestTimeZone)
	runWorkerOnce(t, worker)
	runWorkerOnce(t, worker)
	if len(delivery.taskInputs) != 1 {
		t.Fatalf("first deliveries=%d", len(delivery.taskInputs))
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	clock.Advance(72 * time.Hour)
	reopened, err := openStore(context.Background(), path, clock.Time)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := newTestWorkerWithTimeZone(t, reopened, client, delivery, deepSeekTestTimeZone)
	runWorkerOnce(t, restarted)
	if requests != 1 || len(delivery.taskInputs) != 2 {
		t.Fatalf("extractions=%d deliveries=%d", requests, len(delivery.taskInputs))
	}
	want := time.Date(2026, 3, 8, 0, 0, 0, 0, location)
	for _, input := range delivery.taskInputs {
		if input.Due == nil || !input.Due.Equal(want) || !input.AllDay || input.TimeZone != deepSeekTestTimeZone {
			t.Fatalf("date changed across DST/restart: %+v", input)
		}
	}
	status, err := reopened.RecordingStatus(context.Background(), receipt.ID)
	if err != nil || status.State != "complete" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}
