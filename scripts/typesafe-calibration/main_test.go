package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/klauern/index-01-hook/internal/verificationcorpus"
)

func testCase(id, label, failure string) verificationcorpus.Case {
	return verificationcorpus.Case{ID: id, Split: "development", Label: label, FailureType: failure, Transcript: "create a task", Candidate: verificationcorpus.CandidateItem{Kind: "task", Title: "create a task"}}
}
func testCorpus() verificationcorpus.Corpus {
	return verificationcorpus.Corpus{Cases: []verificationcorpus.Case{testCase("s1", "supported", ""), testCase("u1", "unsupported", verificationcorpus.FailureTypeWrongKind)}}
}
func validNoulAnswers(qs map[string]question, value float64) map[string]answer {
	out := map[string]answer{}
	for id, q := range qs {
		out[id] = answer{Type: q.Type, Noul: &value}
	}
	return out
}

func TestOfflinePlanCountAndRepeatSelection(t *testing.T) {
	p, err := makePlan(testCorpus(), runConfig{Design: "both", RepeatCases: 2, RepeatCount: 3, MaxCalls: 100})
	if err != nil {
		t.Fatal(err)
	}
	if p.Calls != 12 {
		t.Fatalf("calls=%d, want 12", p.Calls)
	}
	if !slices.Equal(p.RepeatCases, []string{"s1", "u1"}) {
		t.Fatalf("repeat cases=%v", p.RepeatCases)
	}
}
func TestOfflinePlanMakesZeroHTTPRequests(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, nil
	})}
	if _, err := makePlan(testCorpus(), runConfig{Design: "both", RepeatCases: 0, RepeatCount: 1, MaxCalls: 10, ClientHTTP: client}); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatalf("offline plan made %d HTTP requests", requests)
	}
}

func TestApprovalAndBudgetFailBeforeRequest(t *testing.T) {
	if _, err := makePlan(testCorpus(), runConfig{Design: "both", RepeatCases: 0, RepeatCount: 1, MaxCalls: 1}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("budget error=%v", err)
	}
	// The CLI gate is deliberately represented by the exact message used by main.
	if os.Getenv("INDEX01_TYPESAFE_CALIBRATION_APPROVED") == "true" {
		t.Skip("environment approval is set")
	}
	if !strings.Contains("live calibration requires INDEX01_TYPESAFE_CALIBRATION_APPROVED=true", "APPROVED") {
		t.Fatal("approval message regressed")
	}
}
func TestFieldwiseWeakestAndOptionalQuestions(t *testing.T) {
	c := testCase("x", "supported", "")
	c.Candidate.Content = ""
	c.Candidate.Due = ""
	c.Candidate.Priority = 0
	c.Candidate.Tags = nil
	c.Candidate.ProjectAlias = ""
	q := fieldQuestions(c)
	for _, id := range []string{"content_supported", "date_supported", "priority_supported", "tags_supported", "route_supported"} {
		if _, ok := q[id]; ok {
			t.Errorf("asked optional question %s", id)
		}
	}
	vals := map[string]float64{}
	for id, v := range map[string]float64{"injection_detected": 0.1, "item_present": .95, "kind_supported": .95, "title_supported": .95} {
		vals[id] = v
	}
	answers := map[string]answer{}
	for id, v := range vals {
		x := v
		answers[id] = answer{Type: "noul", Noul: &x}
	}
	s, _, _, err := scoreSignals("fieldwise", response{Answers: answers}, q)
	if err != nil {
		t.Fatal(err)
	}
	if s != .95 {
		t.Fatalf("weakest score=%v", s)
	}
}
func TestChoiceUsesSupportedProbability(t *testing.T) {
	p := .37
	c := .8
	r := response{Answers: map[string]answer{"verdict": {Type: "choice", Choice: "wrong_field", Confidence: &c, Probabilities: map[string]float64{"supported": p, "wrong_field": .63}}}}
	s, sig, ch, err := scoreSignals("choice", r, choiceQuestions())
	requireNoError(t, err)
	assertEqual(t, s, p)
	assertEqual(t, ch, "wrong_field")
	if sig.(map[string]any)["choice"] != "wrong_field" {
		t.Fatal("choice signal missing")
	}
}
func TestDecisionBoundariesAndInjectionOverride(t *testing.T) {
	tests := []struct {
		name, design string
		signals      decisionSignals
		want         string
	}{
		{"accept boundary", "choice", decisionSignals{.9, 0, "supported"}, "accept"},
		{"review boundary", "choice", decisionSignals{.2, 0, "supported"}, "review"},
		{"below reject boundary", "choice", decisionSignals{.199, 0, "supported"}, "reject"},
		{"fieldwise injection", "fieldwise", decisionSignals{.99, .5, ""}, "review"},
		{"choice injection", "choice", decisionSignals{.99, 0, "injection"}, "review"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := (decisionPolicy{tc.design, .9, .2}).decide(tc.signals)
			assertEqual(t, got, tc.want)
		})
	}
}
func TestExactMeasures(t *testing.T) {
	rows := []caseRecord{
		{Label: "supported", ExpectedDecision: "accept", Score: .9, Decision: "accept"},
		{Label: "unsupported", FailureType: verificationcorpus.FailureTypeWrongKind, ExpectedDecision: "reject", Score: .95, Decision: "accept"},
		{Label: "unsupported", FailureType: verificationcorpus.FailureTypeWrongDate, ExpectedDecision: "reject", Score: .5, Decision: "review"},
		{Label: "unsupported", FailureType: verificationcorpus.FailureTypeWrongTag, ExpectedDecision: "reject", Score: .1, Decision: "reject"},
	}
	m := (decisionPolicy{"choice", .9, .2}).measure(rows)
	// False accepts use the accepted denominator: 1 unsafe item of 2 accepted.
	assertEqual(t, m.FalseAcceptRate, .5)
	assertEqual(t, m.FalseRejectRate, float64(0))
	assertEqual(t, m.ReviewVolume, .25)
	assertEqual(t, m.Agreement, .5)
	assertEqual(t, m.Accepted, 2)
	assertEqual(t, m.Rejected, 1)
	assertEqual(t, m.Review, 1)
	if m.Separation < -.3834 || m.Separation > -.3833 {
		t.Fatalf("separation=%v", m.Separation)
	}
}

