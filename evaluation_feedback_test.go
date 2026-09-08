package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

type feedbackRoute struct {
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	ProjectID string `json:"project_id"`
}

type feedbackResult struct {
	ID              string              `json:"id"`
	Split           string              `json:"split"`
	InputProvenance string              `json:"input_provenance"`
	Trial           int                 `json:"trial"`
	Status          string              `json:"status"`
	BlockedReasons  []string            `json:"blocked_reasons,omitempty"`
	Diagnostics     []string            `json:"diagnostics,omitempty"`
	Observed        []feedbackRoute     `json:"observed_routes,omitempty"`
	Telemetry       evaluationTelemetry `json:"telemetry"`
}

type feedbackReport struct {
	FormatVersion      int                       `json:"format_version"`
	FixtureVersion     int                       `json:"fixture_version"`
	Mode               string                    `json:"mode"`
	Source             evalcorpus.Source         `json:"source"`
	CorpusSHA256       string                    `json:"corpus_sha256"`
	PromptSHA256       string                    `json:"prompt_sha256"`
	Model              string                    `json:"model"`
	Routing            *evalcorpus.RoutingConfig `json:"routing"`
	Claim              string                    `json:"claim"`
	IdentityLimitation string                    `json:"identity_limitation"`
	GenerationCalls    int                       `json:"generation_calls"`
	JudgeCalls         int                       `json:"judge_calls"`
	CallBudget         int                       `json:"call_budget"`
	Trials             int                       `json:"trials"`
	Counts             map[string]int            `json:"counts"`
	Coverage           evalcorpus.Coverage       `json:"coverage"`
	Diagnostics        []string                  `json:"diagnostics,omitempty"`
	Results            []feedbackResult          `json:"results"`
}

// TestEvaluationFeedbackCorpus keeps private feedback separate from the synthetic baseline.
func TestEvaluationFeedbackCorpus(t *testing.T) {
	path := os.Getenv("INDEX01_EVAL_CORPUS")
	if path == "" {
		path = "testdata/evaluation/feedback-corpus.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read feedback corpus")
	}
	corpus, err := evalcorpus.Load(data)
	if err != nil {
		t.Fatal("invalid feedback corpus; validate its schema")
	}
	mode := os.Getenv("INDEX01_EVAL_CORPUS_MODE")
	if mode == "" {
		mode = "replay"
	}
	report, runErr := runFeedbackCorpus(corpus, data, mode, nil)
	t.Logf("feedback mode=%s pass=%d fail=%d error=%d skip=%d unsupported=%d generation_calls=%d judge_calls=%d", mode, report.Counts["pass"], report.Counts["fail"], report.Counts["error"], report.Counts["skip"], report.Counts["unsupported"], report.GenerationCalls, report.JudgeCalls)
	t.Logf("corpus recordings=%d targets=%d ready=%d blocked=%d replay_ready=%d", report.Coverage.Recordings, report.Coverage.Targets, report.Coverage.Ready, report.Coverage.Blocked, report.Coverage.ReplayReady)
	if mode == "audit" {
		t.Log("Audit checks eligibility only. No model trials or model accuracy passes.")
	}
	if directory := os.Getenv("INDEX01_EVAL_REPORT_DIR"); directory != "" {
		if err := writeFeedbackReportForInput(path, directory, report); err != nil {
			t.Fatal("cannot write private feedback report")
		}
	}
	if runErr != nil {
		t.Fatalf("feedback %s failed; inspect report diagnostics", mode)
	}
}

