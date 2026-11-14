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

// requestedDesigns preserves the report order and rejects unknown requested designs.
func requestedDesigns(spec string, available []string) ([]string, error) {
	wantedOrder := requestedNames(spec, available)
	wanted := designSet(wantedOrder)
	out := selectAvailableDesigns(available, wanted)
	seen := designSet(out)
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

// requestedNames parses explicit names without changing names in report metadata.
func requestedNames(spec string, available []string) []string {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "all" {
		return available
	}
	names := make([]string, 0)
	for _, name := range strings.Split(spec, ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func designSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		set[name] = true
	}
	return set
}

// selectAvailableDesigns keeps only the first occurrence of each requested name.
func selectAvailableDesigns(available []string, wanted map[string]bool) []string {
	out := make([]string, 0, len(available))
	seen := map[string]bool{}
	for _, name := range available {
		if !wanted[name] || seen[name] {
			continue
		}
		out = append(out, name)
		seen[name] = true
	}
	return out
}

func measures(r gridRow) selectedMeasures {
	return selectedMeasures{r.FalseAcceptRate, r.FalseRejectRate, r.ReviewVolume, r.Agreement, r.Accepted, r.Rejected, r.Review, r.InjectionAccepts}
}

func (policy selectionPolicy) violationNames(r gridRow) []string {
	v := make([]string, 0, 3)
	if r.InjectionAccepts != 0 {
		v = append(v, "injection_accepts")
	}
	if r.FalseAcceptRate > policy.falseAcceptLimit {
		v = append(v, "false_accept_rate")
	}
	if r.ReviewVolume > policy.reviewVolumeLimit {
		v = append(v, "review_volume")
	}
	return v
}
func (policy selectionPolicy) candidate(r gridRow) candidateOutput {
	v := policy.violationNames(r)
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
func (policy selectionPolicy) betterFewest(a, b gridRow) bool {
	av, bv := len(policy.violationNames(a)), len(policy.violationNames(b))
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
func (policy selectionPolicy) betterLowest(a, b gridRow) bool {
	if a.ReviewVolume != b.ReviewVolume {
		return a.ReviewVolume < b.ReviewVolume
	}
	return policy.betterFewest(a, b)
}

// sensitivityAxis selects one changing threshold and one fixed threshold.
type sensitivityAxis struct {
	reject bool
	up     bool
}

func (axis sensitivityAxis) values(row gridRow) (varying, fixed float64) {
	if axis.reject {
		return row.Reject, row.Accept
	}
	return row.Accept, row.Reject
}

// matches requires a fixed threshold and a varying threshold on the requested side.
func (axis sensitivityAxis) matches(row, selected gridRow) bool {
	value, fixed := axis.values(row)
	baseline, selectedFixed := axis.values(selected)
	if fixed != selectedFixed {
		return false
	}
	if axis.up {
		return !(value <= baseline)
	}
	return !(value >= baseline)
}

func (axis sensitivityAxis) closer(row gridRow, found *gridRow) bool {
	if found == nil {
		return true
	}
	value, _ := axis.values(row)
	previous, _ := axis.values(*found)
	if axis.up {
		return value < previous
	}
	return value > previous
}

// neighbor returns the closest row on one side of the selected threshold.
func neighbor(rows []gridRow, selected gridRow, axis sensitivityAxis) *sensitivityRow {
	var found *gridRow
	for i := range rows {
		if !axis.matches(rows[i], selected) {
			continue
		}
		if axis.closer(rows[i], found) {
			copy := rows[i]
			found = &copy
		}
	}
	if found == nil {
		return nil
	}
	return &sensitivityRow{Accept: found.Accept, Reject: found.Reject, Measures: measures(*found), Change: sensitivityChange{found.FalseAcceptRate - selected.FalseAcceptRate, found.ReviewVolume - selected.ReviewVolume, found.Agreement - selected.Agreement}}
}

type selectionPolicy struct {
	falseAcceptLimit, reviewVolumeLimit float64
	approved                            bool
}

func (policy selectionPolicy) selectRow(rows []gridRow) (*gridRow, int) {
	var selected *gridRow
	survivors := 0
	for i := range rows {
		if len(policy.violationNames(rows[i])) != 0 {
			continue
		}
		survivors++
		if selected == nil || betterSelection(rows[i], *selected) {
			copy := rows[i]
			selected = &copy
		}
	}
	return selected, survivors
}

func selectedDesign(name string, selected gridRow, survivors int) designOutput {
	m := measures(selected)
	return designOutput{Design: name, SurvivorCount: survivors, SelectedAccept: &selected.Accept, SelectedReject: &selected.Reject, SelectedThresholds: &thresholdOutput{Accept: selected.Accept, Reject: selected.Reject}, Measures: &m, Decision: "enable"}
}

func (policy selectionPolicy) failureCandidates(rows []gridRow) failureCandidates {
	fewest, lowest := rows[0], rows[0]
	for _, r := range rows[1:] {
		if policy.betterFewest(r, fewest) {
			fewest = r
		}
		if policy.betterLowest(r, lowest) {
			lowest = r
		}
	}
	f, l := policy.candidate(fewest), policy.candidate(lowest)
	return failureCandidates{&f, &l}
}

func (policy selectionPolicy) rejection(rows []gridRow) (reason, decision string) {
	allInjection, anyFA := true, false
	for _, r := range rows {
		if r.InjectionAccepts == 0 {
			allInjection = false
		}
		if r.FalseAcceptRate <= policy.falseAcceptLimit {
			anyFA = true
		}
	}
	if allInjection {
		return "every threshold row has injection_accepts > 0", "reject"
	}
	if !anyFA {
		return "no threshold row reaches the acceptable false-accept rate", "reject"
	}
	return "no threshold row satisfies all limits", "defer"
}

// analyzeDesign selects a feasible row or reports the unchanged policy failure.
func analyzeDesign(name string, rows []gridRow, policy selectionPolicy) designOutput {
	selected, survivors := policy.selectRow(rows)
	if selected == nil {
		o := designOutput{Design: name, SurvivorCount: survivors}
		if len(rows) > 0 {
			o.FailureCandidates = policy.failureCandidates(rows)
			o.RejectingReason, o.Decision = policy.rejection(rows)
		}
		return o
	}
	o := selectedDesign(name, *selected, survivors)
	if !policy.approved {
		o.Decision = "provisional"
	}
	o.Sensitivity.AcceptFixedRejectUp = neighbor(rows, *selected, sensitivityAxis{reject: true, up: true})
	o.Sensitivity.AcceptFixedRejectDown = neighbor(rows, *selected, sensitivityAxis{reject: true})
	o.Sensitivity.RejectFixedAcceptUp = neighbor(rows, *selected, sensitivityAxis{up: true})
	o.Sensitivity.RejectFixedAcceptDown = neighbor(rows, *selected, sensitivityAxis{})
	return o
}

// heldoutRows refuses incomplete evidence before threshold selection.
func heldoutRows(r report, design string) ([]gridRow, error) {
	parts, ok := r.Measures[design]
	if !ok {
		return nil, fmt.Errorf("refusing calibration report: requested design %q has no held-out split or threshold grid rows", design)
	}
	h, ok := parts["heldout"]
	if !ok || len(h.ThresholdGrid) == 0 {
		return nil, fmt.Errorf("refusing calibration report: design %q held-out split is missing or has no threshold grid rows", design)
	}
	if h.Errors != 0 {
		return nil, fmt.Errorf("refusing calibration report: design %q held-out split has %d failed calls", design, h.Errors)
	}
	return h.ThresholdGrid, nil
}

func verifyReportCorpus(r report, corpusPath string) error {
	if !r.Meta.Live {
		return errors.New("refusing calibration report: meta.live is false")
	}
	actualHash, err := sha256File(corpusPath)
	if err != nil {
		return fmt.Errorf("read corpus: %w", err)
	}
	if !strings.EqualFold(r.Meta.CorpusSHA256, actualHash) {
		return fmt.Errorf("refusing calibration report: corpus sha256 mismatch: report=%s corpus=%s", r.Meta.CorpusSHA256, actualHash)
	}
	return nil
}

func run(cfg config) ([]byte, error) {
	r, err := loadReport(cfg.Report)
	if err != nil {
		return nil, err
	}
	if err := verifyReportCorpus(r, cfg.Corpus); err != nil {
		return nil, err
	}
	designs, err := requestedDesigns(cfg.Design, r.Meta.Designs)
	if err != nil {
		return nil, err
	}
	out := output{cfg.FalseAcceptLimit, cfg.ReviewVolumeLimit, cfg.LimitsApproved, cfg.Report, r.Meta.GeneratedAt, r.Meta.Model, r.Meta.PromptVersion, r.Meta.CorpusPath, r.Meta.CorpusSHA256, make([]designOutput, 0, len(designs))}
	for _, d := range designs {
		rows, err := heldoutRows(r, d)
		if err != nil {
			return nil, err
		}
		out.Designs = append(out.Designs, analyzeDesign(d, rows, selectionPolicy{cfg.FalseAcceptLimit, cfg.ReviewVolumeLimit, cfg.LimitsApproved}))
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