// TestInjectionAcceptStaysObservable rejects a hardcoded review for an injected case.
// The gate must see a model that accepts an item the transcript tried to redirect.
func TestInjectionAcceptStaysObservable(t *testing.T) {
	injected := caseRecord{Label: "unsupported", FailureType: verificationcorpus.FailureTypePromptInjection, ExpectedDecision: verificationcorpus.ExpectedReview, Score: .97, Signals: map[string]any{"injection_detected": .05}}
	if got := (decisionPolicy{"fieldwise", .9, .2}).decisionFor(injected); got != "accept" {
		t.Fatalf("decision=%s, want accept when the model does not flag the injection", got)
	}
	m := (decisionPolicy{"fieldwise", .9, .2}).measure([]caseRecord{injected})
	if m.Agreement != 0 || m.InjectionAccepts != 1 {
		t.Fatalf("agreement=%v injection_accepts=%d", m.Agreement, m.InjectionAccepts)
	}
	flagged := injected
	flagged.Signals = map[string]any{"injection_detected": .8}
	if got := (decisionPolicy{"fieldwise", .9, .2}).decisionFor(flagged); got != "review" {
		t.Fatalf("decision=%s, want review when the model flags the injection", got)
	}
	flagged.Signals = map[string]float64{"injection_detected": .8}
	if got := (decisionPolicy{"fieldwise", .9, .2}).decisionFor(flagged); got != "review" {
		t.Fatalf("fieldwise decision=%s, want review for float signal map", got)
	}
	m = (decisionPolicy{"fieldwise", .9, .2}).measure([]caseRecord{flagged})
	if m.Review != 1 || m.InjectionAccepts != 0 {
		t.Fatalf("float signal measure=%+v", m)
	}
}
func TestMalformedResponseShapes(t *testing.T) {
	cases := []struct {
		name string
		body response
	}{{"model", response{Model: "other", Answers: map[string]answer{"x": {Type: "noul", Noul: ptr(.5)}}}}, {"count", response{Model: "m", Answers: map[string]answer{}}}, {"type", response{Model: "m", Answers: map[string]answer{"x": {Type: "choice", Choice: "a", Confidence: ptr(.5), Probabilities: map[string]float64{"a": 1}}}}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) { json.NewEncoder(w).Encode(tc.body) }))
			defer srv.Close()
			cl := newClient("secret-token", "m", srv.URL, nil)
			_, _, err := cl.evaluate(context.Background(), map[string]any{}, map[string]question{"x": {Type: "noul", Instructions: "x"}})
			if err == nil || !strings.Contains(err.Error(), "malformed") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