// runFeedbackCorpus never connects to TickTick. A custom model transport is used only by safety tests.
func runFeedbackCorpus(corpus evalcorpus.Corpus, data []byte, mode string, modelTransport http.RoundTripper) (feedbackReport, error) {
	digest := sha256.Sum256(data)
	report := feedbackReport{FormatVersion: 1, FixtureVersion: corpus.FormatVersion, Mode: mode,
		Source: corpus.Source, CorpusSHA256: hex.EncodeToString(digest[:]), Routing: corpus.Routing,
		Coverage:     evalcorpus.SummarizeCoverage(corpus),
		PromptSHA256: feedbackPromptHash(), Model: deepSeekModel, Trials: 1,
		Counts:             map[string]int{"pass": 0, "fail": 0, "error": 0, "skip": 0, "unsupported": 0},
		IdentityLimitation: "Items use exact normalized title and kind identity. Paraphrases can fail. This is not semantic grading."}
	report.Claim = "Saved-output replay checks production extraction parsing and routing. It does not measure model accuracy."
	if mode == "audit" {
		report.Claim = "Eligibility audit only. No extraction or model accuracy was tested."
	}
	if mode == "live" {
		report.Claim = "Live synthetic or original-input extraction with strict title identity and route scoring. No TickTick writes."
	}
	appendResult := func(result feedbackResult) {
		report.Results = append(report.Results, result)
		report.Counts[result.Status]++
	}
	eligible := 0
	for _, example := range corpus.Examples {
		if len(evalcorpus.Eligibility(corpus, example)) == 0 {
			eligible++
		}
	}
	preflight := ""
	if mode != "audit" && mode != "replay" && mode != "live" {
		preflight = "mode must be audit, replay, or live"
	}
	var budget int
	if mode == "live" {
		if os.Getenv("INDEX01_RUN_LIVE_CORPUS_EVALUATION") != "1" {
			preflight = "live corpus evaluation requires explicit opt-in"
		}
		if os.Getenv("INDEX01_EVAL_TRIALS") == "" || os.Getenv("INDEX01_EVAL_CALL_BUDGET") == "" {
			preflight = "live corpus evaluation requires explicit trials and call budget"
		}
		if os.Getenv("INDEX01_DEEPSEEK_TOKEN") == "" {
			preflight = "live corpus evaluation requires a model token"
		}
		trials, allowed, err := evaluationLiveSettings(os.Getenv("INDEX01_EVAL_TRIALS"), os.Getenv("INDEX01_EVAL_CALL_BUDGET"), eligible)
		if err != nil {
			preflight = "invalid live trials or call budget"
		} else {
			report.Trials, budget, report.CallBudget = trials, allowed, allowed
		}
		if eligible == 0 {
			preflight = "live corpus contains no eligible original or synthetic examples"
		}
		if eligible*report.Trials > budget {
			preflight = "call budget cannot cover all eligible examples and trials"
		}
	}
	if preflight != "" {
		report.Diagnostics = append(report.Diagnostics, preflight)
	}
	providerFailed := false
	for _, example := range corpus.Examples {
		blocked := evalcorpus.Eligibility(corpus, example)
		trials := report.Trials
		if preflight != "" || len(blocked) != 0 || mode == "audit" {
			trials = 1
		}
		for trial := 1; trial <= trials; trial++ {
			result := feedbackResult{ID: example.ID, Split: example.Split, InputProvenance: example.Input.Provenance, Trial: trial}
			if len(blocked) != 0 {
				result.Status, result.BlockedReasons = "unsupported", blocked
				appendResult(result)
				continue
			}
			if preflight != "" {
				result.Status, result.Diagnostics = "skip", []string{"run preflight failed"}
				appendResult(result)
				continue
			}
			if mode == "audit" {
				result.Status, result.Diagnostics = "skip", []string{"eligible; audit does not execute extraction"}
				appendResult(result)
				continue
			}
			if providerFailed {
				result.Status, result.Diagnostics = "skip", []string{"stopped after model request failure"}
				appendResult(result)
				continue
			}
			if mode == "replay" && len(example.SavedOutput) == 0 {
				result.Status, result.BlockedReasons = "unsupported", []string{"independent saved model output is missing"}
				appendResult(result)
				continue
			}
			routing := evalcorpus.EffectiveRouting(corpus, example)
			clock, clockErr := time.Parse(time.RFC3339Nano, routing.Clock)
			_, zoneErr := time.LoadLocation(routing.TimeZone)
			if clockErr != nil || zoneErr != nil {
				result.Status, result.Diagnostics = "error", []string{"invalid evaluation clock or time zone"}
				appendResult(result)
				continue
			}
			transport := modelTransport
			if mode == "replay" {
				transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
					var request deepSeekRequest
					if req.Method != http.MethodPost || req.URL.String() != deepSeekResponsesURL || json.NewDecoder(req.Body).Decode(&request) != nil || len(request.Input) != 2 || request.Input[1].Content != example.Input.Text {
						return nil, fmt.Errorf("unexpected extraction fixture request")
					}
					return deepSeekFixtureOutput("synthetic-feedback", string(example.SavedOutput)), nil
				})
			} else {
				if transport == nil {
					transport = http.DefaultTransport
				}
				transport = evaluationLiveTransport(&result.Telemetry, transport)
			}
			token := "synthetic-fixture-token"
			if mode == "live" {
				token = os.Getenv("INDEX01_DEEPSEEK_TOKEN")
			}
			client, err := NewDeepSeekClientWithConfig(token, transport, func() time.Time { return clock }, DeepSeekClientConfig{Model: deepSeekModel, TimeZone: routing.TimeZone})
			if err != nil {
				result.Status, result.Diagnostics = "error", []string{"invalid extraction client configuration"}
				appendResult(result)
				continue
			}
			aliases := make([]string, 0, len(routing.Aliases))
			for alias := range routing.Aliases {
				aliases = append(aliases, alias)
			}
			sort.Strings(aliases)
			items, err := client.Extract(context.Background(), example.Input.Text, aliases)
			report.GenerationCalls += result.Telemetry.Calls
			if err != nil {
				result.Status, result.Diagnostics = "error", []string{"extraction request or response failed; provider details omitted"}
				if mode == "live" {
					providerFailed = true
				}
				appendResult(result)
				continue
			}
			routes, err := feedbackResolveRoutes(items, *routing)
			if err != nil {
				result.Status, result.Diagnostics = "error", []string{"production route resolution failed"}
				appendResult(result)
				continue
			}
			result.Observed = routes
			result.Diagnostics = scoreFeedbackRoutes(example.Targets, routes)
			result.Status = "pass"
			if len(result.Diagnostics) != 0 {
				result.Status = "fail"
			}
			appendResult(result)
		}
	}
	if mode == "replay" && report.Counts["pass"]+report.Counts["fail"]+report.Counts["error"] == 0 {
		report.Diagnostics = append(report.Diagnostics, "replay evaluated no examples")
		return report, fmt.Errorf("feedback replay evaluated no examples")
	}
	if preflight != "" || report.Counts["error"] > 0 || report.Counts["fail"] > 0 {
		return report, fmt.Errorf("feedback evaluation failed")
	}
	return report, nil
}

