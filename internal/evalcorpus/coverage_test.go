package evalcorpus

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoverageSeparatesReadinessAndEvidence(t *testing.T) {
	first := approvedCandidate(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0), "home")
	second := approvedCandidate(fixtureCandidate("two", "work", strings.Repeat("b", 64), 0), "work")
	c := mustImport(t, fixtureSnapshot(first, second))
	c.Routing = routing()
	c.Examples[0].Split = "development"
	c.Examples[0].Input = Input{Text: "Repair the gate at home.", Provenance: "original"}
	c.Examples[0].SavedOutput = json.RawMessage(`{"items":[]}`)
	c.Examples[1].Split = "held_out"
	c.Examples[1].Input.Text = ""
	c.Examples[1].Input.Provenance = "missing"
	c.Examples[1].Targets[0].Label.Status = "blocked"
	c.Examples[1].Targets[0].Label.ProjectID = ""
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	coverage := SummarizeCoverage(c)
	requireEqual(t, coverage.Recordings, 2)
	requireEqual(t, coverage.Targets, 2)
	requireEqual(t, coverage.Ready, 1)
	requireEqual(t, coverage.Blocked, 1)
	requireEqual(t, coverage.ReplayReady, 1)
	requireEqual(t, coverage.Splits["development"].Ready, 1)
	requireEqual(t, coverage.Splits["held_out"].Ready, 0)
	requireEqual(t, coverage.Splits["held_out"].Blocked, 1)
	requireEqual(t, coverage.InputProvenance["original"], 1)
	requireEqual(t, coverage.InputProvenance["missing"], 1)
	requireEqual(t, coverage.LabelStatus["blocked"], 1)
	if len(coverage.Blockers) < 2 {
		t.Fatal("overlapping evidence gaps were hidden")
	}
	c.Examples[0].SavedOutput = nil
	if got := SummarizeCoverage(c); got.Ready != 1 || got.ReplayReady != 0 {
		t.Fatal("input readiness depends on saved predictions")
	}
}
