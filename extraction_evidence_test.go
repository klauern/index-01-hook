package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

func TestEvaluationCaptureConfig(t *testing.T) {
	for _, test := range []struct {
		days, interval string
		wantDays       int
		wantInterval   time.Duration
		invalid        bool
	}{
		{wantDays: 0}, {days: "90", wantDays: 90, wantInterval: 6 * time.Hour},
		{days: "90", interval: "0", wantDays: 90}, {days: "365", interval: "24h", wantDays: 365, wantInterval: 24 * time.Hour},
		{days: "-1", invalid: true}, {days: "366", invalid: true}, {days: "1.5", invalid: true},
		{days: "90", interval: "30m", invalid: true}, {days: "90", interval: "169h", invalid: true},
	} {
		t.Run(test.days+"/"+test.interval, func(t *testing.T) {
			env := validConfigEnv()
			env["INDEX01_EVALUATION_RETENTION_DAYS"], env["INDEX01_EVALUATION_POLL_INTERVAL"] = test.days, test.interval
			cfg, err := LoadConfig(func(key string) string { return env[key] })
			if test.invalid {
				if err == nil {
					t.Fatal("invalid capture configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.EvaluationRetention != time.Duration(test.wantDays)*24*time.Hour || cfg.EvaluationPollInterval != test.wantInterval {
				t.Fatalf("capture durations = %s, %s", cfg.EvaluationRetention, cfg.EvaluationPollInterval)
			}
		})
	}
}

func TestEvaluationCaptureMatchesActualModelRequest(t *testing.T) {
	clock := time.Date(2026, 9, 8, 18, 30, 0, 0, time.UTC)
	for _, enabled := range []bool{false, true} {
		var sent deepSeekRequest
		output := `{"items":[` + validDeepSeekTask("Synthetic original task") + `]}`
		calls := 0
		client, err := NewDeepSeekClientWithConfig(deepSeekTestToken, roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(request.Body).Decode(&sent); err != nil {
				t.Fatal(err)
			}
			return deepSeekFixtureOutput("synthetic-response", output), nil
		}), func() time.Time { calls++; return clock }, DeepSeekClientConfig{Model: defaultDeepSeekModel, TimeZone: "America/Chicago", CaptureEvidence: enabled})
		if err != nil {
			t.Fatal(err)
		}
		result, err := client.Extract(context.Background(), "Original recording words", []string{"work"})
		if err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("processing clock read %d times", calls)
		}
		if !enabled {
			if result.Evidence != nil {
				t.Fatal("capture unexpectedly enabled")
			}
			continue
		}
		e := result.Evidence
		if e == nil || !e.Clock.Equal(clock) || e.TimeZone != "America/Chicago" || e.SystemPrompt != sent.Input[0].Content || string(e.SavedOutput) != output {
			t.Fatal("capture differs from request or independent output")
		}
		schema, err := json.Marshal(sent.Text.Format.Schema)
		if err != nil {
			t.Fatal(err)
		}
		if string(e.Schema) != string(schema) || e.PromptSHA256 != evidenceHash([]byte(e.SystemPrompt)) || e.SchemaSHA256 != evidenceHash(schema) {
			t.Fatal("capture identity differs from request")
		}
	}
}

func TestEvaluationWorkerFreezesRoutingWithProcessingClock(t *testing.T) {
	store, _ := configureEvidenceStore(t, 90*24*time.Hour)
	saveQueueRecording(t, store, "Original recording words")
	clock := time.Date(2026, 9, 8, 19, 0, 0, 0, time.UTC)
	extractor := &fakeExtractor{results: []extractionResult{{value: FrozenExtraction{Provider: "deepseek", Model: "synthetic-model", Items: []QueuedItem{}, Evidence: &ExtractionEvidence{Clock: clock, TimeZone: "UTC", SavedOutput: json.RawMessage(`{"items":[]}`)}}}}}
	worker := newTestWorker(t, store, extractor, &fakeDeliverer{})
	worker.config.EvidenceRouting = &evalcorpus.RoutingConfig{Aliases: map[string]string{"work": "work-project"}, DefaultProjectID: "inbox", NoteProjectID: "notes"}
	runWorkerOnce(t, worker)
	worker.config.EvidenceRouting.Aliases["work"] = "changed-later"
	entries := readEvidence(t, store)
	if len(entries) != 1 || entries[0].Extraction == nil || entries[0].Extraction.Evidence == nil {
		t.Fatal("worker lost extraction evidence")
	}
	routing := entries[0].Extraction.Evidence.Routing
	if routing == nil || routing.Clock != clock.Format(time.RFC3339Nano) || routing.Aliases["work"] != "work-project" || routing.DefaultProjectID != "inbox" {
		t.Fatal("worker did not freeze routing configuration")
	}
}