func feedbackResolveRoutes(extraction FrozenExtraction, config evalcorpus.RoutingConfig) ([]feedbackRoute, error) {
	ids := map[string]bool{}
	for _, id := range config.Aliases {
		ids[id] = true
	}
	ids[config.DefaultProjectID], ids[config.NoteProjectID] = true, true
	projects := []map[string]any{}
	for id := range ids {
		if id != "" {
			kind := "TASK"
			if id == config.NoteProjectID {
				kind = "NOTE"
			}
			projects = append(projects, map[string]any{"id": id, "name": "Evaluation fixture", "kind": kind, "closed": false, "permission": nil})
		}
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i]["id"].(string) < projects[j]["id"].(string) })
	encoded, err := json.Marshal(projects)
	if err != nil {
		return nil, err
	}
	client := &TickTickClient{baseURL: "https://ticktick.fixture.invalid", token: "synthetic-fixture-token", httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Path != "/project" {
			return nil, fmt.Errorf("unexpected TickTick fixture request")
		}
		return fixtureResponse(http.StatusOK, string(encoded)), nil
	})}}
	router, err := client.ValidateRouting(context.Background(), TickTickRoutingConfig{Aliases: config.Aliases, DefaultProjectID: config.DefaultProjectID, NoteProjectID: config.NoteProjectID})
	if err != nil {
		return nil, err
	}
	routes := make([]feedbackRoute, 0, len(extraction.Items))
	for _, item := range extraction.Items {
		id, err := router.ResolveItemProject(item.Kind, item.ProjectAlias)
		if err != nil {
			return nil, err
		}
		routes = append(routes, feedbackRoute{Title: item.Title, Kind: string(item.Kind), ProjectID: id})
	}
	return routes, nil
}

