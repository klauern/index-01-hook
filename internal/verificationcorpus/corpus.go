package verificationcorpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	errInvalidCorpus  = errors.New("invalid TypeSafe verification corpus")
	identifierPattern = regexp.MustCompile(`\b[0-9a-f]{24}\b`)
	bearerPattern     = regexp.MustCompile(`(?i)bearer\s`)
	secretPattern     = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]+`)
)

// Load reads and validates a TypeSafe verification corpus from path.
func Load(path string) (Corpus, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Corpus{}, fmt.Errorf("read corpus: %w", err)
	}
	var corpus Corpus
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode corpus: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Corpus{}, fmt.Errorf("decode corpus: %w: expected one JSON document", errInvalidCorpus)
		}
		return Corpus{}, fmt.Errorf("decode corpus: %w: expected one JSON document", err)
	}
	if err := Validate(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// Validate checks the corpus contract.
func Validate(corpus Corpus) error {
	if err := validateHeader(corpus); err != nil {
		return err
	}
	state := corpusValidation{seen: map[string]bool{}, splits: map[string]int{}, covered: map[string]bool{}}
	for _, item := range corpus.Cases {
		if err := state.add(item); err != nil {
			return err
		}
	}
	return state.finish()
}

// validateHeader checks corpus metadata before the case list.
func validateHeader(corpus Corpus) error {
	if corpus.FormatVersion != FormatVersion {
		return invalid("format_version must be 1, got %d", corpus.FormatVersion)
	}
	if corpus.Type != CorpusType {
		return invalid("type must be %q", CorpusType)
	}
	if corpus.Source.Kind != "synthetic" {
		return invalid("source.kind must be synthetic")
	}
	if err := validateTextValues("source description", []string{corpus.Source.Description}); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, corpus.Source.GeneratedAt); err != nil {
		return invalid("source.generated_at must be RFC3339")
	}
	if len(corpus.Cases) == 0 {
		return invalid("cases must not be empty")
	}
	return nil
}

// corpusValidation collects identity, split, and failure coverage in source order.
type corpusValidation struct {
	seen    map[string]bool
	splits  map[string]int
	covered map[string]bool
}

func (state *corpusValidation) add(item Case) error {
	if strings.TrimSpace(item.ID) == "" {
		return invalid("case id must not be empty")
	}
	if state.seen[item.ID] {
		return invalid("duplicate case id %q", item.ID)
	}
	state.seen[item.ID] = true
	if err := validateCaseLabel(item); err != nil {
		return err
	}
	if item.Label == "unsupported" {
		state.covered[item.FailureType] = true
	}
	if item.Split != "development" && item.Split != "heldout" {
		return invalid("case %q split must be development or heldout", item.ID)
	}
	state.splits[item.Split]++
	return validateCaseEvidence(item)
}

func validateCaseLabel(item Case) error {
	if strings.TrimSpace(item.Transcript) == "" {
		return invalid("case %q transcript must not be empty", item.ID)
	}
	if item.Label != "supported" && item.Label != "unsupported" {
		return invalid("case %q label must be supported or unsupported", item.ID)
	}
	if item.Label == "unsupported" {
		return validateFailureLabel(item)
	}
	if item.FailureType != "" || item.ReasonCode != "" {
		return invalid("case %q supported label cannot have failure_type or reason_code", item.ID)
	}
	return nil
}

func validateFailureLabel(item Case) error {
	if item.FailureType == "" {
		return invalid("case %q unsupported label requires failure_type", item.ID)
	}
	if !knownFailureType(item.FailureType) {
		return invalid("case %q has unknown failure_type %q", item.ID, item.FailureType)
	}
	if strings.TrimSpace(item.ReasonCode) == "" {
		return invalid("case %q unsupported label requires reason_code", item.ID)
	}
	return nil
}

func validateCaseEvidence(item Case) error {
	if item.Provenance != "synthetic" {
		return invalid("case %q provenance must be synthetic", item.ID)
	}
	if err := validateText(item); err != nil {
		return err
	}
	return validateGrounding(item)
}

func (state *corpusValidation) finish() error {
	if state.splits["development"] == 0 {
		return invalid("cases must include development cases")
	}
	if state.splits["heldout"] == 0 {
		return invalid("cases must include heldout cases")
	}
	missing := missingFields(FailureTypes, state.covered)
	if len(missing) != 0 {
		return invalid("missing failure types: %s", strings.Join(missing, ", "))
	}
	return nil
}

var groundedFieldNames = map[string]bool{
	"kind":          true,
	"title":         true,
	"all_day":       true,
	"content":       true,
	"due":           true,
	"priority":      true,
	"tags":          true,
	"project_alias": true,
}

func informationBearingFields(candidate CandidateItem) []string {
	fields := []string{"kind", "title"}
	if candidate.AllDay {
		fields = append(fields, "all_day")
	}
	if candidate.Content != "" {
		fields = append(fields, "content")
	}
	if candidate.Due != "" {
		fields = append(fields, "due")
	}
	if candidate.Priority != 0 {
		fields = append(fields, "priority")
	}
	if len(candidate.Tags) != 0 {
		fields = append(fields, "tags")
	}
	if candidate.ProjectAlias != "" {
		fields = append(fields, "project_alias")
	}
	return fields
}

func validateGrounding(item Case) error {
	if item.Candidate.Kind == "" {
		return invalid("case %q candidate kind must be non-empty", item.ID)
	}
	if item.Candidate.Title == "" {
		return invalid("case %q candidate title must be non-empty", item.ID)
	}
	if err := validateCandidateShape(item); err != nil {
		return err
	}
	grounded, err := groundedFields(item)
	if err != nil {
		return err
	}
	informationBearing := informationBearingFields(item.Candidate)
	for field := range grounded {
		if !slices.Contains(informationBearing, field) {
			return invalid("case %q grounds field %q that the candidate does not carry", item.ID, field)
		}
	}
	return validateMissingGrounding(item, missingFields(informationBearing, grounded))
}

func groundedFields(item Case) (map[string]bool, error) {
	grounded := make(map[string]bool, len(item.GroundedFields))
	for _, field := range item.GroundedFields {
		if field == "" {
			return nil, invalid("case %q grounded_fields must not contain empty values", item.ID)
		}
		if !groundedFieldNames[field] {
			return nil, invalid("case %q grounded_fields contains unsupported value %q", item.ID, field)
		}
		if grounded[field] {
			return nil, invalid("case %q grounded_fields contains duplicate value %q", item.ID, field)
		}
		grounded[field] = true
	}
	return grounded, nil
}

// missingFields preserves the expected field order in diagnostics.
func missingFields(fields []string, present map[string]bool) []string {
	missing := make([]string, 0, len(fields))
	for _, field := range fields {
		if !present[field] {
			missing = append(missing, field)
		}
	}
	return missing
}

func validateMissingGrounding(item Case, missing []string) error {
	if item.Label == "supported" {
		return requireAllGrounded(item, missing, "supported case")
	}
	switch item.FailureType {
	case FailureTypeAbsentItem:
		if len(item.GroundedFields) != 0 {
			return invalid("case %q absent_item must have empty grounded_fields", item.ID)
		}
		return nil
	case FailureTypePromptInjection:
		return requireAllGrounded(item, missing, "prompt_injection")
	default:
		return validateFailureGrounding(item, missing)
	}
}

func requireAllGrounded(item Case, missing []string, label string) error {
	if len(missing) != 0 {
		return invalid("case %q %s must ground every information-bearing field; missing %s", item.ID, label, strings.Join(missing, ", "))
	}
	return nil
}

func validateFailureGrounding(item Case, missing []string) error {
	failureField, ok := map[string]string{
		FailureTypeInventedContent: "content", FailureTypeWrongKind: "kind", FailureTypeWrongDate: "due",
		FailureTypeWrongPriority: "priority", FailureTypeWrongTag: "tags", FailureTypeWrongRoute: "project_alias",
	}[item.FailureType]
	if !ok {
		return invalid("case %q has no grounding rule for failure_type %q", item.ID, item.FailureType)
	}
	if err := validateFailureValue(item, failureField); err != nil {
		return err
	}
	if !sameFields(missing, []string{failureField}) {
		return invalid("case %q failure_type %s must leave only %s ungrounded; got %s", item.ID, item.FailureType, failureField, strings.Join(missing, ", "))
	}
	return nil
}

// validateFailureValue requires the value that the negative case contradicts.
func validateFailureValue(item Case, field string) error {
	requirements := map[string]struct {
		present bool
		message string
	}{
		"due":           {item.Candidate.Due != "", "wrong_date candidate due must be non-empty"},
		"priority":      {item.Candidate.Priority != 0, "wrong_priority candidate priority must be non-zero"},
		"tags":          {len(item.Candidate.Tags) != 0, "wrong_tag candidate tags must be non-empty"},
		"project_alias": {item.Candidate.ProjectAlias != "", "wrong_route candidate project_alias must be non-empty"},
	}
	requirement, exists := requirements[field]
	if exists && !requirement.present {
		return invalid("case %q %s", item.ID, requirement.message)
	}
	return nil
}

// validateCandidateShape keeps every candidate producible by the extraction path.
// A note carries no task fields, and the priority scale is the TickTick scale.
func validateCandidateShape(item Case) error {
	candidate := item.Candidate
	if err := validateCandidateKind(item); err != nil {
		return err
	}
	if candidate.AllDay && candidate.Due == "" {
		return invalid("case %q all_day candidate requires a due date", item.ID)
	}
	if !slices.Contains([]int{0, 1, 3, 5}, candidate.Priority) {
		return invalid("case %q candidate priority must be 0, 1, 3, or 5", item.ID)
	}
	if err := validateCandidateDue(item); err != nil {
		return err
	}
	if err := validateCandidateTags(item); err != nil {
		return err
	}
	return validateCandidateRoute(item)
}

func validateCandidateRoute(item Case) error {
	alias := item.Candidate.ProjectAlias
	if alias != "" && alias != strings.TrimSpace(alias) {
		return invalid("case %q candidate project_alias must not have surrounding spaces", item.ID)
	}
	return nil
}

func validateCandidateKind(item Case) error {
	switch item.Candidate.Kind {
	case "task":
		return nil
	case "note":
		if hasTaskFields(item.Candidate) {
			return invalid("case %q note candidate must not carry task fields", item.ID)
		}
		return nil
	default:
		return invalid("case %q candidate kind must be task or note", item.ID)
	}
}

func hasTaskFields(candidate CandidateItem) bool {
	if candidate.Due != "" || candidate.AllDay {
		return true
	}
	if candidate.Priority != 0 || len(candidate.Tags) != 0 {
		return true
	}
	return candidate.ProjectAlias != ""
}

func validateCandidateDue(item Case) error {
	if item.Candidate.Due == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", item.Candidate.Due); err != nil {
		return invalid("case %q candidate due must use the format 2006-01-02", item.ID)
	}
	return nil
}

func validateCandidateTags(item Case) error {
	trimmedTags := make(map[string]bool, len(item.Candidate.Tags))
	for _, tag := range item.Candidate.Tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			return invalid("case %q candidate tags must not contain empty values", item.ID)
		}
		if trimmedTags[trimmed] {
			return invalid("case %q candidate tags must not repeat value %q", item.ID, trimmed)
		}
		trimmedTags[trimmed] = true
	}
	return nil
}

func sameFields(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for _, value := range right {
		found := false
		for _, candidate := range left {
			if candidate == value {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func knownFailureType(value string) bool {
	for _, failureType := range FailureTypes {
		if value == failureType {
			return true
		}
	}
	return false
}

func validateText(item Case) error {
	values := []string{item.ID, item.Transcript, item.Candidate.Title, item.Candidate.Content, item.Candidate.ProjectAlias, item.ReasonCode}
	values = append(values, item.Candidate.Tags...)
	return validateTextValues(fmt.Sprintf("case %q", item.ID), values)
}

// validateTextValues rejects credentials and provider identifiers in any corpus text.
func validateTextValues(where string, values []string) error {
	for _, value := range values {
		if identifierPattern.MatchString(value) {
			return invalid("%s contains a 24-hex identifier", where)
		}
		if strings.Contains(strings.ToLower(value), "project-alias-") {
			return invalid("%s contains an internal project alias", where)
		}
		if bearerPattern.MatchString(value) {
			return invalid("%s contains a bearer credential", where)
		}
		if secretPattern.MatchString(value) {
			return invalid("%s contains an API credential", where)
		}
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidCorpus, fmt.Sprintf(format, args...))
}
