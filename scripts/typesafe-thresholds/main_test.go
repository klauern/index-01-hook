package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func thresholdTestRow(row gridRow) gridRow {
	row.FalseRejectRate = .1
	row.Accepted = 10
	row.Rejected = 8
	row.Review = int(row.ReviewVolume * 10)
	return row
}

func thresholdFixture(t *testing.T, live bool, rows []gridRow) (config, report) {
	t.Helper()
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "corpus.json")
	corpus := []byte(`{"cases":[]}`)
	if err := os.WriteFile(corpusPath, corpus, 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(corpus)
	r := report{Meta: reportMeta{Live: live, Model: "test-model", PromptVersion: "test-v1", CorpusPath: corpusPath, CorpusSHA256: hex.EncodeToString(h[:]), GeneratedAt: "2025-01-01T00:00:00Z", Designs: []string{"fieldwise"}}, Measures: map[string]map[string]measure{"fieldwise": {"heldout": {ThresholdGrid: rows}}}}
	reportPath := filepath.Join(dir, "report.json")
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reportPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	return config{Report: reportPath, Corpus: corpusPath, Out: "", FalseAcceptLimit: .02, ReviewVolumeLimit: .25, LimitsApproved: true}, r
}

func decodeThresholdOutput(t *testing.T, b []byte) output {
	t.Helper()
	var got output
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestThresholdSelectionTable(t *testing.T) {
	rows := []gridRow{
		thresholdTestRow(gridRow{Accept: .8, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .20, Agreement: .7, InjectionAccepts: 0}),
		thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .20, Agreement: .8, InjectionAccepts: 0}),
		thresholdTestRow(gridRow{Accept: .9, Reject: .3, FalseAcceptRate: .01, ReviewVolume: .10, Agreement: .9, InjectionAccepts: 0}),
	}
	cfg, _ := thresholdFixture(t, true, rows)
	b, err := run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeThresholdOutput(t, b).Designs[0]
	assertThreshold(t, got.SelectedAccept, .9)
	assertThreshold(t, got.SelectedReject, .3)
	if got.SurvivorCount != 3 || got.Measures.ReviewVolume != .1 {
		t.Fatalf("selection output: %+v", got)
	}
}

func TestInjectedRowsNeverSelectedAndAllInjectedReject(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{
		thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .001, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 1}),
		thresholdTestRow(gridRow{Accept: .8, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .2, Agreement: .8, InjectionAccepts: 0}),
	})
	got := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if *got.SelectedAccept != .8 || got.SurvivorCount != 1 {
		t.Fatalf("injected row selected: %+v", got)
	}
	cfg, _ = thresholdFixture(t, true, []gridRow{thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .001, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 1})})
	got = decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if got.Decision != "reject" || got.SelectedAccept != nil {
		t.Fatalf("all injected result: %+v", got)
	}
}

func TestTieBreaks(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{
		thresholdTestRow(gridRow{Accept: .8, Reject: .3, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .8, InjectionAccepts: 0}),
		thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0}),
		thresholdTestRow(gridRow{Accept: .9, Reject: .4, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .95, InjectionAccepts: 0}),
	})
	got := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if *got.SelectedAccept != .9 || *got.SelectedReject != .4 {
		t.Fatalf("tie break: %v/%v", *got.SelectedAccept, *got.SelectedReject)
	}
}

func TestNoFeasibleRowReportsDeferAndCandidates(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{
		thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .03, ReviewVolume: .1, Agreement: .8, InjectionAccepts: 0}),
		thresholdTestRow(gridRow{Accept: .8, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .4, Agreement: .7, InjectionAccepts: 0}),
	})
	got := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if got.Decision != "defer" {
		t.Fatalf("decision=%s, want defer", got.Decision)
	}
	if got.FailureCandidates.FewestLimitViolations == nil || got.FailureCandidates.LowestReviewVolume == nil {
		t.Fatalf("failed candidates: %+v", got)
	}
	if got.FailureCandidates.FewestLimitViolations.ViolationCount != 1 {
		t.Fatalf("fewest violations: %+v", got.FailureCandidates.FewestLimitViolations)
	}
}

func TestApprovalControlsEnable(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0})})
	cfg.LimitsApproved = false
	got := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if got.Decision != "provisional" || got.Decision == "enable" {
		t.Fatalf("unapproved decision: %s", got.Decision)
	}
	cfg.LimitsApproved = true
	got = decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if got.Decision != "enable" {
		t.Fatalf("approved decision: %s", got.Decision)
	}
}

func TestRequestedDesignAbsent(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0})})
	cfg.Design = "choice"
	if _, err := run(cfg); err == nil || !strings.Contains(err.Error(), "requested design \"choice\" is absent from meta.designs") {
		t.Fatalf("error=%v", err)
	}
}