func feedbackPromptHash() string {
	data, err := os.ReadFile("deepseek.go")
	if err != nil {
		return "unavailable"
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func scoreFeedbackRoutes(targets []evalcorpus.Target, routes []feedbackRoute) []string {
	key := func(title, kind string) string {
		return strings.ToLower(strings.Join(strings.Fields(title), " ")) + "\x00" + kind
	}
	expected := map[string]string{}
	diagnostics := []string{}
	for _, target := range targets {
		identity := key(target.Title, target.Kind)
		if _, found := expected[identity]; found {
			diagnostics = append(diagnostics, "expected item identity is duplicated")
		}
		expected[identity] = target.Label.ProjectID
	}
	seen := map[string]bool{}
	for _, route := range routes {
		identity := key(route.Title, route.Kind)
		if seen[identity] {
			diagnostics = append(diagnostics, "observed item identity is duplicated")
		}
		seen[identity] = true
		project, found := expected[identity]
		if !found {
			diagnostics = append(diagnostics, "unexpected item title or kind; strict identity may reject paraphrases")
			continue
		}
		if project != route.ProjectID {
			diagnostics = append(diagnostics, "item resolved to the wrong project")
		}
	}
	for identity := range expected {
		if !seen[identity] {
			diagnostics = append(diagnostics, "expected item title and kind is missing")
		}
	}
	sort.Strings(diagnostics)
	return diagnostics
}

func writeFeedbackReport(directory string, report feedbackReport) error {
	if report.Mode != "audit" && report.Mode != "replay" && report.Mode != "live" {
		return fmt.Errorf("invalid feedback report mode")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".feedback-report-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, filepath.Join(directory, "feedback-"+report.Mode+".json"))
}

func writeFeedbackReportForInput(input, directory string, report feedbackReport) error {
	if report.Mode != "audit" && report.Mode != "replay" && report.Mode != "live" {
		return fmt.Errorf("invalid feedback report mode")
	}
	output := filepath.Join(directory, "feedback-"+report.Mode+".json")
	inputPath, err := filepath.Abs(input)
	if err != nil {
		return err
	}
	outputPath, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if inputPath == outputPath {
		return fmt.Errorf("report cannot replace corpus input")
	}
	inputInfo, inputErr := os.Stat(inputPath)
	outputInfo, outputErr := os.Stat(outputPath)
	if inputErr == nil && outputErr == nil && os.SameFile(inputInfo, outputInfo) {
		return fmt.Errorf("report cannot alias corpus input")
	}
	return writeFeedbackReport(directory, report)
}

func TestEvaluationFeedbackRouteIdentity(t *testing.T) {
	targets := []evalcorpus.Target{{Title: "Alpha", Kind: "task", Label: evalcorpus.Label{ProjectID: "work"}}, {Title: "Beta", Kind: "task", Label: evalcorpus.Label{ProjectID: "home"}}}
	for name, routes := range map[string][]feedbackRoute{
		"swapped routes": {{"Alpha", "task", "home"}, {"Beta", "task", "work"}},
		"wrong kind":     {{"Alpha", "note", "work"}, {"Beta", "task", "home"}},
		"duplicate":      {{"Alpha", "task", "work"}, {"Alpha", "task", "work"}, {"Beta", "task", "home"}},
		"missing":        {{"Alpha", "task", "work"}},
		"extra":          {{"Alpha", "task", "work"}, {"Beta", "task", "home"}, {"Gamma", "task", "home"}},
		"paraphrase":     {{"A different Alpha", "task", "work"}, {"Beta", "task", "home"}},
	} {
		t.Run(name, func(t *testing.T) {
			if len(scoreFeedbackRoutes(targets, routes)) == 0 {
				t.Fatal("incorrect output passed")
			}
		})
	}
	if got := scoreFeedbackRoutes(targets, []feedbackRoute{{"  ALPHA ", "task", "work"}, {"Beta", "task", "home"}}); len(got) != 0 {
		t.Fatal("normalized identity failed")
	}
}

func loadFeedbackFixture(t *testing.T) (evalcorpus.Corpus, []byte) {
	t.Helper()
	data, err := os.ReadFile("testdata/evaluation/feedback-corpus.json")
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := evalcorpus.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	return corpus, data
}

func TestEvaluationFeedbackCorpusSafety(t *testing.T) {
	t.Run("private input never reaches process output", func(t *testing.T) {
		corpus, data := loadFeedbackFixture(t)
		corpus.Examples = corpus.Examples[:1]
		corpus.Examples[0].Input.Text = "PRIVATE_TRANSCRIPT_SENTINEL"
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, stderr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = writer, writer
		defer func() { os.Stdout, os.Stderr = stdout, stderr; reader.Close(); writer.Close() }()
		output := make(chan []byte, 1)
		go func() { data, _ := io.ReadAll(reader); output <- data }()
		_, runErr := runFeedbackCorpus(corpus, data, "replay", nil)
		os.Stdout, os.Stderr = stdout, stderr
		writer.Close()
		if runErr != nil || strings.Contains(string(<-output), "PRIVATE_TRANSCRIPT_SENTINEL") {
			t.Fatal("private transcript was exposed or fixture failed")
		}
	})
	t.Run("missing trailing sibling is unsupported", func(t *testing.T) {
		corpus, data := loadFeedbackFixture(t)
		corpus.Examples = corpus.Examples[2:3]
		corpus.Examples[0].Targets = corpus.Examples[0].Targets[:2]
		report, err := runFeedbackCorpus(corpus, data, "replay", roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("incomplete recording attempted extraction")
			return nil, nil
		}))
		if err == nil || report.Counts["unsupported"] != 1 || report.Counts["pass"] != 0 {
			t.Fatal("incomplete recording was evaluated")
		}
	})
	t.Run("unknown recording completeness is unsupported", func(t *testing.T) {
		corpus, data := loadFeedbackFixture(t)
		corpus.Examples = corpus.Examples[:1]
		corpus.Examples[0].ExpectedItemCount = nil
		report, _ := runFeedbackCorpus(corpus, data, "replay", nil)
		if report.Counts["unsupported"] != 1 || report.Counts["pass"] != 0 {
			t.Fatal("unknown recording completeness was evaluated")
		}
	})
	t.Run("audit has no fabricated pass", func(t *testing.T) {
		corpus, data := loadFeedbackFixture(t)
		report, err := runFeedbackCorpus(corpus, data, "audit", roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("audit attempted model request"); return nil, nil }))
		if err != nil || report.Counts["pass"] != 0 || report.Counts["skip"] != len(corpus.Examples) || report.GenerationCalls != 0 {
			t.Fatal("audit incorrectly claimed execution")
		}
	})
	t.Run("missing original transcript is unsupported", func(t *testing.T) {
		corpus, data := loadFeedbackFixture(t)
		corpus.Source.Kind = "routing_feedback_ledger"
		corpus.Examples[0].Input = evalcorpus.Input{Provenance: "missing"}
		report, _ := runFeedbackCorpus(corpus, data, "replay", nil)
		if report.Results[0].Status != "unsupported" || len(report.Results[0].BlockedReasons) == 0 {
			t.Fatal("missing input was evaluated")
		}
	})
	t.Run("independent saved output and preferred label control score", func(t *testing.T) {
		corpus, data := loadFeedbackFixture(t)
		corpus.Examples = corpus.Examples[:1]
		report, err := runFeedbackCorpus(corpus, data, "replay", nil)
		if err != nil || report.Counts["pass"] != 1 {
			t.Fatal("corrected route failed")
		}
		corpus.Examples[0].SavedOutput = json.RawMessage(`{"items":[{"title":"Repair the garden gate","content":"","kind":"task","project_alias":"work","due_at":null,"all_day":false,"priority":0,"tags":[]}]}`)
		report, err = runFeedbackCorpus(corpus, data, "replay", nil)
		if err == nil || report.Counts["fail"] != 1 {
			t.Fatal("old route passed corrected label")
		}
		corpus.Examples[0].Targets[0].Label.ProjectID = "project-work"
		report, err = runFeedbackCorpus(corpus, data, "replay", nil)
		if err != nil || report.Counts["pass"] != 1 {
			t.Fatal("reviewed label did not affect result")
		}
	})
	t.Run("live requires separate opt in", func(t *testing.T) {
		t.Setenv("INDEX01_RUN_LIVE_EVALUATION", "1")
		t.Setenv("INDEX01_RUN_LIVE_CORPUS_EVALUATION", "")
		t.Setenv("INDEX01_EVAL_TRIALS", "1")
		t.Setenv("INDEX01_EVAL_CALL_BUDGET", "20")
		t.Setenv("INDEX01_DEEPSEEK_TOKEN", "fixture")
		corpus, data := loadFeedbackFixture(t)
		report, err := runFeedbackCorpus(corpus, data, "live", roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("disabled live request"); return nil, nil }))
		if err == nil || report.GenerationCalls != 0 {
			t.Fatal("live opt in missing")
		}
	})
	t.Run("budget fails before requests", func(t *testing.T) {
		t.Setenv("INDEX01_RUN_LIVE_CORPUS_EVALUATION", "1")
		t.Setenv("INDEX01_EVAL_TRIALS", "2")
		t.Setenv("INDEX01_EVAL_CALL_BUDGET", "1")
		t.Setenv("INDEX01_DEEPSEEK_TOKEN", "fixture")
		corpus, data := loadFeedbackFixture(t)
		report, err := runFeedbackCorpus(corpus, data, "live", roundTripFunc(func(*http.Request) (*http.Response, error) { t.Fatal("over budget request"); return nil, nil }))
		if err == nil || report.GenerationCalls != 0 {
			t.Fatal("budget preflight failed")
		}
	})
	t.Run("all blocked live makes zero calls", func(t *testing.T) {
		t.Setenv("INDEX01_RUN_LIVE_CORPUS_EVALUATION", "1")
		t.Setenv("INDEX01_EVAL_TRIALS", "1")
		t.Setenv("INDEX01_EVAL_CALL_BUDGET", "20")
		t.Setenv("INDEX01_DEEPSEEK_TOKEN", "fixture")
		corpus, data := loadFeedbackFixture(t)
		for i := range corpus.Examples {
			corpus.Examples[i].Input.Text = ""
		}
		report, err := runFeedbackCorpus(corpus, data, "live", nil)
		if err == nil || report.GenerationCalls != 0 || report.Counts["unsupported"] != len(corpus.Examples) {
			t.Fatal("blocked live preflight failed")
		}
	})
	t.Run("provider failure stops remaining rows without leaking detail", func(t *testing.T) {
		t.Setenv("INDEX01_RUN_LIVE_CORPUS_EVALUATION", "1")
		t.Setenv("INDEX01_EVAL_TRIALS", "1")
		t.Setenv("INDEX01_EVAL_CALL_BUDGET", "20")
		t.Setenv("INDEX01_DEEPSEEK_TOKEN", "fixture")
		corpus, data := loadFeedbackFixture(t)
		requests := 0
		report, err := runFeedbackCorpus(corpus, data, "live", roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private-provider-secret"))}, nil
		}))
		encoded, _ := json.Marshal(report)
		if err == nil || requests != 1 || report.GenerationCalls != 1 || report.Counts["error"] != 1 || strings.Contains(string(encoded), "private-provider-secret") {
			t.Fatal("provider error was not safely bounded")
		}
	})
	t.Run("report permissions", func(t *testing.T) {
		corpus, data := loadFeedbackFixture(t)
		report, _ := runFeedbackCorpus(corpus, data, "audit", nil)
		directory := filepath.Join(t.TempDir(), "private")
		if err := writeFeedbackReport(directory, report); err != nil {
			t.Fatal(err)
		}
		for path, want := range map[string]os.FileMode{directory: 0o700, filepath.Join(directory, "feedback-audit.json"): 0o600} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Fatal("report permission mismatch")
			}
		}
	})
}