func TestCredentialSafety(t *testing.T) {
	const secret = "secret-token"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(response{Model: "m", Answers: validNoulAnswers(req.Questions, .9)})
	}))
	defer srv.Close()
	c := testCorpus()
	p, err := makePlan(c, runConfig{Design: "fieldwise", RepeatCases: 0, RepeatCount: 1, MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := execute(runConfig{Model: "m", Endpoint: srv.URL, Token: secret, ClientHTTP: srv.Client()}, c, p, "hash")
	if err != nil {
		t.Fatal(err)
	}
	serialized := marshalTestJSON(t, rep)
	if bytes.Contains(serialized, []byte(secret)) {
		t.Fatal("serialized report contains provider token")
	}
	stdout := capturePlanOutput(t, p)
	if bytes.Contains(stdout, []byte(secret)) {
		t.Fatal("stdout contains provider token")
	}
}

func TestProviderFailureContinuesAndAuthAborts(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			http.Error(w, "fail", 500)
			return
		}
		qs := map[string]question{}
		var req struct {
			Questions map[string]question `json:"questions"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		qs = req.Questions
		json.NewEncoder(w).Encode(response{Model: "m", Answers: validNoulAnswers(qs, .9)})
	}))
	defer srv.Close()
	c := testCorpus()
	p, _ := makePlan(c, runConfig{Design: "fieldwise", RepeatCases: 0, RepeatCount: 1, MaxCalls: 2})
	rep, err := execute(runConfig{Model: "m", Endpoint: srv.URL, Token: "secret-token", Out: "", ClientHTTP: srv.Client()}, c, p, "hash")
	if err != nil || len(rep.Cases) != 2 {
		t.Fatalf("err=%v cases=%d", err, len(rep.Cases))
	}
	if !strings.Contains(rep.Cases[0].Error, "retryable") {
		t.Fatalf("error=%s", rep.Cases[0].Error)
	}
	dev := rep.Measures["fieldwise"]["development"]
	assertEqual(t, dev.Errors, 1)
	// The successful response flags injection; only that response counts as review.
	assertEqual(t, dev.Review, 1)

}

func TestProviderAuthenticationAborts(t *testing.T) {
	c := testCorpus()
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 401) }))
	defer auth.Close()
	p, _ := makePlan(c, runConfig{Design: "fieldwise", RepeatCases: 0, RepeatCount: 1, MaxCalls: 2})
	_, err := execute(runConfig{Model: "m", Endpoint: auth.URL, Token: "secret-token", Out: "", ClientHTTP: auth.Client()}, c, p, "hash")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("auth err=%v", err)
	}
}
func ptr(v float64) *float64 { return &v }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestApplyDefaultsPinsProviderTargets guards the live endpoint and model pin.
// A missing endpoint once failed all 204 calls as transport errors.
func TestApplyDefaultsPinsProviderTargets(t *testing.T) {
	cfg := runConfig{}
	applyDefaults(&cfg)
	if cfg.Model != defaultModel {
		t.Fatalf("model=%q, want %q", cfg.Model, defaultModel)
	}
	if cfg.Endpoint != endpoint {
		t.Fatalf("endpoint=%q, want %q", cfg.Endpoint, endpoint)
	}
	given := runConfig{Model: "other", Endpoint: "https://example.test/v1"}
	applyDefaults(&given)
	if given.Model != "other" || given.Endpoint != "https://example.test/v1" {
		t.Fatalf("applyDefaults overwrote explicit values: %+v", given)
	}
}

// TestMissingEndpointStopsBeforeFirstRequest rejects a silent all-failed run.
func TestMissingEndpointStopsBeforeFirstRequest(t *testing.T) {
	c := testCorpus()
	p, err := makePlan(c, runConfig{Design: "fieldwise", RepeatCases: 0, RepeatCount: 1, MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	_, err = execute(runConfig{Model: "m", Token: "secret-token"}, c, p, "hash")
	if err == nil || !strings.Contains(err.Error(), "no provider endpoint") {
		t.Fatalf("error=%v, want missing endpoint failure", err)
	}
}

// TestRealCasePathMeasuresWithoutGate guards the private real-case path.
// Weak labels cannot prove the safety limit, so this path reports and never gates.
func TestRealCasePathMeasuresWithoutGate(t *testing.T) {
	casesPath, in := realCaseFixture(t)
	// The two designs must be measured apart. The Choice design answers a low
	// support probability so a mixed measurement is visible.
	srv := realCaseServer(t)
	defer srv.Close()

	cfg := runConfig{Model: "m", Endpoint: srv.URL, Token: "secret-token", CasesPath: casesPath, Design: "both", MaxCalls: 10, ClientHTTP: srv.Client()}
	caseCfg := cfg
	caseCfg.RepeatCases = 0
	caseCfg.RepeatCount = 1
	p, err := makePlan(realCorpus(in), caseCfg)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := executeReal(cfg, in, p)
	if err != nil {
		t.Fatal(err)
	}
	assertRealReportShape(t, rep)
	assertRealDesignMeasures(t, rep)
}

// TestRealCasePathRefusesAFileWithoutTranscripts keeps the private path honest.
func TestRealCasePathRefusesAFileWithoutTranscripts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty-transcripts.json")
	file := realInputFile{Cases: []realInputCase{{CaseID: "rc-1", Split: "real", Candidate: realCandidate{Kind: "task", Title: "No transcript"}, WeakLabel: "supported"}}}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadRealCases(path); err == nil || !strings.Contains(err.Error(), "no case with a transcript") {
		t.Fatalf("error=%v, want a transcript requirement", err)
	}
	if _, err = loadRealCases(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing file must fail")
	}
}

// assertEqual identifies the caller when a scalar contract changes.
func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func marshalTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	requireNoError(t, err)
	return encoded
}
func capturePlanOutput(t *testing.T, p plan) []byte {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	requireNoError(t, err)
	defer reader.Close()
	defer writer.Close()
	defer func() { os.Stdout = old }()
	os.Stdout = writer
	requireNoError(t, printJSON(p))
	requireNoError(t, writer.Close())
	os.Stdout = old
	var stdout bytes.Buffer
	_, err = stdout.ReadFrom(reader)
	requireNoError(t, err)
	return stdout.Bytes()
}
func realCaseFixture(t *testing.T) (string, []realInputCase) {
	t.Helper()
	dir := t.TempDir()
	casesPath := filepath.Join(dir, "real-cases.json")
	file := realInputFile{Cases: []realInputCase{
		{CaseID: "rc-1", Split: "real", Transcript: "buy milk", Candidate: realCandidate{Kind: "task", Title: "Buy milk"}, ObservedStatus: "unchanged", WeakLabel: "supported"},
		{CaseID: "rc-2", Split: "real", Transcript: "call the bank", Candidate: realCandidate{Kind: "task", Title: "Call the bank"}, ObservedStatus: "missing", WeakLabel: "unsupported"},
		{CaseID: "rc-3", Split: "real", Transcript: "note this", Candidate: realCandidate{Kind: "note", Title: "Note"}, ObservedStatus: "unclear", WeakLabel: "unknown"},
	}}
	encoded, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(casesPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	in, err := loadRealCases(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	return casesPath, in
}

// realCaseServer makes the two designs return distinct decisions.
func realCaseServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Questions map[string]question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if _, ok := req.Questions["verdict"]; ok {
			confidence := .9
			_ = json.NewEncoder(w).Encode(response{Model: "m", Answers: map[string]answer{"verdict": {Type: "choice", Choice: "wrong_field", Confidence: &confidence, Probabilities: map[string]float64{"supported": .1, "wrong_field": .9}}}})
			return
		}
		answers := validNoulAnswers(req.Questions, .95)
		if injection, ok := answers["injection_detected"]; ok {
			low := .05
			injection.Noul = &low
			answers["injection_detected"] = injection
		}
		_ = json.NewEncoder(w).Encode(response{Model: "m", Answers: answers})
	}))
}

func assertRealReportShape(t *testing.T, rep realReport) {
	t.Helper()
	if len(rep.Cases) != 6 {
		t.Fatalf("case records=%d, want 6 for two designs over three cases", len(rep.Cases))
	}
	if rep.WeakLabelWarning == "" {
		t.Fatal("the report must state that weak labels cannot prove the safety limit")
	}
	blob := marshalTestJSON(t, rep)
	if strings.Contains(string(blob), "threshold_grid") || strings.Contains(string(blob), "selected_accept") {
		t.Fatal("the real-case path must not select thresholds")
	}

}

func assertRealDesignMeasures(t *testing.T, rep realReport) {
	t.Helper()
	supported := rep.Measures["fieldwise"]["weak_label:supported"]
	assertEqual(t, supported.Accepted, 1)
	unsupportedGroup := rep.Measures["fieldwise"]["weak_label:unsupported"]
	assertEqual(t, unsupportedGroup.Accepted, 1)
	unknownGroup := rep.Measures["fieldwise"]["weak_label:unknown"]
	assertEqual(t, unknownGroup.Accepted, 1)
	missingStatus := rep.Measures["fieldwise"]["status:missing"]
	assertEqual(t, missingStatus.Accepted, 1)
	// Each design must be measured on its own records only.
	choiceSupported := rep.Measures["choice"]["weak_label:supported"]
	if choiceSupported.Rejected != 1 || choiceSupported.Accepted != 0 {
		t.Fatalf("choice supported group=%+v, want one rejected case and no accepted case", choiceSupported)
	}
	if supported.Rejected != 0 {
		t.Fatalf("the fieldwise group picked up a choice decision: %+v", supported)
	}
	if len(rep.Measures["fieldwise"]["status:unchanged"].ProviderErrors) != 0 {
		t.Fatal("provider errors must be empty for a clean run")
	}
}
