package evalcorpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validID(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\r\n\t")
}

func validTime(value string) bool {
	_, err := time.Parse(time.RFC3339Nano, value)
	return err == nil
}

func strictDecode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return errors.New("invalid corpus fields")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("expected one JSON document")
	}
	return nil
}

// Load reads a saved corpus. It validates declarations, not transcript provenance.
func Load(data []byte) (Corpus, error) {
	var c Corpus
	if err := strictDecode(data, &c); err != nil {
		return c, err
	}
	// A missing item index must not become an implicit zero during JSON decoding.
	var fields struct {
		Examples []struct {
			Targets []map[string]json.RawMessage `json:"targets"`
		} `json:"examples"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return c, errors.New("invalid corpus target fields")
	}
	for _, e := range fields.Examples {
		for _, t := range e.Targets {
			if index, exists := t["item_index"]; !exists || bytes.Equal(bytes.TrimSpace(index), []byte("null")) {
				return c, errors.New("target requires a known item_index")
			}
		}
	}
	return c, Validate(c)
}

func validateRouting(r *RoutingConfig) error {
	if r == nil {
		return nil
	}
	if !validRoutingFields(r) {
		return errors.New("routing requires a clock, named time zone, aliases, and default and note project IDs")
	}
	if _, err := time.LoadLocation(r.TimeZone); err != nil {
		return errors.New("routing time zone is invalid")
	}
	return validateAliases(r.Aliases)
}
func validRoutingFields(r *RoutingConfig) bool {
	clockAndZone := validTime(r.Clock) && r.TimeZone != "" && r.TimeZone != "Local"
	projects := validID(r.DefaultProjectID) && validID(r.NoteProjectID)
	return clockAndZone && projects && r.Aliases != nil
}
func validateAliases(values map[string]string) error {
	aliases := map[string]bool{}
	for name, id := range values {
		key := strings.ToLower(strings.TrimSpace(name))
		validPair := validID(name) && validID(id)
		if !validPair || aliases[key] {
			return errors.New("routing aliases contain an invalid or duplicate name or project ID")
		}
		aliases[key] = true
	}
	return nil
}

type corpusValidator struct{ ids, fingerprints, taskIDs map[string]bool }

// Validate checks the corpus schema. Eligibility reports incomplete examples separately.
func Validate(c Corpus) error {
	if err := validateHeader(c); err != nil {
		return err
	}
	v := corpusValidator{map[string]bool{}, map[string]bool{}, map[string]bool{}}
	for _, e := range c.Examples {
		if err := v.validateExample(e, c.Source.Kind); err != nil {
			return err
		}
	}
	return nil
}
func validateHeader(c Corpus) error {
	if c.FormatVersion != Version || c.Type != CorpusType {
		return errors.New("unsupported corpus schema or version")
	}
	if !validSourceKind(c.Source.Kind) {
		return errors.New("invalid corpus source kind")
	}
	if !fingerprintPattern.MatchString(c.Source.SHA256) || !validTime(c.Source.CollectedAt) {
		return errors.New("corpus source requires a SHA-256 digest and collection time")
	}
	if err := validateRouting(c.Routing); err != nil {
		return err
	}
	if len(c.Examples) == 0 {
		return errors.New("corpus requires at least one example")
	}
	return nil
}
func (v *corpusValidator) validateExample(e Example, sourceKind string) error {
	if err := validateRouting(e.Routing); err != nil {
		return err
	}
	if err := validateInputMetadata(e.Input); err != nil {
		return err
	}
	if err := v.validateExampleIdentity(e); err != nil {
		return err
	}
	if err := validateInputProvenance(e.Input, sourceKind); err != nil {
		return err
	}
	if err := validateExampleTargets(e); err != nil {
		return err
	}
	if err := v.validateTargets(e.Targets); err != nil {
		return err
	}
	return validateSavedOutput(e.SavedOutput)
}
func validateInputMetadata(input Input) error {
	if input.PromptSHA256 != "" && !fingerprintPattern.MatchString(input.PromptSHA256) {
		return errors.New("input prompt_sha256 must be a SHA-256 digest")
	}
	if input.Model != "" && !validID(input.Model) {
		return errors.New("input model must be a valid identifier")
	}
	return nil
}
func (v *corpusValidator) validateExampleIdentity(e Example) error {
	duplicate := v.ids[e.ID] || v.fingerprints[e.RecordingFingerprint]
	valid := validID(e.ID) && fingerprintPattern.MatchString(e.RecordingFingerprint)
	if !valid || duplicate {
		return errors.New("examples require unique IDs and recording fingerprints")
	}
	v.ids[e.ID], v.fingerprints[e.RecordingFingerprint] = true, true
	return validateExamplePartition(e)
}
func validateExamplePartition(e Example) error {
	if e.Split != "development" && e.Split != "held_out" {
		return errors.New("invalid example split")
	}
	if e.ExpectedItemCount != nil && *e.ExpectedItemCount <= 0 {
		return errors.New("expected_item_count must be positive when supplied")
	}
	return nil
}
func validateInputProvenance(input Input, sourceKind string) error {
	switch input.Provenance {
	case "original", "synthetic", "missing", "reconstructed":
	default:
		return errors.New("invalid input provenance")
	}
	if input.Provenance == "synthetic" && sourceKind != "synthetic" {
		return errors.New("synthetic input requires a declared synthetic corpus")
	}
	if input.Provenance == "missing" && input.Text != "" {
		return errors.New("missing input cannot contain text")
	}
	return nil
}
func validateExampleTargets(e Example) error {
	if e.Blockers == nil || len(e.Targets) == 0 {
		return errors.New("example requires targets and a blockers array")
	}
	for _, blocker := range e.Blockers {
		if strings.TrimSpace(blocker) == "" {
			return errors.New("empty example blocker")
		}
	}
	return nil
}
func validateSavedOutput(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return errors.New("saved_output must be an independently supplied JSON object")
	}
	return nil
}
func (v *corpusValidator) validateTargets(targets []Target) error {
	indices := map[int]bool{}
	for _, target := range targets {
		if err := v.validateTargetIdentity(target, indices); err != nil {
			return err
		}
		if err := validateTargetContent(target); err != nil {
			return err
		}
		if err := validateLabel(target.Label); err != nil {
			return err
		}
	}
	return nil
}
func (v *corpusValidator) validateTargetIdentity(target Target, indices map[int]bool) error {
	duplicate := v.taskIDs[target.TaskID] || indices[target.ItemIndex]
	valid := validID(target.TaskID) && target.ItemIndex >= 0
	if !valid || duplicate {
		return errors.New("targets require unique task IDs and item indices")
	}
	v.taskIDs[target.TaskID], indices[target.ItemIndex] = true, true
	return nil
}
func validateTargetContent(target Target) error {
	titleValid := strings.TrimSpace(target.Title) != "" && !strings.Contains(target.Title, "[index01:")
	kindValid := target.Kind == "task" || target.Kind == "note"
	valid := titleValid && kindValid && validID(target.ObservedProjectID)
	if !valid {
		return errors.New("invalid target title, kind, or observed project ID")
	}
	return nil
}
func validateLabel(label Label) error {
	if !validID(label.Basis) {
		return errors.New("target label requires a basis")
	}
	switch label.Status {
	case "approved", "inferred":
		if !validID(label.ProjectID) {
			return errors.New("approved or inferred label requires a project ID")
		}
	case "unlabeled", "blocked":
		if label.ProjectID != "" {
			return errors.New("unlabeled or blocked target cannot assert a project ID")
		}
	default:
		return errors.New("invalid target label status")
	}
	return nil
}

// EffectiveRouting selects a recording's configuration before the corpus default.
func EffectiveRouting(c Corpus, e Example) *RoutingConfig {
	if e.Routing != nil {
		return e.Routing
	}
	return c.Routing
}

// Eligibility reports why an example cannot be used for prompt evaluation.
func Eligibility(c Corpus, e Example) []string {
	routing := EffectiveRouting(c, e)
	blockers := append([]string{}, e.Blockers...)
	blockers = append(blockers, inputBlockers(e.Input, c.Source.Kind)...)
	blockers = append(blockers, routingBlockers(routing)...)
	blockers = append(blockers, completenessBlockers(e)...)
	blockers = append(blockers, targetBlockers(e.Targets, routing)...)
	sort.Strings(blockers)
	return uniqueBlockers(blockers)
}
func inputBlockers(input Input, sourceKind string) []string {
	blockers := []string{}
	if strings.TrimSpace(input.Text) == "" {
		blockers = append(blockers, "missing_original_transcript")
	}
	synthetic := input.Provenance == "synthetic" && sourceKind == "synthetic"
	if input.Provenance != "original" && !synthetic {
		blockers = append(blockers, "original_transcript_required")
	}
	return blockers
}
func routingBlockers(routing *RoutingConfig) []string {
	if routing == nil {
		return []string{"missing_routing_configuration"}
	}
	if validateRouting(routing) != nil {
		return []string{"invalid_routing_configuration"}
	}
	return nil
}
func completenessBlockers(e Example) []string {
	if e.ExpectedItemCount == nil {
		return []string{"unknown_recording_completeness"}
	}
	if *e.ExpectedItemCount != len(e.Targets) {
		return []string{"recording_item_count_mismatch"}
	}
	return nil
}
func targetBlockers(targets []Target, routing *RoutingConfig) []string {
	indices, matches := map[int]bool{}, map[string]bool{}
	blockers := []string{}
	for _, target := range targets {
		indices[target.ItemIndex] = true
		key := target.Kind + ":" + strings.ToLower(strings.Join(strings.Fields(target.Title), " "))
		if matches[key] {
			blockers = append(blockers, "ambiguous_target_title_and_kind")
		}
		matches[key] = true
		blockers = append(blockers, targetRouteBlockers(target, routing)...)
	}
	blockers = append(blockers, indexBlockers(indices, len(targets))...)
	return blockers
}
func targetRouteBlockers(target Target, routing *RoutingConfig) []string {
	blockers := []string{}
	if target.Label.Status != "approved" && target.Label.Status != "inferred" {
		blockers = append(blockers, "missing_or_blocked_route_label")
	}
	if routing != nil && target.Label.ProjectID != "" {
		if !supportedProject(target.Label.ProjectID, routing) {
			blockers = append(blockers, "unsupported_target_project")
		}
	}
	return blockers
}
func supportedProject(project string, routing *RoutingConfig) bool {
	if project == routing.DefaultProjectID || project == routing.NoteProjectID {
		return true
	}
	for _, id := range routing.Aliases {
		if project == id {
			return true
		}
	}
	return false
}
func indexBlockers(indices map[int]bool, count int) []string {
	for i := range count {
		if !indices[i] {
			return []string{"noncontiguous_item_indices"}
		}
	}
	if count == 0 {
		return []string{"missing_targets"}
	}
	return nil
}
func uniqueBlockers(blockers []string) []string {
	unique := []string{}
	for _, blocker := range blockers {
		if len(unique) == 0 || unique[len(unique)-1] != blocker {
			unique = append(unique, blocker)
		}
	}
	return unique
}

func invalidSource(what string) error { return fmt.Errorf("invalid corpus source: %s", what) }

func validSourceKind(kind string) bool {
	return kind == "snapshot" || kind == "routing_feedback_ledger" || kind == "synthetic"
}
