// Package verificationcorpus defines the TypeSafe verification calibration corpus.
package verificationcorpus

const (
	FormatVersion = 1
	CorpusType    = "typesafe_verification_corpus"

	FailureTypeInventedContent = "invented_content"
	FailureTypeWrongDate       = "wrong_date"
	FailureTypeWrongRoute      = "wrong_route"
	FailureTypeWrongKind       = "wrong_kind"
	FailureTypeWrongPriority   = "wrong_priority"
	FailureTypeWrongTag        = "wrong_tag"
	FailureTypeAbsentItem      = "absent_item"
	FailureTypePromptInjection = "prompt_injection"

	FailureInventedContent = FailureTypeInventedContent
	FailureWrongDate       = FailureTypeWrongDate
	FailureWrongRoute      = FailureTypeWrongRoute
	FailureWrongKind       = FailureTypeWrongKind
	FailureWrongPriority   = FailureTypeWrongPriority
	FailureWrongTag        = FailureTypeWrongTag
	FailureAbsentItem      = FailureTypeAbsentItem
	FailurePromptInjection = FailureTypePromptInjection
)

// FailureTypes lists all supported negative labels.
var FailureTypes = []string{
	FailureTypeInventedContent,
	FailureTypeWrongDate,
	FailureTypeWrongRoute,
	FailureTypeWrongKind,
	FailureTypeWrongPriority,
	FailureTypeWrongTag,
	FailureTypeAbsentItem,
	FailureTypePromptInjection,
}

type Corpus struct {
	FormatVersion int    `json:"format_version"`
	Type          string `json:"type"`
	Source        Source `json:"source"`
	Cases         []Case `json:"cases"`
}

type Source struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	GeneratedAt string `json:"generated_at"`
}

type Case struct {
	ID             string        `json:"id"`
	Split          string        `json:"split"`
	Label          string        `json:"label"`
	FailureType    string        `json:"failure_type"`
	ReasonCode     string        `json:"reason_code"`
	Transcript     string        `json:"transcript"`
	Candidate      CandidateItem `json:"candidate"`
	GroundedFields []string      `json:"grounded_fields"`
	Provenance     string        `json:"provenance"`
}

type CandidateItem struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	Due          string   `json:"due"`
	AllDay       bool     `json:"all_day"`
	Priority     int      `json:"priority"`
	Tags         []string `json:"tags"`
	ProjectAlias string   `json:"project_alias"`
}

// Split returns cases in the requested split.
func (c Corpus) Split(name string) []Case {
	result := make([]Case, 0)
	for _, item := range c.Cases {
		if item.Split == name {
			result = append(result, item)
		}
	}
	return result
}

// Counts returns counts by split, label, and failure type.
func (c Corpus) Counts() map[string]int {
	counts := make(map[string]int)
	for _, item := range c.Cases {
		counts[item.Split]++
		counts[item.Label]++
		if item.FailureType != "" {
			counts[item.FailureType]++
		}
	}
	return counts
}

// Expected routing outcomes that the calibration gate compares against.
const (
	ExpectedAccept = "accept"
	ExpectedReview = "review"
	ExpectedReject = "reject"
)

// ExpectedDecision reports the routing that the label and failure type require.
// An injected item must reach review and must never be accepted.
func ExpectedDecision(item Case) string {
	if item.Label == "supported" {
		return ExpectedAccept
	}
	if item.FailureType == FailureTypePromptInjection {
		return ExpectedReview
	}
	return ExpectedReject
}