func TestEvaluationFeedbackImportedCorrection(t *testing.T) {
	fixture, _ := loadFeedbackFixture(t)
	fingerprint := fixture.Examples[0].RecordingFingerprint
	observation := func(project, at string) map[string]any {
		return map[string]any{"task_id": "moved-task", "observed_at": at, "candidate": map[string]any{
			"candidate_id":        "candidate-moved-task",
			"source":              map[string]any{"task_id": "moved-task", "markers": []any{map[string]any{"marker": "[index01:" + fingerprint + ":0]", "recording_fingerprint": fingerprint, "item_index": 0}}},
			"observed_current":    map[string]any{"project_id": project, "title": "Repair the garden gate", "kind": "TEXT"},
			"input":               map[string]any{"transcript": "Repair the garden gate.", "transcript_provenance": "original"},
			"historical_delivery": map[string]any{"expected_item_count": 1},
		}}
	}
	from, to := observation("project-work", "2026-09-07T12:00:00Z"), observation("project-home", "2026-09-08T12:00:00Z")
	input := map[string]any{
		"format_version": 1, "type": "routing_feedback_ledger", "collected_at": "2026-09-08T12:00:00Z",
		"incomplete": false, "findings": []any{}, "collection_failures": []any{}, "reviews": map[string]any{},
		"events":       []any{map[string]any{"event_id": "reviewed-move", "type": "observed_move", "from": from, "to": to, "review": map[string]any{"status": "inferred", "basis": "owner_default"}, "expected_route": map[string]any{"project_id": "project-home"}}},
		"observations": []any{to},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := evalcorpus.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	corpus.Routing = fixture.Routing
	if len(corpus.Examples) != 1 || corpus.Examples[0].Targets[0].Label.ProjectID != "project-home" {
		t.Fatal("ledger correction did not become expected route")
	}
	corpus.Examples[0].SavedOutput = fixture.Examples[0].SavedOutput
	report, err := runFeedbackCorpus(corpus, data, "replay", nil)
	if err != nil || report.Counts["pass"] != 1 {
		t.Fatal("corrected output failed imported route")
	}
	corpus.Examples[0].SavedOutput = json.RawMessage(strings.ReplaceAll(string(corpus.Examples[0].SavedOutput), `"home"`, `"work"`))
	report, err = runFeedbackCorpus(corpus, data, "replay", nil)
	if err == nil || report.Counts["fail"] != 1 {
		t.Fatal("old output passed imported correction")
	}
}

func TestEvaluationFeedbackFakeLiveTrials(t *testing.T) {
	t.Setenv("INDEX01_RUN_LIVE_CORPUS_EVALUATION", "1")
	t.Setenv("INDEX01_EVAL_TRIALS", "2")
	t.Setenv("INDEX01_EVAL_CALL_BUDGET", "2")
	t.Setenv("INDEX01_DEEPSEEK_TOKEN", "fixture")
	corpus, data := loadFeedbackFixture(t)
	corpus.Examples = corpus.Examples[:1]
	requests := 0
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		var request deepSeekRequest
		if req.URL.String() != deepSeekResponsesURL || json.NewDecoder(req.Body).Decode(&request) != nil || len(request.Input) != 2 || request.Input[1].Content != corpus.Examples[0].Input.Text {
			t.Fatal("live trial was not a fresh input request")
		}
		return deepSeekFixtureOutput(fmt.Sprintf("trial-%d", requests), string(corpus.Examples[0].SavedOutput)), nil
	})
	report, err := runFeedbackCorpus(corpus, data, "live", transport)
	if err != nil || requests != 2 || report.GenerationCalls != 2 || report.Counts["pass"] != 2 || report.JudgeCalls != 0 {
		t.Fatal("bounded live trials failed")
	}
}

func TestEvaluationFeedbackReportPreservesInput(t *testing.T) {
	corpus, data := loadFeedbackFixture(t)
	report, _ := runFeedbackCorpus(corpus, data, "audit", nil)
	for _, alias := range []string{"same path", "hard link", "symbolic link"} {
		t.Run(alias, func(t *testing.T) {
			directory := t.TempDir()
			input := filepath.Join(directory, "input.json")
			output := filepath.Join(directory, "feedback-audit.json")
			if alias == "same path" {
				input = output
			}
			if err := os.WriteFile(input, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if alias == "hard link" {
				if err := os.Link(input, output); err != nil {
					t.Fatal(err)
				}
			}
			if alias == "symbolic link" {
				if err := os.Symlink(input, output); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeFeedbackReportForInput(input, directory, report); err == nil {
				t.Fatal("input alias accepted")
			}
			after, err := os.ReadFile(input)
			if err != nil || string(after) != string(data) {
				t.Fatal("input corpus changed")
			}
		})
	}
	t.Run("existing directory permissions", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := writeFeedbackReport(directory, report); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(directory)
		if err != nil || info.Mode().Perm() != 0o750 {
			t.Fatal("existing directory permissions changed")
		}
	})
}
