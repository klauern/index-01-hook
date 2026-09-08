package evalcorpus

// Coverage counts recording groups once, independently of evaluation trials.
// Blocker counts overlap because a recording can lack several kinds of evidence.
type Coverage struct {
	Recordings      int                      `json:"recordings"`
	Targets         int                      `json:"targets"`
	Ready           int                      `json:"ready"`
	Blocked         int                      `json:"blocked"`
	ReplayReady     int                      `json:"replay_ready"`
	InputProvenance map[string]int           `json:"input_provenance"`
	LabelStatus     map[string]int           `json:"target_label_status"`
	Blockers        map[string]int           `json:"blockers"`
	Splits          map[string]SplitCoverage `json:"splits"`
}

type SplitCoverage struct {
	Recordings  int `json:"recordings"`
	Ready       int `json:"ready"`
	Blocked     int `json:"blocked"`
	ReplayReady int `json:"replay_ready"`
}

// SummarizeCoverage describes a validated corpus. Readiness does not imply accuracy.
// Replay readiness means saved output exists; the runner must still evaluate it.
func SummarizeCoverage(c Corpus) Coverage {
	summary := Coverage{
		InputProvenance: map[string]int{}, LabelStatus: map[string]int{}, Blockers: map[string]int{},
		Splits: map[string]SplitCoverage{"development": {}, "held_out": {}},
	}
	for _, example := range c.Examples {
		summary.Recordings++
		summary.Targets += len(example.Targets)
		summary.InputProvenance[example.Input.Provenance]++
		for _, target := range example.Targets {
			summary.LabelStatus[target.Label.Status]++
		}
		split := summary.Splits[example.Split]
		split.Recordings++
		blockers := Eligibility(c, example)
		if len(blockers) == 0 {
			summary.Ready++
			split.Ready++
			if len(example.SavedOutput) > 0 {
				summary.ReplayReady++
				split.ReplayReady++
			}
		} else {
			summary.Blocked++
			split.Blocked++
			for _, blocker := range blockers {
				summary.Blockers[blocker]++
			}
		}
		summary.Splits[example.Split] = split
	}
	return summary
}
