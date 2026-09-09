package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

// ExtractionEvidence retains the actual successful model request and output.
// It contains private input context and must not appear in logs or HTTP responses.
type ExtractionEvidence struct {
	Clock        time.Time                 `json:"clock"`
	TimeZone     string                    `json:"time_zone"`
	SystemPrompt string                    `json:"system_prompt"`
	PromptSHA256 string                    `json:"prompt_sha256"`
	Schema       json.RawMessage           `json:"schema"`
	SchemaSHA256 string                    `json:"schema_sha256"`
	SavedOutput  json.RawMessage           `json:"saved_output"`
	BuildCommit  string                    `json:"build_commit"`
	Routing      *evalcorpus.RoutingConfig `json:"routing,omitempty"`
}

func evidenceHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
