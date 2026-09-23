package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const evaluationFixturePath = "testdata/evaluation/scenarios.json"

type evaluationTelemetry struct {
	Calls       int              `json:"calls"`
	LatencyMS   int64            `json:"latency_ms"`
	ResponseIDs []string         `json:"response_ids"`
	Models      []string         `json:"provider_models"`
	Usage       []map[string]int `json:"usage"`
}

type evaluationResult struct {
	Suite            string                `json:"suite"`
	Case             string                `json:"case"`
	Split            string                `json:"split"`
	Adapter          string                `json:"adapter"`
	Trial            int                   `json:"trial"`
	Clock            string                `json:"clock"`
	TimeZone         string                `json:"time_zone"`
	Aliases          []string              `json:"aliases"`
	HistoryLimit     int                   `json:"history_limit"`
	Model            string                `json:"model"`
	MaxOutputTokens  int                   `json:"max_output_tokens"`
	Generations      int                   `json:"model_generations"`
	JudgeRepetitions int                   `json:"judge_repetitions"`
	Telemetry        evaluationTelemetry   `json:"telemetry"`
	Observation      evaluationObservation `json:"observation"`
	Score            evaluationScore       `json:"score"`
}

type evaluationReport struct {
	Version          int                       `json:"version"`
	Suite            string                    `json:"suite"`
	Mode             string                    `json:"mode"`
	FixtureSHA256    string                    `json:"fixture_sha256"`
	PromptSchemaSHA  string                    `json:"prompt_schema_source_sha256"`
	CallBudget       int                       `json:"call_budget"`
	ApplicationCache string                    `json:"application_cache"`
	ProviderCache    string                    `json:"provider_cache"`
	Counts           map[string]int            `json:"counts"`
	Results          []evaluationResult        `json:"results"`
	DeliveryChecks   []evaluationDeliveryCheck `json:"delivery_checks"`
}

type evaluationDeliveryCheck struct {
	Case         string `json:"case"`
	Adapter      string `json:"adapter"`
	Trial        int    `json:"trial"`
	Clock        string `json:"clock"`
	Source       string `json:"source"`
	SourceSHA256 string `json:"source_sha256"`
	Status       string `json:"status"`
}

func evaluationHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func newEvaluationReport(t *testing.T, suite, mode string) evaluationReport {
	t.Helper()
	return evaluationReport{
		Version: evaluationVersion, Suite: suite, Mode: mode,
		FixtureSHA256: evaluationHash(t, evaluationFixturePath), PromptSchemaSHA: evaluationHash(t, "deepseek.go"),
		ApplicationCache: "none; each live trial calls Extract", ProviderCache: "not controlled; usage records cache tokens when supplied",
		Counts: map[string]int{"pass": 0, "fail": 0, "error": 0, "skip": 0, "unsupported": 0}, Results: []evaluationResult{}, DeliveryChecks: []evaluationDeliveryCheck{},
	}
}

func (r *evaluationReport) add(result evaluationResult) {
	r.Counts[result.Score.Status]++
	r.Results = append(r.Results, result)
}

func saveEvaluationReport(t *testing.T, report evaluationReport) {
	t.Helper()
	dir := os.Getenv("INDEX01_EVAL_REPORT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, report.Suite+"-"+report.Mode+".json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func evaluationCases(t *testing.T) []evaluationCase {
	t.Helper()
	cases, err := loadEvaluationCases(evaluationFixturePath)
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func evaluationRouter(c evaluationCase) (*TickTickRouter, error) {
	projects := []map[string]any{{"id": "notes", "kind": "NOTE", "closed": false, "permission": nil}}
	aliases := map[string]string{}
	for _, alias := range c.Aliases {
		aliases[alias] = alias
		projects = append(projects, map[string]any{"id": alias, "kind": "TASK", "closed": false, "permission": nil})
	}
	data, err := json.Marshal(projects)
	if err != nil {
		return nil, err
	}
	client, err := NewTickTickClient("https://api.ticktick.test/open/v1", "synthetic", &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet || req.URL.String() != "https://api.ticktick.test/open/v1/project" {
				return nil, errors.New("evaluation forbids TickTick network operations")
			}
			return fixtureResponse(http.StatusOK, string(data)), nil
		}),
	})
	if err != nil {
		return nil, err
	}
	return client.ValidateRouting(context.Background(), TickTickRoutingConfig{
		DefaultProjectID: "inbox", NoteProjectID: "notes", Aliases: aliases,
	})
}

