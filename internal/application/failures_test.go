package application

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

type opaqueFailure struct{ info Failure }

// Error panics to detect accidental formatting of an adapter cause.
func (opaqueFailure) Error() string { panic("adapter cause must not be formatted") }

// ApplicationFailure returns safe metadata without a provider-specific dependency.
func (e opaqueFailure) ApplicationFailure() Failure { return e.info }

// Unwrap preserves an opaque cause without calling Error.
func (w failureEnvelope) Unwrap() error { return w.cause }

// Error returns the fixed wrapper diagnostic.
func (failureEnvelope) Error() string { return "wrapped failure" }

type failureEnvelope struct{ cause error }

// TestFailurePortPreservesMetadata checks every category, wrapped lookup and value isolation.
func TestFailurePortPreservesMetadata(t *testing.T) {
	kinds := []FailureKind{Authentication, Configuration, Retryable, Malformed, ReviewRequired, AmbiguousWrite, Terminal}
	for _, kind := range kinds {
 if !kind.Valid(){t.Fatalf("known category %q rejected",kind)}
		want := Failure{Kind: kind, Operation: "extract items", StatusCode: 429, Reason: "provider_http_error"}
		original := opaqueFailure{info: want}
		for _, err := range []error{want, original, failureEnvelope{original}, errors.Join(errors.New("unrelated"), failureEnvelope{original})} {
			got, ok := FailureOf(err)
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("kind %q metadata=%+v found=%v", kind, got, ok)
			}
			got.Operation = "changed"
			if original.info.Operation != want.Operation {
				t.Fatal("metadata aliases adapter state")
			}
		}
	}
	for _, err := range []error{nil, errors.New("private transport body"), fmt.Errorf("context: %w", errors.New("private body"))} {
		if got, ok := FailureOf(err); ok || got != (Failure{}) {
			t.Fatal("unknown error became typed failure")
		}
	}
}

// TestFailureDiagnosticDoesNotFormatMetadata protects the generic error boundary from supplied text.
func TestFailureDiagnosticDoesNotFormatMetadata(t *testing.T) {
	if FailureKind("private-kind").Valid(){t.Fatal("unknown category accepted")}
 f := Failure{Kind: "private-kind", Operation: "private-operation", StatusCode: 401, Reason: "private-body"}
	if got := f.Error(); got != "application operation failed" {
		t.Fatalf("unsafe diagnostic %q", got)
	}
	a, _ := FailureOf(errors.Join(Failure{Kind: AmbiguousWrite}, Failure{Kind: Retryable}))
	if a.Kind != AmbiguousWrite {
		t.Fatal("joined ambiguity lost its first failure")
	}
	if AmbiguousWrite == Retryable || ReviewRequired == Retryable {
		t.Fatal("unsafe retry category collapse")
	}
}
