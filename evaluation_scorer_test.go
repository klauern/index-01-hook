package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cloneEvaluation[T any](t *testing.T, value T) T {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var copy T
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func TestEvaluationScorerRejectsWrongOutputs(t *testing.T) {
	cases := evaluationCases(t)
	find := func(id string) evaluationCase {
		for _, c := range cases {
			if c.ID == id {
				return cloneEvaluation(t, c)
			}
		}
		t.Fatalf("missing fixture %s", id)
		return evaluationCase{}
	}
	for _, test := range []struct {
		name   string
		id     string
		change func(*evaluationObservation)
	}{
		{"extra_item", "task-fallback", func(o *evaluationObservation) {
			a := cloneEvaluation(t, o.Actions[0])
			a.Target = "extra"
			a.Item.ID = "extra"
			o.Actions = append(o.Actions, a)
			o.State = append(o.State, *a.Item)
		}},
		{"missing_item", "task-fallback", func(o *evaluationObservation) { o.Actions = []evaluationAction{}; o.State = []evaluationItem{} }},
		{"wrong_route", "task-fallback", func(o *evaluationObservation) { o.Actions[0].Item.Route = "work"; o.State[0].Route = "work" }},
		{"duplicate_action", "task-fallback", func(o *evaluationObservation) { o.Actions = append(o.Actions, o.Actions[0]) }},
		{"duplicate_state", "task-fallback", func(o *evaluationObservation) { o.State = append(o.State, o.State[0]) }},
		{"wrong_kind", "task-fallback", func(o *evaluationObservation) { o.Actions[0].Item.Kind = "note"; o.State[0].Kind = "note" }},
		{"invented_content_exact_check", "task-fallback", func(o *evaluationObservation) {
			o.Actions[0].Item.Content = "Invented detail"
			o.State[0].Content = "Invented detail"
		}},
		{"wrong_due", "relative-date-processing-clock", func(o *evaluationObservation) {
			o.Actions[0].Item.Due = "2026-09-07T00:00:00-05:00"
			o.State[0].Due = o.Actions[0].Item.Due
		}},
		{"wrong_target", "close-explicit-task", func(o *evaluationObservation) {
			o.Actions[0].Target = "task-b"
			o.State[0].Closed = false
			o.State[1].Closed = true
		}},
		{"forbidden_closure", "negated-closure", func(o *evaluationObservation) {
			o.Actions = []evaluationAction{{Step: 1, Op: "close", Target: "task-a"}}
			o.State[0].Closed = true
		}},
		{"unexpected_no_action_mutation", "unrelated-no-action", func(o *evaluationObservation) {
			o.Actions = []evaluationAction{{Step: 1, Op: "update", Target: "task-a", Fields: map[string]string{"title": "Changed"}}}
			o.State[0].Title = "Changed"
		}},
		{"out_of_order", "interleaved-topics", func(o *evaluationObservation) { o.Actions[0], o.Actions[2] = o.Actions[2], o.Actions[0] }},
		{"wrong_history_boundary", "history-11-message-boundary", func(o *evaluationObservation) { o.Visible[0] = append([]string{"h0"}, o.Visible[0]...) }},
		{"missing_remote_snapshot", "stale-remote-state", func(o *evaluationObservation) { o.State = cloneEvaluation(t, find("stale-remote-state").Initial) }},
		{"final_state_disagrees_with_action", "update-explicit-target", func(o *evaluationObservation) { o.State[0].Title = "Send estimate" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := find(test.id)
			o := runEvaluationCase(c, 1, false).Observation
			test.change(&o)
			score := scoreEvaluation(c, o)
			if score.Status != "fail" || score.FailureCount == 0 {
				t.Fatalf("wrong output was not rejected: %+v", score)
			}
		})
	}
}

func TestEvaluationScorerPreservesInvalidAndNonPassingStates(t *testing.T) {
	c := evaluationCases(t)[0]
	good := runEvaluationCase(c, 1, false).Observation
	for _, field := range []string{"actions", "state", "visible"} {
		t.Run("missing_"+field, func(t *testing.T) {
			o := cloneEvaluation(t, good)
			switch field {
			case "actions":
				o.Actions = nil
			case "state":
				o.State = nil
			case "visible":
				o.Visible = nil
			}
			s := scoreEvaluation(c, o)
			if s.Status != "error" || s.Actions != "skip" {
				t.Fatalf("missing input counted as a pass: %+v", s)
			}
		})
	}
	for _, status := range []string{"error", "skip", "unsupported"} {
		o := evaluationObservation{Outcome: status, Reason: "synthetic outcome"}
		s := scoreEvaluation(c, o)
		if s.Status != status || s.Semantic != "unsupported" {
			t.Fatalf("status lost: %+v", s)
		}
		// A later error or skip cannot hide an earlier forbidden mutation.
		o.Actions = []evaluationAction{{Step: 1, Op: "close", Target: "any"}}
		s = scoreEvaluation(c, o)
		if s.Status != "fail" || s.FailureCount != 1 {
			t.Fatalf("forbidden action hidden: %+v", s)
		}
	}
	o := cloneEvaluation(t, good)
	o.Actions = append(o.Actions, evaluationAction{Step: 1, Op: "close", Target: "s1-i1"})
	o.State[0].Route = "wrong"
	s := scoreEvaluation(c, o)
	if s.FailureCount < 3 {
		t.Fatalf("failure counts collapsed: %+v", s)
	}
}

func TestEvaluationContractRejectsInvalidFixtures(t *testing.T) {
	original := evaluationCases(t)[0]
	for _, change := range []struct {
		name string
		fn   func(*evaluationCase)
	}{
		{"version", func(c *evaluationCase) { c.Version++ }},
		{"expected_actions", func(c *evaluationCase) { c.ExpectedActions = nil }},
		{"expected_state", func(c *evaluationCase) { c.ExpectedState = nil }},
		{"expected_history", func(c *evaluationCase) { c.ExpectedVisible = nil }},
		{"adapter", func(c *evaluationCase) { c.Adapter = "scripted-state-v1" }},
		{"clock", func(c *evaluationCase) { c.Clock = "tomorrow" }},
		{"time_zone", func(c *evaluationCase) { c.TimeZone = "" }},
		{"history", func(c *evaluationCase) { c.HistoryLimit = 0 }},
		{"output", func(c *evaluationCase) { c.Turns[0].Output = nil }},
		{"tags", func(c *evaluationCase) { c.ExpectedState[0].Tags = nil }},
		{"duplicate_state", func(c *evaluationCase) { c.ExpectedState = append(c.ExpectedState, c.ExpectedState[0]) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			c := cloneEvaluation(t, original)
			change.fn(&c)
			if validateEvaluationCase(c) == nil {
				t.Fatal("accepted invalid fixture")
			}
			if s := scoreEvaluation(c, evaluationObservation{Outcome: "ok"}); s.Status != "error" {
				t.Fatalf("invalid fixture status: %+v", s)
			}
		})
	}
	for _, data := range []string{`[]`, `[{"unknown":true}]`, `null`, `[] {}`} {
		path := filepath.Join(t.TempDir(), "cases.json")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadEvaluationCases(path); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestEvaluationSimulatorRejectsInvalidMutations(t *testing.T) {
	var c evaluationCase
	for _, candidate := range evaluationCases(t) {
		if candidate.ID == "update-explicit-target" {
			c = candidate
		}
	}
	for _, action := range []evaluationAction{
		{Op: "close", Target: "absent"}, {Op: "update", Target: "task-a"},
		{Op: "update", Target: "task-a", Fields: map[string]string{"unknown": "value"}},
		{Op: "review", Target: "task-a"}, {Op: "delete", Target: "task-a"},
		{Op: "create", Target: "task-a", Item: &c.Initial[0]},
	} {
		if _, err := applyEvaluationAction(c.Initial, action); err == nil {
			t.Fatalf("accepted invalid action: %+v", action)
		}
	}
}

func TestEvaluationReportsKeepStatusesSeparate(t *testing.T) {
	r := newEvaluationReport(t, "current", "offline")
	for _, status := range []string{"pass", "fail", "error", "skip", "unsupported"} {
		r.add(evaluationResult{Score: evaluationScore{Status: status}})
	}
	for status, count := range r.Counts {
		if count != 1 {
			t.Fatalf("%s count = %d", status, count)
		}
	}
	dir := t.TempDir()
	t.Setenv("INDEX01_EVAL_REPORT_DIR", dir)
	saveEvaluationReport(t, r)
	data, err := os.ReadFile(filepath.Join(dir, "current-offline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var restored evaluationReport
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Results) != 5 || restored.Counts["pass"] != 1 {
		t.Fatal("report lost non-passing results")
	}
}

func TestEvaluationLiveBudgetAndOptIn(t *testing.T) {
	for _, v := range []struct {
		trials, budget string
		calls          int
	}{
		{"", "", 2}, {"0", "4", 2}, {"2", "3", 2}, {"11", "30", 2}, {"2", "31", 2}, {"1", "1", 0},
	} {
		if _, _, err := evaluationLiveSettings(v.trials, v.budget, v.calls); err == nil {
			t.Fatalf("accepted invalid budget: %+v", v)
		}
	}
	if trials, budget, err := evaluationLiveSettings("2", "4", 2); err != nil || trials != 2 || budget != 4 {
		t.Fatalf("valid budget rejected: %d %d %v", trials, budget, err)
	}
	for _, c := range evaluationCases(t) {
		if c.Suite == "proposed" {
			r := runEvaluationCase(c, 1, true)
			if r.Score.Status != "unsupported" || r.Telemetry.Calls != 0 {
				t.Fatal("experimental case used live provider")
			}
			break
		}
	}
	t.Setenv("INDEX01_RUN_LIVE_EVALUATION", "")
	// The disabled live entrypoint must skip without a token or report writes.
	t.Setenv("INDEX01_DEEPSEEK_TOKEN", "")
	if !t.Run("disabled", TestEvaluationLive) {
		t.Fatal("disabled live test failed")
	}
}

func TestEvaluationScriptErrorsDoNotPass(t *testing.T) {
	for _, c := range evaluationCases(t) {
		if c.Suite == "proposed" {
			c.Turns[0].Output = json.RawMessage(`[{"step":1,"op":"create","target":"missing"}]`)
			r := runEvaluationCase(c, 1, false)
			if r.Score.Status != "error" || !strings.Contains(r.Observation.Reason, "invalid simulated action") {
				t.Fatalf("invalid script result: %+v", r)
			}
			break
		}
	}
}

func TestEvaluationLiveTelemetryUsesFreshRequests(t *testing.T) {
	telemetry := evaluationTelemetry{}
	calls := 0
	transport := evaluationLiveTransport(&telemetry, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return fixtureResponse(http.StatusOK, `{"id":"synthetic-response","model":"synthetic-model","usage":{"input_tokens":11,"output_tokens":7,"private":"do not retain"}}`), nil
	}))
	for range 2 {
		req, err := http.NewRequest(http.MethodPost, deepSeekResponsesURL, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		response, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || !strings.Contains(string(body), "synthetic-response") {
			t.Fatal("metadata capture consumed the response")
		}
	}
	if calls != 2 || telemetry.Calls != 2 || len(telemetry.ResponseIDs) != 2 || len(telemetry.Usage) != 2 {
		t.Fatalf("requests were reused or metadata was lost: %+v", telemetry)
	}
	if telemetry.Usage[0]["input_tokens"] != 11 {
		t.Fatal("usage was lost")
	}
	data, err := json.Marshal(telemetry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "do not retain") {
		t.Fatal("retained unapproved provider metadata")
	}
	bad, err := http.NewRequest(http.MethodPost, "https://api.ticktick.test/open/v1/task", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(bad); err == nil || calls != 2 {
		t.Fatal("live transport accepted another destination")
	}
}