func evaluationProductionActions(extraction FrozenExtraction, router *TickTickRouter, step int) ([]evaluationAction, error) {
	actions := []evaluationAction{}
	for index, queued := range extraction.Items {
		route, err := router.ResolveItemProject(queued.Kind, queued.ProjectAlias)
		if err != nil {
			return nil, err
		}
		item := evaluationItem{
			ID: fmt.Sprintf("s%d-i%d", step, index+1), Kind: string(queued.Kind), Title: queued.Title,
			Content: queued.Content, Route: route, AllDay: queued.AllDay, Priority: queued.Priority,
			Tags: append([]string{}, queued.Tags...),
		}
		if queued.Due != nil {
			item.Due = queued.Due.Format(time.RFC3339)
		}
		actions = append(actions, evaluationAction{Step: step, Op: "create", Target: item.ID, Item: &item})
	}
	return actions, nil
}

func evaluationLiveTransport(telemetry *evaluationTelemetry, transport http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != deepSeekResponsesURL {
			return nil, errors.New("unexpected live evaluation destination")
		}
		telemetry.Calls++
		start := time.Now()
		response, err := transport.RoundTrip(req)
		if err != nil {
			telemetry.LatencyMS += time.Since(start).Milliseconds()
			return nil, errors.New("provider transport failed")
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, deepSeekMaxResponseBytes+1))
		_ = response.Body.Close()
		telemetry.LatencyMS += time.Since(start).Milliseconds()
		if readErr != nil {
			return nil, errors.New("provider response read failed")
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		// Retain allowlisted numeric metadata only. Never retain an error body.
		if response.StatusCode == http.StatusOK && len(body) <= deepSeekMaxResponseBytes {
			var envelope struct {
				ID    string                     `json:"id"`
				Model string                     `json:"model"`
				Usage map[string]json.RawMessage `json:"usage"`
			}
			if json.Unmarshal(body, &envelope) == nil {
				if safeProviderIdentifier(envelope.ID) {
					telemetry.ResponseIDs = append(telemetry.ResponseIDs, envelope.ID)
				}
				if safeProviderIdentifier(envelope.Model) {
					telemetry.Models = append(telemetry.Models, envelope.Model)
				}
				usage := map[string]int{}
				for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", "prompt_tokens", "completion_tokens", "prompt_cache_hit_tokens", "prompt_cache_miss_tokens"} {
					var value int
					if raw, ok := envelope.Usage[key]; ok && json.Unmarshal(raw, &value) == nil && value >= 0 {
						usage[key] = value
					}
				}
				if len(usage) > 0 {
					telemetry.Usage = append(telemetry.Usage, usage)
				}
			}
		}
		return response, nil
	})
}

