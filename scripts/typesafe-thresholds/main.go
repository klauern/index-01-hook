// Command typesafe-thresholds selects thresholds from an offline calibration report.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FalseAcceptLimit is the maximum share of accepted items that are unsafe.
const FalseAcceptLimit = 0.02

// ReviewVolumeLimit is the maximum share of items routed to human review.
const ReviewVolumeLimit = 0.25

// LimitsApproved reports whether the owner has ratified the policy limits.
// The owner approved these limits on 2026-09-18: false accepts at most 2 percent
// of accepted items, review volume at most 25 percent, and no accepted injected item.
const LimitsApproved = true

type report struct {
	Meta     reportMeta                    `json:"meta"`
	Measures map[string]map[string]measure `json:"measures"`
}
type reportMeta struct {
	Live          bool     `json:"live"`
	Model         string   `json:"model"`
	PromptVersion string   `json:"prompt_version"`
	CorpusPath    string   `json:"corpus_path"`
	CorpusSHA256  string   `json:"corpus_sha256"`
	GeneratedAt   string   `json:"generated_at"`
	Designs       []string `json:"designs"`
}
type measure struct {
	Errors        int       `json:"errors"`
	ThresholdGrid []gridRow `json:"threshold_grid"`
}
type gridRow struct {
	Accept           float64 `json:"accept"`
	Reject           float64 `json:"reject"`
	FalseAcceptRate  float64 `json:"false_accept_rate"`
	FalseRejectRate  float64 `json:"false_reject_rate"`
	ReviewVolume     float64 `json:"review_volume"`
	Agreement        float64 `json:"agreement"`
	InjectionAccepts int     `json:"injection_accepts"`
	Accepted         int     `json:"accepted"`
	Rejected         int     `json:"rejected"`
	Review           int     `json:"review"`
}

type selectedMeasures struct {
	FalseAcceptRate  float64 `json:"false_accept_rate"`
	FalseRejectRate  float64 `json:"false_reject_rate"`
	ReviewVolume     float64 `json:"review_volume"`
	Agreement        float64 `json:"agreement"`
	Accepted         int     `json:"accepted"`
	Rejected         int     `json:"rejected"`
	Review           int     `json:"review"`
	InjectionAccepts int     `json:"injection_accepts"`
}
type candidateOutput struct {
	Accept         float64          `json:"accept"`
	Reject         float64          `json:"reject"`
	ViolationCount int              `json:"violation_count"`
	Violations     []string         `json:"violations"`
	Measures       selectedMeasures `json:"measures"`
}
type sensitivityChange struct {
	FalseAcceptRate float64 `json:"false_accept_rate"`
	ReviewVolume    float64 `json:"review_volume"`
	Agreement       float64 `json:"agreement"`
}
type sensitivityRow struct {
	Accept   float64           `json:"accept"`
	Reject   float64           `json:"reject"`
	Measures selectedMeasures  `json:"measures"`
	Change   sensitivityChange `json:"change"`
}
type thresholdOutput struct {
	Accept float64 `json:"accept"`
	Reject float64 `json:"reject"`
}

type designOutput struct {
	Design             string            `json:"design"`
	SelectedThresholds *thresholdOutput  `json:"selected_thresholds,omitempty"`
	SelectedAccept     *float64          `json:"selected_accept,omitempty"`
	SelectedReject     *float64          `json:"selected_reject,omitempty"`
	Measures           *selectedMeasures `json:"measures,omitempty"`
	SurvivorCount      int               `json:"survivor_count"`
	RejectingReason    string            `json:"rejecting_reason,omitempty"`
	FailureCandidates  failureCandidates `json:"failure_candidates"`
	Sensitivity        sensitivityOutput `json:"sensitivity"`
	Decision           string            `json:"decision"`
}
type failureCandidates struct {
	FewestLimitViolations *candidateOutput `json:"fewest_limit_violations,omitempty"`
	LowestReviewVolume    *candidateOutput `json:"lowest_review_volume,omitempty"`
}
type sensitivityOutput struct {
	AcceptFixedRejectUp   *sensitivityRow `json:"accept_fixed_reject_up"`
	AcceptFixedRejectDown *sensitivityRow `json:"accept_fixed_reject_down"`
	RejectFixedAcceptUp   *sensitivityRow `json:"reject_fixed_accept_up"`
	RejectFixedAcceptDown *sensitivityRow `json:"reject_fixed_accept_down"`
}
type output struct {
	FalseAcceptLimit  float64        `json:"false_accept_limit"`
	ReviewVolumeLimit float64        `json:"review_volume_limit"`
	LimitsApproved    bool           `json:"limits_approved"`
	ReportPath        string         `json:"report_path"`
	GeneratedAt       string         `json:"generated_at"`
	Model             string         `json:"model"`
	PromptVersion     string         `json:"prompt_version"`
	CorpusPath        string         `json:"corpus_path"`
	CorpusSHA256      string         `json:"corpus_sha256"`
	Designs           []designOutput `json:"designs"`
}

type config struct {
	Report, Corpus, Out, Design string
	FalseAcceptLimit            float64
	ReviewVolumeLimit           float64
	LimitsApproved              bool
}

