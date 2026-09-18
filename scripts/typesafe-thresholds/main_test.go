package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func thresholdTestRow(a, r, fa, review, agreement float64, injection int) gridRow {
	return gridRow{Accept: a, Reject: r, FalseAcceptRate: fa, FalseRejectRate: .1, ReviewVolume: review, Agreement: agreement, InjectionAccepts: injection, Accepted: 10, Rejected: 8, Review: int(review * 10)}
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
		thresholdTestRow(.8, .2, .01, .20, .7, 0),
		thresholdTestRow(.9, .2, .01, .20, .8, 0),
		thresholdTestRow(.9, .3, .01, .10, .9, 0),
	}
	cfg, _ := thresholdFixture(t, true, rows)
	b, err := run(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := decodeThresholdOutput(t, b).Designs[0]
	if got.SelectedAccept == nil || *got.SelectedAccept != .9 || got.SelectedReject == nil || *got.SelectedReject != .3 {
		t.Fatalf("selected thresholds: %v/%v", got.SelectedAccept, got.SelectedReject)
	}
	if got.SurvivorCount != 3 || got.Measures.ReviewVolume != .1 {
		t.Fatalf("selection output: %+v", got)
	}
}

func TestInjectedRowsNeverSelectedAndAllInjectedReject(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{
		thresholdTestRow(.9, .2, .001, .1, .9, 1),
		thresholdTestRow(.8, .2, .01, .2, .8, 0),
	})
	got := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if *got.SelectedAccept != .8 || got.SurvivorCount != 1 {
		t.Fatalf("injected row selected: %+v", got)
	}
	cfg, _ = thresholdFixture(t, true, []gridRow{thresholdTestRow(.9, .2, .001, .1, .9, 1)})
	got = decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if got.Decision != "reject" || got.SelectedAccept != nil {
		t.Fatalf("all injected result: %+v", got)
	}
}

func TestTieBreaks(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{
		thresholdTestRow(.8, .3, .01, .1, .8, 0),
		thresholdTestRow(.9, .2, .01, .1, .9, 0),
		thresholdTestRow(.9, .4, .01, .1, .95, 0),
	})
	got := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if *got.SelectedAccept != .9 || *got.SelectedReject != .4 {
		t.Fatalf("tie break: %v/%v", *got.SelectedAccept, *got.SelectedReject)
	}
}

func TestNoFeasibleRowReportsDeferAndCandidates(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{
		thresholdTestRow(.9, .2, .03, .1, .8, 0),
		thresholdTestRow(.8, .2, .01, .4, .7, 0),
	})
	got := decodeThresholdOutput(t, mustRun(t, cfg)).Designs[0]
	if got.Decision != "defer" || got.FailureCandidates.FewestLimitViolations == nil || got.FailureCandidates.LowestReviewVolume == nil {
		t.Fatalf("failed candidates: %+v", got)
	}
	if got.FailureCandidates.FewestLimitViolations.ViolationCount != 1 {
		t.Fatalf("fewest violations: %+v", got.FailureCandidates.FewestLimitViolations)
	}
}

func TestApprovalControlsEnable(t *testing.T) {
	cfg, _ := thresholdFixture(t, true, []gridRow{thresholdTestRow(.9, .2, .01, .1, .9, 0)})
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
	cfg, _ := thresholdFixture(t, true, []gridRow{thresholdTestRow(.9, .2, .01, .1, .9, 0)})
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
			cfg, r := thresholdFixture(t, true, []gridRow{thresholdTestRow(.9, .2, .01, .1, .9, 0)})
			r.Meta.CorpusSHA256 = strings.Repeat("0", 64)
			b, _ := json.Marshal(r)
			if err := os.WriteFile(cfg.Report, b, 0600); err != nil {
				t.Fatal(err)
			}
			return cfg, r
		}, "corpus sha256 mismatch"},
		{"not live", func(t *testing.T) (config, report) {
			return thresholdFixture(t, false, []gridRow{thresholdTestRow(.9, .2, .01, .1, .9, 0)})
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
			cfg, r := thresholdFixture(t, true, []gridRow{thresholdTestRow(.9, .2, .01, .1, .9, 0)})
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
		thresholdTestRow(.8, .2, .01, .2, .8, 0),
		thresholdTestRow(.9, .2, .01, .1, .9, 0),
		thresholdTestRow(.9, .3, .01, .1, .9, 0),
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
