package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOperationalStatusReportsHealthyWorkerAndProviderLatency(t *testing.T) {
	store, _ := newQueueStore(t)
	startHealthyWorker(t, store, "health-worker")
	if err := store.RecordProviderLatency(context.Background(), "health-worker", "deepseek", 1200*time.Millisecond, false); err != nil {
		t.Fatalf("RecordProviderLatency(deepseek) error = %v", err)
	}
	if err := store.RecordProviderLatency(context.Background(), "health-worker", "ticktick", 2300*time.Millisecond, true); err != nil {
		t.Fatalf("RecordProviderLatency(ticktick) error = %v", err)
	}

	status, err := store.OperationalStatus(context.Background())
	if err != nil {
		t.Fatalf("OperationalStatus() error = %v", err)
	}
	if status.Status != "ok" || len(status.Reasons) != 0 || status.Worker.State != "running" || status.Worker.Stale {
		t.Fatalf("healthy status = %+v", status)
	}
	if status.Providers.DeepSeek.LastLatencyMilliseconds != 1200 || status.Providers.DeepSeek.LastFailed {
		t.Errorf("DeepSeek status = %+v", status.Providers.DeepSeek)
	}
	if status.Providers.TickTick.LastLatencyMilliseconds != 2300 || !status.Providers.TickTick.LastFailed {
		t.Errorf("TickTick status = %+v", status.Providers.TickTick)
	}
}

func TestOperationalStatusReportsQueueFailureStates(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*testing.T, *Store, *adjustableClock, int64)
		reason     string
		assert     func(*testing.T, QueueOperationalStatus)
	}{
		{
			name: "delayed",
			transition: func(t *testing.T, store *Store, clock *adjustableClock, _ int64) {
				clock.Advance(queueDelayedAfter + time.Second)
				completeHealthCycle(t, store, "health-worker")
			},
			reason: "queue_delayed",
			assert: func(t *testing.T, queue QueueOperationalStatus) {
				if queue.OldestActiveAgeSeconds <= int64(queueDelayedAfter/time.Second) {
					t.Errorf("oldest queue age = %d", queue.OldestActiveAgeSeconds)
				}
			},
		},
		{
			name: "retry",
			transition: func(t *testing.T, store *Store, clock *adjustableClock, recordingID int64) {
				claimQueueExtraction(t, store, recordingID)
				if err := store.RetryExtraction(context.Background(), recordingID, "queue-worker", OutcomeRetryable, clock.Time().Add(time.Minute)); err != nil {
					t.Fatalf("RetryExtraction() error = %v", err)
				}
			},
			reason: "",
			assert: func(t *testing.T, queue QueueOperationalStatus) {
				if queue.Retries != 1 {
					t.Errorf("retry count = %d, want 1", queue.Retries)
				}
			},
		},
		{
			name: "blocked authentication",
			transition: func(t *testing.T, store *Store, _ *adjustableClock, recordingID int64) {
				claimQueueExtraction(t, store, recordingID)
				if err := store.BlockExtraction(context.Background(), recordingID, "queue-worker", OutcomeAuthentication, "blocked_auth"); err != nil {
					t.Fatalf("BlockExtraction() error = %v", err)
				}
			},
			reason: "blocked_authentication",
			assert: func(t *testing.T, queue QueueOperationalStatus) {
				if queue.BlockedAuthentication != 1 {
					t.Errorf("blocked authentication count = %d, want 1", queue.BlockedAuthentication)
				}
			},
		},
		{
			name: "review",
			transition: func(t *testing.T, store *Store, _ *adjustableClock, recordingID int64) {
				claimQueueExtraction(t, store, recordingID)
				if err := store.BlockExtraction(context.Background(), recordingID, "queue-worker", OutcomeReview, "needs_review"); err != nil {
					t.Fatalf("BlockExtraction() error = %v", err)
				}
			},
			reason: "needs_review",
			assert: func(t *testing.T, queue QueueOperationalStatus) {
				if queue.NeedsReview != 1 {
					t.Errorf("review count = %d, want 1", queue.NeedsReview)
				}
			},
		},
		{
			name: "dead letter",
			transition: func(t *testing.T, store *Store, _ *adjustableClock, recordingID int64) {
				claimQueueExtraction(t, store, recordingID)
				if err := store.BlockExtraction(context.Background(), recordingID, "queue-worker", OutcomeMalformed, "dead_letter"); err != nil {
					t.Fatalf("BlockExtraction() error = %v", err)
				}
			},
			reason: "dead_letter",
			assert: func(t *testing.T, queue QueueOperationalStatus) {
				if queue.DeadLetter != 1 {
					t.Errorf("dead-letter count = %d, want 1", queue.DeadLetter)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newQueueStore(t)
			startHealthyWorker(t, store, "health-worker")
			receipt := saveQueueRecording(t, store, "private queue content")
			test.transition(t, store, clock, receipt.ID)
			status, err := store.OperationalStatus(context.Background())
			if err != nil {
				t.Fatalf("OperationalStatus() error = %v", err)
			}
			test.assert(t, status.Queue)
			if test.reason == "" {
				if status.Status != "ok" {
					t.Fatalf("status = %+v, want ok", status)
				}
				return
			}
			if status.Status != "degraded" || !hasReason(status.Reasons, test.reason) {
				t.Fatalf("status = %+v, want reason %q", status, test.reason)
			}
		})
	}
}

