package verificationcorpus

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func fixturePath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "typesafe-verification", "corpus.json")
}

func TestShippedFixture(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Split("development")) == 0 || len(corpus.Split("heldout")) == 0 {
		t.Fatal("fixture must contain both splits")
	}
	assertFixtureFailureCoverage(t, corpus)
	assertFixtureCaseIdentity(t, corpus)
}

func assertFixtureFailureCoverage(t *testing.T, corpus Corpus) {
	t.Helper()
	counts := corpus.Counts()
	for _, failureType := range FailureTypes {
		if counts[failureType] == 0 {
			t.Errorf("failure type %q is not covered", failureType)
		}
	}
}

func assertFixtureCaseIdentity(t *testing.T, corpus Corpus) {
	t.Helper()
	seen := map[string]bool{}
	for _, item := range corpus.Cases {
		if seen[item.ID] {
			t.Errorf("duplicate case id %q", item.ID)
		}
		seen[item.ID] = true
		if item.Label == "unsupported" && item.ReasonCode == "" {
			t.Errorf("negative case %q has no reason code", item.ID)
		}
	}
}

func TestShippedFixtureHasNoIdentifier(t *testing.T) {
	data, err := os.ReadFile(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`\b[0-9a-f]{24}\b`).Find(data) != nil {
		t.Fatal("fixture contains a 24-hex identifier")
	}
}

func TestFixtureSplitsDisjoint(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	transcripts := map[string]string{}
	pairs := map[string]string{}
	for _, item := range corpus.Cases {
		if ids[item.ID] {
			t.Errorf("duplicate id %q", item.ID)
		}
		ids[item.ID] = true
		key := item.Transcript + "\x00" + item.Candidate.Title
		if prior, ok := transcripts[item.Transcript]; ok && prior != item.Split {
			t.Errorf("transcript shared across splits: %q", item.Transcript)
		}
		transcripts[item.Transcript] = item.Split
		if prior, ok := pairs[key]; ok && prior != item.Split {
			t.Errorf("transcript and title pair shared across splits: %q", key)
		}
		pairs[key] = item.Split
	}
}

// TestFixtureCoveragePerSplit keeps each split large enough for calibration.
func TestFixtureCoveragePerSplit(t *testing.T) {
	checkFixtureSize(t, fixtureSizeCheck{func(n int) bool { return n >= 72 }, "at least 72", assertFixtureCoveragePerSplit})
}

func assertFixtureCoveragePerSplit(t *testing.T, corpus Corpus, split string) {
	t.Helper()
	const minSupported = 12
	const minPerFailure = 2
	supported, failures := fixtureLabelCounts(corpus.Split(split))
	if supported < minSupported {
		t.Errorf("split %s has %d supported cases, want at least %d", split, supported, minSupported)
	}
	for _, failureType := range FailureTypes {
		if failures[failureType] < minPerFailure {
			t.Errorf("split %s has %d %s cases, want at least %d", split, failures[failureType], failureType, minPerFailure)
		}
	}
}

// TestFixtureExactCardinality pins the corpus size so a silent shrink fails the build.
func TestFixtureExactCardinality(t *testing.T) {
	checkFixtureSize(t, fixtureSizeCheck{func(n int) bool { return n == 86 }, "86", assertFixtureExactCardinality})
}

func assertFixtureExactCardinality(t *testing.T, corpus Corpus, split string) {
	t.Helper()
	const wantPerSplit = 43
	const wantSupportedPerSplit = 19
	const wantPerFailure = 3
	items := corpus.Split(split)
	if len(items) != wantPerSplit {
		t.Errorf("split %s has %d cases, want %d", split, len(items), wantPerSplit)
	}
	supported, failures := fixtureLabelCounts(items)
	if supported != wantSupportedPerSplit {
		t.Errorf("split %s has %d supported cases, want %d", split, supported, wantSupportedPerSplit)
	}
	for _, failureType := range FailureTypes {
		if failures[failureType] != wantPerFailure {
			t.Errorf("split %s has %d %s cases, want %d", split, failures[failureType], failureType, wantPerFailure)
		}
	}
}

// fixtureLabelCounts keeps the same supported and failure accounting in size checks.
func fixtureLabelCounts(items []Case) (int, map[string]int) {
	supported := 0
	failures := map[string]int{}
	for _, item := range items {
		if item.Label == "supported" {
			supported++
			continue
		}
		failures[item.FailureType]++
	}
	return supported, failures
}

// loadFixture reports file and contract failures at the calling assertion.
func loadFixture(t *testing.T) Corpus {
	t.Helper()
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	return corpus
}

// fixtureSizeCheck describes one independent corpus size gate.
type fixtureSizeCheck struct {
	accepts func(int) bool
	want    string
	split   func(*testing.T, Corpus, string)
}

func checkFixtureSize(t *testing.T, check fixtureSizeCheck) {
	t.Helper()
	corpus := loadFixture(t)
	if !check.accepts(len(corpus.Cases)) {
		t.Errorf("corpus has %d cases, want %s", len(corpus.Cases), check.want)
	}
	for _, split := range []string{"development", "heldout"} {
		check.split(t, corpus, split)
	}
}
