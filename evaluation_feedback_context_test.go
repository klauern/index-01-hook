package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

func TestEvaluationFeedbackRecordingContext(t *testing.T) {
	corpus, _ := loadFeedbackFixture(t)
	corpus.Examples = corpus.Examples[:2]
	corpus.Routing = nil
	for i := range corpus.Examples {
		e := &corpus.Examples[i]
		alias, project := fmt.Sprintf("alias%d", i), fmt.Sprintf("project%d", i)
		e.Routing = &evalcorpus.RoutingConfig{
			Clock: fmt.Sprintf("2026-09-%02dT02:00:00Z", i+8), TimeZone: "America/Chicago",
			Aliases: map[string]string{alias: project}, DefaultProjectID: "default", NoteProjectID: "notes",
		}
		e.Targets[0].Label.ProjectID = project
		e.SavedOutput = json.RawMessage(fmt.Sprintf(`{"items":[{"title":%q,"content":"","kind":"task","project_alias":%q,"due_at":null,"all_day":false,"priority":0,"tags":[]}]}`, e.Targets[0].Title, alias))
	}
	data, err := json.Marshal(corpus)
	if err != nil || evalcorpus.Validate(corpus) != nil {
		t.Fatal("invalid test corpus", err)
	}
	t.Run("replay resolves each recorded destination", func(t *testing.T) {
		report, err := runFeedbackCorpus(corpus, data, "replay", nil)
		if err != nil || report.Counts["pass"] != 2 {
			t.Fatal("recording replay did not use each context", err)
		}
	})
	t.Run("live request uses each recorded clock and aliases", func(t *testing.T) {
		t.Setenv("INDEX01_RUN_LIVE_CORPUS_EVALUATION", "1")
		t.Setenv("INDEX01_EVAL_TRIALS", "1")
		t.Setenv("INDEX01_EVAL_CALL_BUDGET", "2")
		t.Setenv("INDEX01_DEEPSEEK_TOKEN", "fixture")
		requests := 0
		transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			i := requests
			requests++
			if i >= len(corpus.Examples) {
				t.Fatal("unexpected model request")
			}
			e := corpus.Examples[i]
			var request deepSeekRequest
			if json.NewDecoder(req.Body).Decode(&request) != nil || len(request.Input) != 2 {
				t.Fatal("invalid extraction request")
			}
			clock, _ := time.Parse(time.RFC3339Nano, e.Routing.Clock)
			zone, _ := time.LoadLocation(e.Routing.TimeZone)
			want := deepSeekSystemPrompt(clock.In(zone), e.Routing.TimeZone, []string{fmt.Sprintf("alias%d", i)})
			if request.Input[0].Content != want || request.Input[1].Content != e.Input.Text || strings.Contains(want, fmt.Sprintf("alias%d", 1-i)) {
				t.Fatal("request context came from another recording")
			}
			return deepSeekFixtureOutput(fmt.Sprintf("recording-%d", i), string(e.SavedOutput)), nil
		})
		report, err := runFeedbackCorpus(corpus, data, "live", transport)
		if err != nil || requests != 2 || report.Counts["pass"] != 2 || report.GenerationCalls != 2 {
			t.Fatal("recording contexts failed bounded live simulation", err)
		}
	})
}