func TestRefusals(t *testing.T) {
	tests := []struct {
		name string
		make func(*testing.T) (config, report)
		want string
	}{
		{"stale corpus", func(t *testing.T) (config, report) {
			cfg, r := thresholdFixture(t, true, []gridRow{thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0})})
			r.Meta.CorpusSHA256 = strings.Repeat("0", 64)
			b, _ := json.Marshal(r)
			if err := os.WriteFile(cfg.Report, b, 0600); err != nil {
				t.Fatal(err)
			}
			return cfg, r
		}, "corpus sha256 mismatch"},
		{"not live", func(t *testing.T) (config, report) {
			return thresholdFixture(t, false, []gridRow{thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0})})
		}, "meta.live is false"},
		{"missing heldout", func(t *testing.T) (config, report) {
			cfg, r := thresholdFixture(t, true, nil)
			r.Measures["fieldwise"] = map[string]measure{"development": {}}
			b, _ := json.Marshal(r)
			if err := os.WriteFile(cfg.Report, b, 0600); err != nil {
				t.Fatal(err)
			}
			return cfg, r
		}, "held-out split is missing"},
		{"heldout has failed calls", func(t *testing.T) (config, report) {
			cfg, r := thresholdFixture(t, true, []gridRow{thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0})})
			r.Measures["fieldwise"]["heldout"] = measure{Errors: 2, ThresholdGrid: r.Measures["fieldwise"]["heldout"].ThresholdGrid}
			b, _ := json.Marshal(r)
			if err := os.WriteFile(cfg.Report, b, 0600); err != nil {
				t.Fatal(err)
			}
			return cfg, r
		}, "has 2 failed calls"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, _ := tc.make(t)
			if _, err := run(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
}

func TestRunIsByteIdenticalAndGridOrderIndependent(t *testing.T) {
	rows := []gridRow{
		thresholdTestRow(gridRow{Accept: .8, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .2, Agreement: .8, InjectionAccepts: 0}),
		thresholdTestRow(gridRow{Accept: .9, Reject: .2, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0}),
		thresholdTestRow(gridRow{Accept: .9, Reject: .3, FalseAcceptRate: .01, ReviewVolume: .1, Agreement: .9, InjectionAccepts: 0}),
	}
	cfg, _ := thresholdFixture(t, true, rows)
	first := mustRun(t, cfg)
	second := mustRun(t, cfg)
	if string(first) != string(second) {
		t.Fatal("same inputs produced different bytes")
	}
	cfg, r := thresholdFixture(t, true, []gridRow{rows[2], rows[0], rows[1]})
	shuffled := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if *shuffled.SelectedAccept != .9 || *shuffled.SelectedReject != .3 {
		t.Fatalf("shuffled selection: %v/%v", *shuffled.SelectedAccept, *shuffled.SelectedReject)
	}
	_ = r
}

func mustRun(t *testing.T, cfg config) []byte {
	t.Helper()
	b, err := run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertThreshold(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatal("missing threshold")
	}
	if *got != want {
		t.Fatalf("threshold=%v, want %v", *got, want)
	}
}

func TestRequestedDesignsPreservesReportOrder(t *testing.T) {
	tests := []struct {
		name, spec      string
		available, want []string
		errorText       string
	}{
		{name: "all", spec: " all ", available: []string{"b", "a", "b"}, want: []string{"b", "a"}},
		{name: "requested order differs", spec: " a, b, a, ", available: []string{"b", "a", "b"}, want: []string{"b", "a"}},
		{name: "empty metadata name", available: []string{"", "a"}, want: []string{"", "a"}},
		{name: "unknown", spec: "missing,a", available: []string{"a"}, errorText: `requested design "missing" is absent from meta.designs`},
		{name: "none", spec: ",", available: []string{"a"}, errorText: "calibration report has no designs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := requestedDesigns(tc.spec, tc.available)
			if tc.errorText != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if err.Error() != tc.errorText {
					t.Fatalf("error=%v, want %s", err, tc.errorText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("designs=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestSensitivityUsesNearestMatchingAxis(t *testing.T) {
	selected := gridRow{Accept: .8, Reject: .3, Agreement: .7}
	rows := []gridRow{
		{Accept: .8, Reject: .6, Agreement: .9},
		{Accept: .8, Reject: .4, Agreement: .8},
		{Accept: .8, Reject: .4, Agreement: .1}, // Equal-distance duplicates retain the first row.
		{Accept: .8, Reject: .1}, {Accept: .8, Reject: .2},
		{Accept: .9, Reject: .3}, {Accept: 1, Reject: .3},
		{Accept: .7, Reject: .3}, {Accept: .6, Reject: .3},
		{Accept: .81, Reject: .31}, // A nearer diagonal row must not match either axis.
	}
	tests := []struct {
		name string
		axis sensitivityAxis
		want thresholdOutput
	}{
		{"reject up", sensitivityAxis{reject: true, up: true}, thresholdOutput{.8, .4}},
		{"reject down", sensitivityAxis{reject: true}, thresholdOutput{.8, .2}},
		{"accept up", sensitivityAxis{up: true}, thresholdOutput{.9, .3}},
		{"accept down", sensitivityAxis{}, thresholdOutput{.7, .3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := neighbor(rows, selected, tc.axis)
			if got == nil {
				t.Fatal("missing neighbor")
			}
			if got.Accept != tc.want.Accept || got.Reject != tc.want.Reject {
				t.Fatalf("neighbor=%+v, want %+v", got, tc.want)
			}
		})
	}
	got := neighbor(rows, selected, sensitivityAxis{reject: true, up: true})
	if got.Measures.Agreement != .8 {
		t.Fatalf("duplicate tie chose %+v", got)
	}
	if math.Abs(got.Change.Agreement-.1) > 1e-12 {
		t.Fatalf("agreement change=%v", got.Change.Agreement)
	}
	if neighbor([]gridRow{selected}, selected, sensitivityAxis{}) != nil {
		t.Fatal("equal threshold must not be a neighbor")
	}
}