func runEvaluationCase(c evaluationCase, trial int, live bool) evaluationResult {
	r := evaluationResult{
		Suite: c.Suite, Case: c.ID, Split: c.Split, Adapter: c.Adapter, Trial: trial,
		Clock: c.Clock, TimeZone: c.TimeZone, Aliases: c.Aliases, HistoryLimit: c.HistoryLimit,
		Model: "scripted", Telemetry: evaluationTelemetry{ResponseIDs: []string{}, Models: []string{}, Usage: []map[string]int{}},
		Observation: evaluationObservation{Outcome: "ok", Actions: []evaluationAction{}, State: copyEvaluationState(c.Initial), Visible: [][]string{}},
	}
	o := &r.Observation
	finish := func() evaluationResult { r.Score = scoreEvaluation(c, *o); return r }
	if err := validateEvaluationCase(c); err != nil {
		o.Outcome, o.Reason = "error", "invalid fixture"
		return finish()
	}
	if live && (c.Suite != "current" || !c.Live) {
		o.Outcome, o.Reason = "unsupported", "live adapter is unavailable for this case"
		return finish()
	}
	clock, _ := time.Parse(time.RFC3339, c.Clock)
	history := slices.Clone(c.History)
	for step, turn := range c.Turns {
		history = append(history, turn.Message)
		visible := history[max(0, len(history)-c.HistoryLimit):]
		ids := []string{}
		for _, message := range visible {
			ids = append(ids, message.ID)
		}
		o.Visible = append(o.Visible, ids)
		if turn.RemoteState != nil {
			o.State = copyEvaluationState(*turn.RemoteState)
		}
		var actions []evaluationAction
		if c.Suite == "current" {
			r.Model, r.MaxOutputTokens = deepSeekModel, deepSeekMaxOutputTokens
			transport := http.RoundTripper(roundTripFunc(func(req *http.Request) (*http.Response, error) {
				r.Telemetry.Calls++
				var request deepSeekRequest
				if req.URL.String() != deepSeekResponsesURL || req.Method != http.MethodPost || json.NewDecoder(req.Body).Decode(&request) != nil ||
					len(request.Input) != 2 || request.Input[1].Content != turn.Message.Text {
					return nil, errors.New("unexpected production extraction request")
				}
				location, _ := time.LoadLocation(c.TimeZone)
				if !strings.Contains(request.Input[0].Content, "Current local time is "+clock.In(location).Format(time.RFC3339)) ||
					!strings.Contains(request.Input[0].Content, "The time zone is "+c.TimeZone) {
					return nil, errors.New("production prompt uses the wrong date or time zone")
				}
				return deepSeekFixtureOutput("synthetic-evaluation", string(turn.Output)), nil
			}))
			token := "synthetic"
			if live {
				transport, token = evaluationLiveTransport(&r.Telemetry, http.DefaultTransport), os.Getenv("INDEX01_DEEPSEEK_TOKEN")
			}
			client, err := NewDeepSeekClientWithConfig(token, transport, func() time.Time { return clock }, DeepSeekClientConfig{Model: deepSeekModel, TimeZone: c.TimeZone})
			if err != nil {
				o.Outcome, o.Reason = "error", "client_configuration"
				return finish()
			}
			extraction, err := client.Extract(context.Background(), turn.Message.Text, c.Aliases)
			if live {
				r.Generations = r.Telemetry.Calls
			}
			if err != nil {
				var providerErr *DeepSeekError
				o.Outcome, o.Reason = "error", "provider_error"
				if errors.As(err, &providerErr) {
					o.Reason = string(providerErr.Kind)
				}
				return finish()
			}
			router, err := evaluationRouter(c)
			if err == nil {
				actions, err = evaluationProductionActions(extraction, router, step+1)
			}
			if err != nil {
				o.Outcome, o.Reason = "error", "route_configuration"
				return finish()
			}
		} else {
			// This adapter replays saved proposals. It does not infer intent from text.
			if err := decodeEvaluation(turn.Output, &actions); err != nil || actions == nil {
				o.Outcome, o.Reason = "error", "invalid scripted actions"
				return finish()
			}
		}
		for _, action := range actions {
			o.Actions = append(o.Actions, action)
			var err error
			o.State, err = applyEvaluationAction(o.State, action)
			if err != nil {
				o.Outcome, o.Reason = "error", "invalid simulated action: "+err.Error()
				return finish()
			}
		}
	}
	return finish()
}

