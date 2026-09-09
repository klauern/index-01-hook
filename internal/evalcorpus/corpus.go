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
	if !validTime(r.Clock) || r.TimeZone == "" || r.TimeZone == "Local" || r.Aliases == nil || !validID(r.DefaultProjectID) || !validID(r.NoteProjectID) {
		return errors.New("routing requires a clock, named time zone, aliases, and default and note project IDs")
	}
	if _, err := time.LoadLocation(r.TimeZone); err != nil {
		return errors.New("routing time zone is invalid")
	}
	aliases := map[string]bool{}
	for name, id := range r.Aliases {
		key := strings.ToLower(strings.TrimSpace(name))
		if !validID(name) || !validID(id) || aliases[key] {
			return errors.New("routing aliases contain an invalid or duplicate name or project ID")
		}
		aliases[key] = true
	}
	return nil
}

// Validate checks the corpus schema. Eligibility reports incomplete examples separately.
func Validate(c Corpus) error {
	if c.FormatVersion != Version || c.Type != CorpusType {
		return errors.New("unsupported corpus schema or version")
	}
	if c.Source.Kind != "snapshot" && c.Source.Kind != "routing_feedback_ledger" && c.Source.Kind != "synthetic" {
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
	ids, fingerprints, taskIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range c.Examples {
		if err := validateRouting(e.Routing); err != nil {
			return err
		}
		if e.Input.PromptSHA256 != "" && !fingerprintPattern.MatchString(e.Input.PromptSHA256) {
			return errors.New("input prompt_sha256 must be a SHA-256 digest")
		}
		if e.Input.Model != "" && !validID(e.Input.Model) {
			return errors.New("input model must be a valid identifier")
		}
		if !validID(e.ID) || ids[e.ID] || !fingerprintPattern.MatchString(e.RecordingFingerprint) || fingerprints[e.RecordingFingerprint] {
			return errors.New("examples require unique IDs and recording fingerprints")
		}
		ids[e.ID], fingerprints[e.RecordingFingerprint] = true, true
		if e.Split != "development" && e.Split != "held_out" {
			return errors.New("invalid example split")
		}
		if e.ExpectedItemCount != nil && *e.ExpectedItemCount <= 0 {
			return errors.New("expected_item_count must be positive when supplied")
		}
		if e.Input.Provenance != "original" && e.Input.Provenance != "synthetic" && e.Input.Provenance != "missing" && e.Input.Provenance != "reconstructed" {
			return errors.New("invalid input provenance")
		}
		if e.Input.Provenance == "synthetic" && c.Source.Kind != "synthetic" {
			return errors.New("synthetic input requires a declared synthetic corpus")
		}
		if e.Input.Provenance == "missing" && e.Input.Text != "" {
			return errors.New("missing input cannot contain text")
		}
		if e.Blockers == nil || len(e.Targets) == 0 {
			return errors.New("example requires targets and a blockers array")
		}
		for _, blocker := range e.Blockers {
			if strings.TrimSpace(blocker) == "" {
				return errors.New("empty example blocker")
			}
		}
		indices := map[int]bool{}
		for _, target := range e.Targets {
			if !validID(target.TaskID) || taskIDs[target.TaskID] || target.ItemIndex < 0 || indices[target.ItemIndex] {
				return errors.New("targets require unique task IDs and item indices")
			}
			taskIDs[target.TaskID], indices[target.ItemIndex] = true, true
			if strings.TrimSpace(target.Title) == "" || strings.Contains(target.Title, "[index01:") || (target.Kind != "task" && target.Kind != "note") || !validID(target.ObservedProjectID) {
				return errors.New("invalid target title, kind, or observed project ID")
			}
			if !validID(target.Label.Basis) {
				return errors.New("target label requires a basis")
			}
			switch target.Label.Status {
			case "approved", "inferred":
				if !validID(target.Label.ProjectID) {
					return errors.New("approved or inferred label requires a project ID")
				}
			case "unlabeled", "blocked":
				if target.Label.ProjectID != "" {
					return errors.New("unlabeled or blocked target cannot assert a project ID")
				}
			default:
				return errors.New("invalid target label status")
			}
		}
		if len(e.SavedOutput) > 0 {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(e.SavedOutput, &object); err != nil || object == nil {
				return errors.New("saved_output must be an independently supplied JSON object")
			}
		}
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
	if strings.TrimSpace(e.Input.Text) == "" {
		blockers = append(blockers, "missing_original_transcript")
	}
	if e.Input.Provenance != "original" && !(e.Input.Provenance == "synthetic" && c.Source.Kind == "synthetic") {
		blockers = append(blockers, "original_transcript_required")
	}
	if routing == nil {
		blockers = append(blockers, "missing_routing_configuration")
	} else if validateRouting(routing) != nil {
		blockers = append(blockers, "invalid_routing_configuration")
	}
	indices, matches := map[int]bool{}, map[string]bool{}
	if e.ExpectedItemCount == nil {
		blockers = append(blockers, "unknown_recording_completeness")
	} else if *e.ExpectedItemCount != len(e.Targets) {
		blockers = append(blockers, "recording_item_count_mismatch")
	}
	for _, target := range e.Targets {
		indices[target.ItemIndex] = true
		key := target.Kind + ":" + strings.ToLower(strings.Join(strings.Fields(target.Title), " "))
		if matches[key] {
			blockers = append(blockers, "ambiguous_target_title_and_kind")
		}
		matches[key] = true
		if target.Label.Status != "approved" && target.Label.Status != "inferred" {
			blockers = append(blockers, "missing_or_blocked_route_label")
		}
		if routing != nil && target.Label.ProjectID != "" {
			resolved := target.Label.ProjectID == routing.DefaultProjectID || target.Label.ProjectID == routing.NoteProjectID
			for _, id := range routing.Aliases {
				resolved = resolved || target.Label.ProjectID == id
			}
			if !resolved {
				blockers = append(blockers, "unsupported_target_project")
			}
		}
	}
	for i := range len(e.Targets) {
		if !indices[i] {
			blockers = append(blockers, "noncontiguous_item_indices")
			break
		}
	}
	if len(e.Targets) == 0 {
		blockers = append(blockers, "missing_targets")
	}
	sort.Strings(blockers)
	unique := []string{}
	for _, blocker := range blockers {
		if len(unique) == 0 || unique[len(unique)-1] != blocker {
			unique = append(unique, blocker)
		}
	}
	return unique
}

func invalidSource(what string) error { return fmt.Errorf("invalid corpus source: %s", what) }