func TestOperationalStatusReportsStoppedStaleFailedAndSlowWorker(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *Store, *adjustableClock)
		reason string
	}{
		{
			name: "stopped",
			change: func(t *testing.T, store *Store, _ *adjustableClock) {
				if err := store.WorkerStopped(context.Background(), "health-worker"); err != nil {
					t.Fatalf("WorkerStopped() error = %v", err)
				}
			},
			reason: "worker_stopped",
		},
		{
			name: "stale",
			change: func(_ *testing.T, _ *Store, clock *adjustableClock) {
				clock.Advance(workerStaleAfter + time.Second)
			},
			reason: "worker_stale",
		},
		{
			name: "failed cycle",
			change: func(t *testing.T, store *Store, _ *adjustableClock) {
				if err := store.WorkerCycleStarted(context.Background(), "health-worker"); err != nil {
					t.Fatalf("WorkerCycleStarted() error = %v", err)
				}
				if err := store.WorkerCycleCompleted(context.Background(), "health-worker", true); err != nil {
					t.Fatalf("WorkerCycleCompleted() error = %v", err)
				}
			},
			reason: "worker_cycle_failed",
		},
		{
			name: "slow provider",
			change: func(t *testing.T, store *Store, _ *adjustableClock) {
				if err := store.RecordProviderLatency(context.Background(), "health-worker", "deepseek", providerSlowAfter+time.Second, false); err != nil {
					t.Fatalf("RecordProviderLatency() error = %v", err)
				}
			},
			reason: "deepseek_slow",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newQueueStore(t)
			startHealthyWorker(t, store, "health-worker")
			test.change(t, store, clock)
			status, err := store.OperationalStatus(context.Background())
			if err != nil {
				t.Fatalf("OperationalStatus() error = %v", err)
			}
			if status.Status != "degraded" || !hasReason(status.Reasons, test.reason) {
				t.Fatalf("status = %+v, want reason %q", status, test.reason)
			}
		})
	}
}

