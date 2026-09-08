package main

import (
	"slices"
	"testing"
)

func TestEvaluationFeedbackReplayRequiresEvaluatedExamples(t *testing.T) {
	for _, name := range []string{"all examples blocked", "all saved outputs missing"} {
		t.Run(name, func(t *testing.T) {
			corpus, data := loadFeedbackFixture(t)
			for i := range corpus.Examples {
				if name == "all examples blocked" {
					corpus.Examples[i].Blockers = []string{"missing_original_transcript"}
				} else {
					corpus.Examples[i].SavedOutput = nil
				}
			}

			report, err := runFeedbackCorpus(corpus, data, "replay", nil)
			if err == nil || report.Counts["unsupported"] != len(corpus.Examples) || report.Counts["pass"] != 0 || report.GenerationCalls != 0 {
				t.Fatal("replay without evaluated examples must fail without model calls")
			}
			if !slices.Contains(report.Diagnostics, "replay evaluated no examples") {
				t.Fatal("replay must explain why no evaluation result is available")
			}

			report, err = runFeedbackCorpus(corpus, data, "audit", nil)
			if err != nil || report.Counts["pass"] != 0 || report.GenerationCalls != 0 {
				t.Fatal("audit must succeed without evaluated examples or model calls")
			}
			if report.Counts["unsupported"]+report.Counts["skip"] != len(corpus.Examples) {
				t.Fatal("audit must account for each unevaluated example")
			}
		})
	}
}