func TestEvaluationOffline(t *testing.T) {
	cases := evaluationCases(t)
	for _, suite := range []string{"current", "proposed"} {
		t.Run(suite, func(t *testing.T) {
			report := newEvaluationReport(t, suite, "offline")
			defer func() { saveEvaluationReport(t, report) }()
			for _, c := range cases {
				if c.Suite != suite {
					continue
				}
				t.Run(c.ID, func(t *testing.T) {
					result := runEvaluationCase(c, 1, false)
					report.add(result)
					if result.Score.Status != "pass" {
						t.Errorf("%s: %v", result.Score.Status, result.Score.Diagnostics)
					}
				})
			}
			if suite == "current" {
				for _, check := range []struct {
					name   string
					source string
					run    func(*testing.T)
				}{
					{"delivery-exact-replay", "item_acceptance_test.go", TestItemAcceptanceExactReplayMakesNoDuplicateProviderCalls},
					{"delivery-restart", "item_acceptance_test.go", TestItemAcceptanceRestartDoesNotRepeatCompletedSibling},
					{"delivery-ambiguous-note", "worker_test.go", TestWorkerReconcilesAmbiguousNoteCreationByStoredKind},
					{"delivery-bounded-retry", "worker_test.go", TestWorkerDeadLettersAfterBoundedRetry},
				} {
					status := "pass"
					if !t.Run(check.name, func(t *testing.T) {
						defer func() {
							if t.Skipped() {
								status = "skip"
							}
						}()
						check.run(t)
					}) {
						status = "fail"
					}
					report.Counts[status]++
					report.DeliveryChecks = append(report.DeliveryChecks, evaluationDeliveryCheck{
						Case: check.name, Adapter: "production-worker-v1", Trial: 1, Clock: "2026-08-12T12:00:00Z",
						Source: check.source, SourceSHA256: evaluationHash(t, check.source), Status: status,
					})
				}
			}
			t.Logf("%s counts: %v", suite, report.Counts)
		})
	}
}

func evaluationLiveSettings(trialsRaw, budgetRaw string, callsPerTrial int) (int, int, error) {
	trials, err := strconv.Atoi(trialsRaw)
	if err != nil || trials < 1 || trials > 10 {
		return 0, 0, errors.New("INDEX01_EVAL_TRIALS must be between 1 and 10")
	}
	budget, err := strconv.Atoi(budgetRaw)
	if err != nil || budget < 1 || budget > 30 || callsPerTrial < 1 || trials*callsPerTrial > budget {
		return 0, 0, errors.New("INDEX01_EVAL_CALL_BUDGET must cover the run and be at most 30")
	}
	return trials, budget, nil
}

func TestEvaluationLive(t *testing.T) {
	if os.Getenv("INDEX01_RUN_LIVE_EVALUATION") != "1" {
		t.Skip("set INDEX01_RUN_LIVE_EVALUATION=1 to enable synthetic model trials")
	}
	cases := []evaluationCase{}
	calls := 0
	for _, c := range evaluationCases(t) {
		if c.Live && c.Suite == "current" {
			cases = append(cases, c)
			calls += len(c.Turns)
		}
	}
	trials, budget, err := evaluationLiveSettings(os.Getenv("INDEX01_EVAL_TRIALS"), os.Getenv("INDEX01_EVAL_CALL_BUDGET"), calls)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("INDEX01_DEEPSEEK_TOKEN") == "" {
		t.Fatal("INDEX01_DEEPSEEK_TOKEN is required")
	}
	report := newEvaluationReport(t, "current", "live")
	report.CallBudget = budget
	defer func() { saveEvaluationReport(t, report) }()
	stopped := false
	for trial := 1; trial <= trials; trial++ {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/trial-%d", c.ID, trial), func(t *testing.T) {
				if stopped {
					result := evaluationResult{Suite: c.Suite, Case: c.ID, Split: c.Split, Adapter: c.Adapter, Trial: trial,
						Clock: c.Clock, TimeZone: c.TimeZone, Aliases: c.Aliases, HistoryLimit: c.HistoryLimit, Model: deepSeekModel,
						Observation: evaluationObservation{Outcome: "skip", Reason: "stopped after provider error"}}
					result.Score = scoreEvaluation(c, result.Observation)
					report.add(result)
					t.Skip("stopped after provider error")
				}
				result := runEvaluationCase(c, trial, true)
				report.add(result)
				if result.Score.Status == "error" {
					stopped = true
				}
				if result.Score.Status != "pass" {
					t.Errorf("%s: %v", result.Score.Status, result.Score.Diagnostics)
				}
			})
		}
	}
}