func TestStatuszReturnsSafeAggregateData(t *testing.T) {
	store, handler, _ := newTestApp(t, 1<<20)
	startHealthyWorker(t, store, "private-worker-owner")
	_, err := store.SaveRecording(context.Background(), RecordingInput{
		RecordedAtMillis: 1760000000000,
		Client:           "private-client",
		Trigger:          "private-trigger",
		Transcription:    "private transcript and token-value",
		AudioFilename:    "private-audio.m4a",
		Fingerprint:      strings.Repeat("d", 64),
	})
	if err != nil {
		t.Fatalf("SaveRecording() error = %v", err)
	}
	rr := send(t, handler, httptest.NewRequest(http.MethodGet, "/statusz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var status OperationalStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Queue.Active != 1 {
		t.Errorf("active queue count = %d, want 1", status.Queue.Active)
	}
	if status.Intake.RetainedUniqueRecordings != 1 || status.Intake.RetainedReceiveCount != 1 || status.Intake.LastResponseStatus != http.StatusAccepted {
		t.Errorf("intake status = %+v, want one accepted recording", status.Intake)
	}
	if status.Intake.LastReceivedAt == "" || status.Intake.LastDuplicate {
		t.Errorf("latest intake status = %+v, want a non-duplicate timestamp", status.Intake)
	}
	for _, privateValue := range []string{
		"private-worker-owner", "private-client", "private-trigger", "private transcript",
		"token-value", "private-audio.m4a", strings.Repeat("d", 64),
	} {
		if strings.Contains(rr.Body.String(), privateValue) {
			t.Errorf("status contains private value %q: %s", privateValue, rr.Body.String())
		}
	}
}

func TestReadyzRequiresBearerTokenAndReturnsSafeStatus(t *testing.T) {
	store, handler, _ := newTestApp(t, 1<<20)
	startHealthyWorker(t, store, "private-worker-owner")
	input := RecordingInput{
		RecordedAtMillis: 1760000000000,
		Client:           "private-client",
		Trigger:          "private-trigger",
		Transcription:    "",
		AudioFilename:    "private-audio.m4a",
		Fingerprint:      strings.Repeat("e", 64),
	}
	if _, err := store.SaveRecording(context.Background(), input); err != nil {
		t.Fatalf("first SaveRecording() error = %v", err)
	}
	if _, err := store.SaveRecording(context.Background(), input); err != nil {
		t.Fatalf("duplicate SaveRecording() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	rr := send(t, handler, request)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var status OperationalStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode readiness status: %v", err)
	}
	if status.Intake.RetainedUniqueRecordings != 1 || status.Intake.RetainedReceiveCount != 2 || !status.Intake.LastDuplicate || status.Intake.LastResponseStatus != http.StatusOK {
		t.Errorf("intake status = %+v, want duplicate response summary", status.Intake)
	}
	for _, privateValue := range []string{
		"private-worker-owner", "private-client", "private-trigger", "private-audio.m4a",
		strings.Repeat("e", 64), testToken,
	} {
		if strings.Contains(rr.Body.String(), privateValue) {
			t.Errorf("readiness status contains private value %q: %s", privateValue, rr.Body.String())
		}
	}
}

func TestReadyzRejectsInvalidAuthorizationWithoutLoggingValues(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, handler, _ := newTestAppWithLogger(t, 1<<20, logger)

	tests := []struct {
		name       string
		configure  func(*http.Request)
		wantReason string
		sensitive  string
	}{
		{name: "missing", configure: func(*http.Request) {}, wantReason: "missing_authorization"},
		{name: "invalid", configure: func(r *http.Request) { r.Header.Set("Authorization", "Bearer status-secret") }, wantReason: "invalid_authorization", sensitive: "status-secret"},
		{name: "duplicate", configure: func(r *http.Request) {
			r.Header.Add("Authorization", "Bearer first-secret")
			r.Header.Add("Authorization", "Bearer second-secret")
		}, wantReason: "duplicate_authorization", sensitive: "second-secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logs.Reset()
			request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			test.configure(request)
			rr := send(t, handler, request)
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401; body=%s", rr.Code, rr.Body.String())
			}
			if !strings.Contains(logs.String(), `"msg":"rejected readiness check"`) || !strings.Contains(logs.String(), `"reason_code":"`+test.wantReason+`"`) {
				t.Errorf("readiness rejection log = %q", logs.String())
			}
			for _, value := range []string{test.sensitive, testToken, "first-secret"} {
				if value != "" && strings.Contains(logs.String(), value) {
					t.Errorf("readiness rejection log contains sensitive value %q", value)
				}
			}
		})
	}
}

func TestStatuszReturnsServiceUnavailableForDegradedStatus(t *testing.T) {
	_, handler, _ := newTestApp(t, 1<<20)
	rr := send(t, handler, httptest.NewRequest(http.MethodGet, "/statusz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var status OperationalStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Status != "degraded" || !hasReason(status.Reasons, "worker_missing") {
		t.Fatalf("status = %+v, want worker_missing", status)
	}
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	request.Header.Set("Authorization", "Bearer "+testToken)
	rr = send(t, handler, request)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func startHealthyWorker(t *testing.T, store *Store, owner string) {
	t.Helper()
	if err := store.WorkerStarted(context.Background(), owner); err != nil {
		t.Fatalf("WorkerStarted() error = %v", err)
	}
	completeHealthCycle(t, store, owner)
}

func completeHealthCycle(t *testing.T, store *Store, owner string) {
	t.Helper()
	if err := store.WorkerCycleStarted(context.Background(), owner); err != nil {
		t.Fatalf("WorkerCycleStarted() error = %v", err)
	}
	if err := store.WorkerCycleCompleted(context.Background(), owner, false); err != nil {
		t.Fatalf("WorkerCycleCompleted() error = %v", err)
	}
}

func claimQueueExtraction(t *testing.T, store *Store, recordingID int64) {
	t.Helper()
	claim, err := store.ClaimExtraction(context.Background(), "queue-worker", time.Minute)
	if err != nil || claim == nil || claim.RecordingID != recordingID {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
}

func hasReason(reasons []string, expected string) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}