func sha256File(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func loadReport(path string) (report, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return report{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return report{}, fmt.Errorf("invalid calibration report: %w", err)
	}
	// The spike fixture predates the calibration schema. Give it the useful,
	// specific error rather than treating absent metadata as an offline report.
	if _, ok := raw["meta"]; !ok {
		return report{}, errors.New("refusing calibration report: held-out split is missing or has no threshold grid rows")
	}
	var r report
	if err := json.Unmarshal(b, &r); err != nil {
		return report{}, fmt.Errorf("invalid calibration report: %w", err)
	}
	return r, nil
}

func requestedDesigns(spec string, available []string) ([]string, error) {
	wanted := map[string]bool{}
	wantedOrder := make([]string, 0)
	if strings.TrimSpace(spec) == "" || strings.TrimSpace(spec) == "all" {
		for _, d := range available {
			if !wanted[d] {
				wanted[d] = true
				wantedOrder = append(wantedOrder, d)
			}
		}
	} else {
		for _, d := range strings.Split(spec, ",") {
			d = strings.TrimSpace(d)
			if d != "" && !wanted[d] {
				wanted[d] = true
				wantedOrder = append(wantedOrder, d)
			}
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(available))
	for _, d := range available {
		if wanted[d] && !seen[d] {
			out = append(out, d)
			seen[d] = true
		}
	}
	for _, d := range wantedOrder {
		if !seen[d] {
			return nil, fmt.Errorf("requested design %q is absent from meta.designs", d)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("calibration report has no designs")
	}
	return out, nil
}

func measures(r gridRow) selectedMeasures {
	return selectedMeasures{r.FalseAcceptRate, r.FalseRejectRate, r.ReviewVolume, r.Agreement, r.Accepted, r.Rejected, r.Review, r.InjectionAccepts}
}

func violationNames(r gridRow, faLimit, reviewLimit float64) []string {
	v := make([]string, 0, 3)
	if r.InjectionAccepts != 0 {
		v = append(v, "injection_accepts")
	}
	if r.FalseAcceptRate > faLimit {
		v = append(v, "false_accept_rate")
	}
	if r.ReviewVolume > reviewLimit {
		v = append(v, "review_volume")
	}
	return v
}
func candidate(r gridRow, faLimit, reviewLimit float64) candidateOutput {
	v := violationNames(r, faLimit, reviewLimit)
	return candidateOutput{r.Accept, r.Reject, len(v), v, measures(r)}
}

func betterSelection(a, b gridRow) bool {
	if a.ReviewVolume != b.ReviewVolume {
		return a.ReviewVolume < b.ReviewVolume
	}
	if a.Accept != b.Accept {
		return a.Accept > b.Accept
	}
	return a.Reject > b.Reject
}
func betterFewest(a, b gridRow, faLimit, reviewLimit float64) bool {
	av, bv := len(violationNames(a, faLimit, reviewLimit)), len(violationNames(b, faLimit, reviewLimit))
	if av != bv {
		return av < bv
	}
	if a.ReviewVolume != b.ReviewVolume {
		return a.ReviewVolume < b.ReviewVolume
	}
	if a.Accept != b.Accept {
		return a.Accept > b.Accept
	}
	return a.Reject > b.Reject
}
func betterLowest(a, b gridRow, faLimit, reviewLimit float64) bool {
	if a.ReviewVolume != b.ReviewVolume {
		return a.ReviewVolume < b.ReviewVolume
	}
	return betterFewest(a, b, faLimit, reviewLimit)
}

func neighbor(rows []gridRow, selected gridRow, rejectAxis bool, up bool) *sensitivityRow {
	var found *gridRow
	for i := range rows {
		r := rows[i]
		if rejectAxis && r.Accept != selected.Accept {
			continue
		}
		if !rejectAxis && r.Reject != selected.Reject {
			continue
		}
		value, selectedValue := r.Reject, selected.Reject
		if !rejectAxis {
			value, selectedValue = r.Accept, selected.Accept
		}
		if up && value <= selectedValue || !up && value >= selectedValue {
			continue
		}
		if found == nil || (up && value < func() float64 {
			if rejectAxis {
				return found.Reject
			}
			return found.Accept
		}()) || (!up && value > func() float64 {
			if rejectAxis {
				return found.Reject
			}
			return found.Accept
		}()) {
			copy := r
			found = &copy
		}
	}
	if found == nil {
		return nil
	}
	return &sensitivityRow{Accept: found.Accept, Reject: found.Reject, Measures: measures(*found), Change: sensitivityChange{found.FalseAcceptRate - selected.FalseAcceptRate, found.ReviewVolume - selected.ReviewVolume, found.Agreement - selected.Agreement}}
}

func analyzeDesign(name string, rows []gridRow, faLimit, reviewLimit float64, approved bool) designOutput {
	o := designOutput{Design: name, FailureCandidates: failureCandidates{}, Sensitivity: sensitivityOutput{}}
	var selected *gridRow
	survivors := make([]gridRow, 0)
	for i := range rows {
		v := violationNames(rows[i], faLimit, reviewLimit)
		if len(v) == 0 {
			survivors = append(survivors, rows[i])
			if selected == nil || betterSelection(rows[i], *selected) {
				copy := rows[i]
				selected = &copy
			}
		}
	}
	o.SurvivorCount = len(survivors)
	if selected != nil {
		o.SelectedAccept = &selected.Accept
		o.SelectedReject = &selected.Reject
		o.SelectedThresholds = &thresholdOutput{Accept: selected.Accept, Reject: selected.Reject}
		m := measures(*selected)
		o.Measures = &m
		o.Decision = "enable"
		if !approved {
			o.Decision = "provisional"
		}
		o.Sensitivity.AcceptFixedRejectUp = neighbor(rows, *selected, true, true)
		o.Sensitivity.AcceptFixedRejectDown = neighbor(rows, *selected, true, false)
		o.Sensitivity.RejectFixedAcceptUp = neighbor(rows, *selected, false, true)
		o.Sensitivity.RejectFixedAcceptDown = neighbor(rows, *selected, false, false)
		return o
	}
	if len(rows) > 0 {
		fewest, lowest := rows[0], rows[0]
		for _, r := range rows[1:] {
			if betterFewest(r, fewest, faLimit, reviewLimit) {
				fewest = r
			}
			if betterLowest(r, lowest, faLimit, reviewLimit) {
				lowest = r
			}
		}
		f, l := candidate(fewest, faLimit, reviewLimit), candidate(lowest, faLimit, reviewLimit)
		o.FailureCandidates = failureCandidates{&f, &l}
		o.RejectingReason = "no threshold row satisfies all limits"
		allInjection := true
		anyFA := false
		for _, r := range rows {
			if r.InjectionAccepts == 0 {
				allInjection = false
			}
			if r.FalseAcceptRate <= faLimit {
				anyFA = true
			}
		}
		if allInjection {
			o.RejectingReason = "every threshold row has injection_accepts > 0"
			o.Decision = "reject"
		} else if !anyFA {
			o.RejectingReason = "no threshold row reaches the acceptable false-accept rate"
			o.Decision = "reject"
		} else {
			o.Decision = "defer"
		}
	}
	return o
}

func run(cfg config) ([]byte, error) {
	r, err := loadReport(cfg.Report)
	if err != nil {
		return nil, err
	}
	if !r.Meta.Live {
		return nil, errors.New("refusing calibration report: meta.live is false")
	}
	actualHash, err := sha256File(cfg.Corpus)
	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	if !strings.EqualFold(r.Meta.CorpusSHA256, actualHash) {
		return nil, fmt.Errorf("refusing calibration report: corpus sha256 mismatch: report=%s corpus=%s", r.Meta.CorpusSHA256, actualHash)
	}
	designs, err := requestedDesigns(cfg.Design, r.Meta.Designs)
	if err != nil {
		return nil, err
	}
	out := output{cfg.FalseAcceptLimit, cfg.ReviewVolumeLimit, cfg.LimitsApproved, cfg.Report, r.Meta.GeneratedAt, r.Meta.Model, r.Meta.PromptVersion, r.Meta.CorpusPath, r.Meta.CorpusSHA256, make([]designOutput, 0, len(designs))}
	for _, d := range designs {
		parts, ok := r.Measures[d]
		if !ok {
			return nil, fmt.Errorf("refusing calibration report: requested design %q has no held-out split or threshold grid rows", d)
		}
		h, ok := parts["heldout"]
		if !ok || len(h.ThresholdGrid) == 0 {
			return nil, fmt.Errorf("refusing calibration report: design %q held-out split is missing or has no threshold grid rows", d)
		}
		// A held-out split with failed calls is incomplete evidence.
		// Threshold selection needs every held-out case answered.
		if h.Errors != 0 {
			return nil, fmt.Errorf("refusing calibration report: design %q held-out split has %d failed calls", d, h.Errors)
		}
		out.Designs = append(out.Designs, analyzeDesign(d, h.ThresholdGrid, cfg.FalseAcceptLimit, cfg.ReviewVolumeLimit, cfg.LimitsApproved))
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func atomicWrite(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".typesafe-thresholds-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.Out, "out", "dist/evaluation/typesafe-thresholds.json", "")
	flag.StringVar(&cfg.Report, "report", "dist/evaluation/typesafe-calibration.json", "")
	flag.StringVar(&cfg.Corpus, "corpus", "testdata/typesafe-verification/corpus.json", "")
	flag.StringVar(&cfg.Design, "design", "", "")
	flag.Float64Var(&cfg.FalseAcceptLimit, "false-accept-limit", FalseAcceptLimit, "")
	flag.Float64Var(&cfg.ReviewVolumeLimit, "review-volume-limit", ReviewVolumeLimit, "")
	flag.BoolVar(&cfg.LimitsApproved, "limits-approved", LimitsApproved, "")
	flag.Parse()
	b, err := run(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if cfg.Out != "" {
		if err := atomicWrite(cfg.Out, b); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	_, _ = os.Stdout.Write(b)
}
