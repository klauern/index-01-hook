package main

import (
	"encoding/json"
	"testing"
)

func TestEvaluationFeedbackExplicitRouteCannotPassByDefault(t *testing.T) {
	for _, alias := range []string{"", "work"} {
		t.Run("alias="+alias, func(t *testing.T) {
			corpus, _ := loadFeedbackFixture(t)
			corpus.Examples = corpus.Examples[:1]
			if corpus.Routing.DefaultProjectID == corpus.Examples[0].Targets[0].Label.ProjectID {
				t.Fatal("explicit route equals fallback and cannot test alias selection")
			}
			var output struct {
				Items []map[string]any `json:"items"`
			}
			if err := json.Unmarshal(corpus.Examples[0].SavedOutput, &output); err != nil {
				t.Fatal(err)
			}
			output.Items[0]["project_alias"] = alias
			if alias == "" {
				output.Items[0]["project_alias"] = nil
			}
			corpus.Examples[0].SavedOutput, _ = json.Marshal(output)
			data, _ := json.Marshal(corpus)
			report, err := runFeedbackCorpus(corpus, data, "replay", nil)
			if err == nil || report.Counts["fail"] != 1 || report.Counts["pass"] != 0 {
				t.Fatalf("wrong explicit route passed: counts=%v err=%v", report.Counts, err)
			}
		})
	}
}
