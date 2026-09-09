// Package evalcorpus defines private routing evaluation data and its provenance.
package evalcorpus

import "encoding/json"

const Version = 1
const CorpusType = "routing_evaluation_corpus"

type Corpus struct {
	FormatVersion int            `json:"format_version"`
	Type          string         `json:"type"`
	Source        Source         `json:"source"`
	Routing       *RoutingConfig `json:"routing,omitempty"`
	Examples      []Example      `json:"examples"`
}

type Source struct {
	Kind        string `json:"kind"`
	SHA256      string `json:"sha256"`
	CollectedAt string `json:"collected_at"`
}

// RoutingConfig selects the clock and destinations used for evaluation.
// Each example can preserve its recorded configuration or inherit the corpus configuration.
type RoutingConfig struct {
	Clock            string            `json:"clock"`
	TimeZone         string            `json:"time_zone"`
	Aliases          map[string]string `json:"aliases"`
	DefaultProjectID string            `json:"default_project_id"`
	NoteProjectID    string            `json:"note_project_id"`
}

type Example struct {
	ID                   string          `json:"id"`
	RecordingFingerprint string          `json:"recording_fingerprint"`
	ExpectedItemCount    *int            `json:"expected_item_count"`
	Split                string          `json:"split"`
	Input                Input           `json:"input"`
	Routing              *RoutingConfig  `json:"routing,omitempty"`
	Targets              []Target        `json:"targets"`
	Blockers             []string        `json:"blockers"`
	SavedOutput          json.RawMessage `json:"saved_output,omitempty"`
}

type Input struct {
	Text         string `json:"text"`
	Provenance   string `json:"provenance"`
	PromptSHA256 string `json:"prompt_sha256,omitempty"`
	Model        string `json:"model,omitempty"`
}

type Target struct {
	TaskID            string `json:"task_id"`
	ItemIndex         int    `json:"item_index"`
	Title             string `json:"title"`
	Kind              string `json:"kind"`
	ObservedProjectID string `json:"observed_project_id"`
	Label             Label  `json:"label"`
}

type Label struct {
	ProjectID string `json:"project_id"`
	Status    string `json:"status"`
	Basis     string `json:"basis"`
	EventID   string `json:"event_id,omitempty"`
}
