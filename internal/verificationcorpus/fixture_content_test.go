package verificationcorpus

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestFixtureNoHintPhrases(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`does not mention`),
		regexp.MustCompile(`unrelated work`),
		regexp.MustCompile(`this message`),
		regexp.MustCompile(`test case`),
		regexp.MustCompile(`ground truth`),
		regexp.MustCompile(`\bexpected\b`),
		regexp.MustCompile(`\bcorpus\b`),
		regexp.MustCompile(`\blabel\b`),
		regexp.MustCompile(`\bcandidate\b`),
	}
	for _, item := range corpus.Cases {
		lower := strings.ToLower(item.Transcript)
		for _, phrase := range forbidden {
			if phrase.MatchString(lower) {
				t.Errorf("case %q transcript contains forbidden phrase %q", item.ID, phrase)
			}
		}
	}
}

// normalizeTranscript removes part-of-day words and punctuation.
// A word-swapped copy of a development case must not pass as new evidence.
var splitNoiseWords = []string{"morning", "afternoon", "evening", "night", "today", "tomorrow", "weekend"}

func normalizeTranscript(text string) string {
	lowered := strings.ToLower(text)
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case asciiWordRune(r):
			return r
		default:
			return ' '
		}
	}, lowered)
	kept := make([]string, 0, 16)
	for _, word := range strings.Fields(cleaned) {
		if slices.Contains(splitNoiseWords, word) {
			continue
		}
		kept = append(kept, word)
	}
	return strings.Join(kept, " ")
}

// asciiWordRune keeps ASCII letters and digits in normalized fixture text.
func asciiWordRune(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	return r >= '0' && r <= '9'
}

// TestFixtureItemsAreDistinct keeps every case an independent observation.
// A transcript may carry a derived negative, so the pair must differ and the
// number of cases per transcript stays small.
func TestFixtureItemsAreDistinct(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	const maxCasesPerTranscript = 10
	pairs := map[string]string{}
	perTranscript := map[string]int{}
	titleTranscript := map[string]string{}
	for _, item := range corpus.Cases {
		normalized := normalizeTranscript(item.Transcript)
		perTranscript[normalized]++
		if perTranscript[normalized] > maxCasesPerTranscript {
			t.Errorf("transcript of %s is used by more than %d cases", item.ID, maxCasesPerTranscript)
		}
		pair := normalized + "\x00" + candidateKey(item.Candidate)
		if prior, ok := pairs[pair]; ok {
			t.Errorf("cases %s and %s share a transcript and a candidate", prior, item.ID)
		}
		pairs[pair] = item.ID
		title := strings.ToLower(item.Candidate.Title)
		if prior, ok := titleTranscript[title]; ok && prior != normalized {
			t.Errorf("title %q appears with different transcripts in %s and %s", item.Candidate.Title, prior, item.ID)
		}
		titleTranscript[title] = normalized
	}
}

// candidateKey renders every candidate field so two cases can be compared.
func candidateKey(candidate CandidateItem) string {
	return strings.Join([]string{
		candidate.Kind, candidate.Title, candidate.Content, candidate.Due,
		fmt.Sprint(candidate.AllDay), fmt.Sprint(candidate.Priority),
		strings.Join(candidate.Tags, ","), candidate.ProjectAlias,
	}, "\x00")
}

// TestFixtureAvoidsRepeatedBoilerplate rejects a story frame reused too often.
// Hand-written speech still shares function words, so the limit is a share.
func TestFixtureAvoidsRepeatedBoilerplate(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	const maxOpeningShare = 0.05
	prefixes, first := fixtureOpenings(corpus.Cases)
	limit := int(float64(len(corpus.Cases)) * maxOpeningShare)
	if limit < 3 {
		limit = 3
	}
	for prefix, count := range prefixes {
		if count > limit {
			t.Errorf("%d cases start with %q, first at %s; allow %d or fewer", count, prefix, first[prefix], limit)
		}
	}
}

func fixtureOpenings(items []Case) (map[string]int, map[string]string) {
	prefixes := map[string]int{}
	first := map[string]string{}
	for _, item := range items {
		words := strings.Fields(normalizeTranscript(item.Transcript))
		if len(words) < 3 {
			continue
		}
		prefix := strings.Join(words[:3], " ")
		prefixes[prefix]++
		if _, ok := first[prefix]; !ok {
			first[prefix] = item.ID
		}
	}
	return prefixes, first
}

