package verificationcorpus

import (
	"fmt"
	"strings"
	"testing"
)

func TestFixtureCoherence(t *testing.T) {
	corpus, err := Load(fixturePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range corpus.Cases {
		for _, diagnostic := range fixtureCoherenceErrors(item) {
			t.Error(diagnostic)
		}
	}
}

func fixtureCoherenceErrors(item Case) []string {
	seen, diagnostics := fixtureGroundingErrors(item)
	missing := []string{}
	for _, field := range informationBearingFields(item.Candidate) {
		if !seen[field] {
			missing = append(missing, field)
		}
	}
	if diagnostic := fixtureMissingGroundingError(item, missing); diagnostic != "" {
		diagnostics = append(diagnostics, diagnostic)
	}
	return diagnostics
}

func fixtureGroundingErrors(item Case) (map[string]bool, []string) {
	seen := map[string]bool{}
	var diagnostics []string
	for _, field := range item.GroundedFields {
		if field == "" || !groundedFieldNames[field] {
			diagnostics = append(diagnostics, fmt.Sprintf("case %q has invalid grounded field %q", item.ID, field))
		}
		if seen[field] {
			diagnostics = append(diagnostics, fmt.Sprintf("case %q repeats grounded field %q", item.ID, field))
		}
		seen[field] = true
	}
	return seen, diagnostics
}

func fixtureMissingGroundingError(item Case, missing []string) string {
	if item.Label == "supported" && len(missing) != 0 {
		return fmt.Sprintf("case %q supported fields missing grounding: %s", item.ID, strings.Join(missing, ", "))
	}
	if item.FailureType == FailureTypeAbsentItem && len(item.GroundedFields) != 0 {
		return fmt.Sprintf("case %q absent item has grounded fields", item.ID)
	}
	if item.FailureType == FailureTypePromptInjection && len(missing) != 0 {
		return fmt.Sprintf("case %q injection fields missing grounding: %s", item.ID, strings.Join(missing, ", "))
	}
	return fixtureFailureFieldError(item, missing)
}

func fixtureFailureFieldError(item Case, missing []string) string {
	if item.Label != "unsupported" {
		return ""
	}
	if item.FailureType == FailureTypeAbsentItem || item.FailureType == FailureTypePromptInjection {
		return ""
	}
	want := map[string]string{FailureTypeInventedContent: "content", FailureTypeWrongKind: "kind", FailureTypeWrongDate: "due", FailureTypeWrongPriority: "priority", FailureTypeWrongTag: "tags", FailureTypeWrongRoute: "project_alias"}[item.FailureType]
	if !sameFields(missing, []string{want}) {
		return fmt.Sprintf("case %q leaves %v ungrounded, want [%s]", item.ID, missing, want)
	}
	return ""
}

// TestFixtureDiagnosticsRejectMutations proves the extracted checks reject damaged evidence.
func TestFixtureDiagnosticsRejectMutations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Case)
		want   string
	}{
		{"missing grounding", func(item *Case) { item.GroundedFields = nil }, "supported fields missing grounding"},
		{"duplicate grounding", func(item *Case) { item.GroundedFields = append(item.GroundedFields, "kind") }, "repeats grounded field"},
		{"unknown grounding", func(item *Case) { item.GroundedFields = append(item.GroundedFields, "unknown") }, "invalid grounded field"},
		{"absent grounding", func(item *Case) { item.Label = "unsupported"; item.FailureType = FailureTypeAbsentItem }, "absent item has grounded fields"},
		{"injection grounding", func(item *Case) {
			item.Label = "unsupported"
			item.FailureType = FailureTypePromptInjection
			item.GroundedFields = nil
		}, "injection fields missing grounding"},
		{"failure field", func(item *Case) { item.Label = "unsupported"; item.FailureType = FailureTypeWrongDate }, "want [due]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := validCorpus().Cases[0]
			test.mutate(&item)
			got := strings.Join(fixtureCoherenceErrors(item), "\n")
			if !strings.Contains(got, test.want) {
				t.Fatalf("diagnostics = %q, want %q", got, test.want)
			}
		})
	}
}
