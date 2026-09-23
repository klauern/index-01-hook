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

	seen := make(map[string]bool, len(corpus.Cases))
	splits := map[string]int{}
	covered := make(map[string]bool, len(FailureTypes))
	for _, item := range corpus.Cases {
		if strings.TrimSpace(item.ID) == "" {
			return invalid("case id must not be empty")
		}
		if seen[item.ID] {
			return invalid("duplicate case id %q", item.ID)
		}
		seen[item.ID] = true
		if strings.TrimSpace(item.Transcript) == "" {
			return invalid("case %q transcript must not be empty", item.ID)
		}
		if item.Label != "supported" && item.Label != "unsupported" {
			return invalid("case %q label must be supported or unsupported", item.ID)
		}
		if item.Label == "unsupported" {
			if item.FailureType == "" {
				return invalid("case %q unsupported label requires failure_type", item.ID)
			}
			if !knownFailureType(item.FailureType) {
				return invalid("case %q has unknown failure_type %q", item.ID, item.FailureType)
			}
			if strings.TrimSpace(item.ReasonCode) == "" {
				return invalid("case %q unsupported label requires reason_code", item.ID)
			}
			covered[item.FailureType] = true
		} else if item.FailureType != "" || item.ReasonCode != "" {
			return invalid("case %q supported label cannot have failure_type or reason_code", item.ID)
		}
		if item.Split != "development" && item.Split != "heldout" {
			return invalid("case %q split must be development or heldout", item.ID)
		}
		splits[item.Split]++
		if item.Provenance != "synthetic" {
			return invalid("case %q provenance must be synthetic", item.ID)
		}
		if err := validateText(item); err != nil {
			return err
		}
		if err := validateGrounding(item); err != nil {
			return err
		}
	}
	if splits["development"] == 0 {
		return invalid("cases must include development cases")
	}
	if splits["heldout"] == 0 {
		return invalid("cases must include heldout cases")
	}
	missing := make([]string, 0)
	for _, failureType := range FailureTypes {
		if !covered[failureType] {
			missing = append(missing, failureType)
		}
	}
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
	grounded := make(map[string]bool, len(item.GroundedFields))
	for _, field := range item.GroundedFields {
		if field == "" {
			return invalid("case %q grounded_fields must not contain empty values", item.ID)
		}
		if !groundedFieldNames[field] {
			return invalid("case %q grounded_fields contains unsupported value %q", item.ID, field)
		}
		if grounded[field] {
			return invalid("case %q grounded_fields contains duplicate value %q", item.ID, field)
		}
		grounded[field] = true
	}

	informationBearing := informationBearingFields(item.Candidate)
	for field := range grounded {
		if !slices.Contains(informationBearing, field) {
			return invalid("case %q grounds field %q that the candidate does not carry", item.ID, field)
		}
	}
	missing := make([]string, 0, len(informationBearing))
	for _, field := range informationBearing {
		if !grounded[field] {
			missing = append(missing, field)
		}
	}

	if item.Label == "supported" {
		if len(missing) != 0 {
			return invalid("case %q supported case must ground every information-bearing field; missing %s", item.ID, strings.Join(missing, ", "))
		}
		return nil
	}

	switch item.FailureType {
	case FailureTypeAbsentItem:
		if len(item.GroundedFields) != 0 {
			return invalid("case %q absent_item must have empty grounded_fields", item.ID)
		}
	case FailureTypePromptInjection:
		if len(missing) != 0 {
			return invalid("case %q prompt_injection must ground every information-bearing field; missing %s", item.ID, strings.Join(missing, ", "))
		}
	default:
		failureField, ok := map[string]string{
			FailureTypeInventedContent: "content",
			FailureTypeWrongKind:       "kind",
			FailureTypeWrongDate:       "due",
			FailureTypeWrongPriority:   "priority",
			FailureTypeWrongTag:        "tags",
			FailureTypeWrongRoute:      "project_alias",
		}[item.FailureType]
		if !ok {
			return invalid("case %q has no grounding rule for failure_type %q", item.ID, item.FailureType)
		}
		switch failureField {
		case "due":
			if item.Candidate.Due == "" {
				return invalid("case %q wrong_date candidate due must be non-empty", item.ID)
			}
		case "priority":
			if item.Candidate.Priority == 0 {
				return invalid("case %q wrong_priority candidate priority must be non-zero", item.ID)
			}
		case "tags":
			if len(item.Candidate.Tags) == 0 {
				return invalid("case %q wrong_tag candidate tags must be non-empty", item.ID)
			}
		case "project_alias":
			if item.Candidate.ProjectAlias == "" {
				return invalid("case %q wrong_route candidate project_alias must be non-empty", item.ID)
			}
		}
		wantMissing := []string{failureField}
		if !sameFields(missing, wantMissing) {
			return invalid("case %q failure_type %s must leave only %s ungrounded; got %s", item.ID, item.FailureType, failureField, strings.Join(missing, ", "))
		}
	}
	return nil
}

// validateCandidateShape keeps every candidate producible by the extraction path.
// A note carries no task fields, and the priority scale is the TickTick scale.
func validateCandidateShape(item Case) error {
	candidate := item.Candidate
	switch candidate.Kind {
	case "task":
	case "note":
		if candidate.Due != "" || candidate.AllDay || candidate.Priority != 0 || len(candidate.Tags) != 0 || candidate.ProjectAlias != "" {
			return invalid("case %q note candidate must not carry task fields", item.ID)
		}
	default:
		return invalid("case %q candidate kind must be task or note", item.ID)
	}
	if candidate.AllDay && candidate.Due == "" {
		return invalid("case %q all_day candidate requires a due date", item.ID)
	}
	switch candidate.Priority {
	case 0, 1, 3, 5:
	default:
		return invalid("case %q candidate priority must be 0, 1, 3, or 5", item.ID)
	}
	if candidate.Due != "" {
		if _, err := time.Parse("2006-01-02", candidate.Due); err != nil {
			return invalid("case %q candidate due must use the format 2006-01-02", item.ID)
		}
	}
	trimmedTags := make(map[string]bool, len(candidate.Tags))
	for _, tag := range candidate.Tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			return invalid("case %q candidate tags must not contain empty values", item.ID)
		}
		if trimmedTags[trimmed] {
			return invalid("case %q candidate tags must not repeat value %q", item.ID, trimmed)
		}
		trimmedTags[trimmed] = true
	}
	if candidate.ProjectAlias != "" && candidate.ProjectAlias != strings.TrimSpace(candidate.ProjectAlias) {
		return invalid("case %q candidate project_alias must not have surrounding spaces", item.ID)
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