// contentWords drops function words so shared frames do not hide shared content.
func contentWords(text string) []string {
	kept := make([]string, 0, 32)
	for _, word := range strings.Fields(normalizeTranscript(text)) {
		if contentStopWords[word] {
			continue
		}
		kept = append(kept, word)
	}
	return kept
}

var contentStopWords = map[string]bool{
	"a": true, "about": true, "after": true, "all": true, "an": true, "and": true,
	"any": true, "are": true, "as": true, "at": true, "be": true, "before": true,
	"but": true, "by": true, "can": true, "could": true, "do": true, "for": true,
	"from": true, "get": true, "had": true, "has": true, "have": true, "i": true,
	"if": true, "in": true, "into": true, "is": true, "it": true, "its": true,
	"just": true, "like": true, "make": true, "me": true, "my": true, "need": true,
	"not": true, "of": true, "on": true, "or": true, "our": true, "out": true,
	"please": true, "put": true, "should": true, "so": true, "some": true, "that": true,
	"the": true, "their": true, "them": true, "then": true, "there": true, "this": true,
	"to": true, "up": true, "us": true, "was": true, "we": true, "when": true,
	"will": true, "with": true, "would": true, "you": true, "your": true,
}

// TestFixtureContentIsNotRepeated rejects generated text that reuses content.
// A repeated content bigram inside one transcript means composed text, not speech.
func TestFixtureContentIsNotRepeated(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	phrases := map[string]int{}
	first := map[string]string{}
	for _, item := range corpus.Cases {
		words := contentWords(item.Transcript)
		for _, bigram := range repeatedBigrams(words) {
			t.Errorf("case %s repeats the content phrase %q", item.ID, bigram)
		}
		recordContentPhrases(words, item.ID, phrases, first)
	}
	assertContentPhraseCounts(t, phrases, first)
}

func assertContentPhraseCounts(t *testing.T, phrases map[string]int, first map[string]string) {
	t.Helper()
	const maxSharedContentPhrase = 5
	for phrase, count := range phrases {
		if count > maxSharedContentPhrase {
			t.Errorf("%d cases share the content phrase %q, first at %s; allow %d or fewer", count, phrase, first[phrase], maxSharedContentPhrase)
		}
	}
}

func repeatedBigrams(words []string) []string {
	seen := map[string]bool{}
	var repeated []string
	for index := 0; index+1 < len(words); index++ {
		bigram := words[index] + " " + words[index+1]
		if seen[bigram] {
			repeated = append(repeated, bigram)
		}
		seen[bigram] = true
	}
	return repeated
}

func recordContentPhrases(words []string, id string, phrases map[string]int, first map[string]string) {
	for index := 0; index+3 < len(words); index++ {
		phrase := strings.Join(words[index:index+4], " ")
		phrases[phrase]++
		if _, ok := first[phrase]; !ok {
			first[phrase] = id
		}
	}
}

// TestFixtureIdentifiersAreOpaque rejects ids that name the split, label, or failure type.
func TestFixtureIdentifiersAreOpaque(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	hints := append([]string{"support", "negative", "unsupported", "development", "heldout"}, FailureTypes...)
	for _, item := range corpus.Cases {
		lower := strings.ToLower(item.ID)
		for _, hint := range hints {
			if strings.Contains(lower, hint) {
				t.Errorf("case id %q names %q", item.ID, hint)
			}
		}
	}
}

// TestFixtureContentIsParaphrased rejects candidate content copied from the transcript.
func TestFixtureContentIsParaphrased(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range corpus.Cases {
		content := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(item.Candidate.Content), "."))
		transcript := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(item.Transcript), "."))
		if content != "" && content == transcript {
			t.Errorf("case %s candidate content repeats the transcript", item.ID)
		}
	}
}

// TestFixtureTextHelpersKeepRepeatedEvidence pins duplicate and first-occurrence accounting.
func TestFixtureTextHelpersKeepRepeatedEvidence(t *testing.T) {
	words := []string{"alpha", "beta", "alpha", "beta", "alpha", "beta"}
	if got := repeatedBigrams(words); !slices.Equal(got, []string{"alpha beta", "beta alpha", "alpha beta"}) {
		t.Fatalf("repeated bigrams = %v", got)
	}
	phrases, first := map[string]int{}, map[string]string{}
	recordContentPhrases(words, "first", phrases, first)
	recordContentPhrases(words, "second", phrases, first)
	if phrases["alpha beta alpha beta"] != 4 {
		t.Fatalf("phrase count = %v", phrases)
	}
	if first["alpha beta alpha beta"] != "first" {
		t.Fatalf("first occurrence = %v", first)
	}
}
