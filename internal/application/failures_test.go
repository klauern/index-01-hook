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
		if !kind.Valid() {
			t.Fatalf("known category %q rejected", kind)
		}
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
	if FailureKind("private-kind").Valid() {
		t.Fatal("unknown category accepted")
	}
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

// TestFailureOfJoinedAmbiguity preserves ambiguous metadata in either order and through nested wrappers.
func TestFailureOfJoinedAmbiguity(t *testing.T) {
	ambiguous := Failure{Kind: AmbiguousWrite, Operation: "create task", StatusCode: 504, Reason: "provider_transport"}
	retryable := Failure{Kind: Retryable, Operation: "retry request", StatusCode: 429, Reason: "provider_http_error"}
	for _, tt := range []struct {
		name string
		err  error
	}{
		{"retryable_first", errors.Join(retryable, ambiguous)},
		{"ambiguous_first", errors.Join(ambiguous, retryable)},
		{"wrapped_join", failureEnvelope{errors.Join(retryable, ambiguous)}},
		{"nested_join", errors.Join(retryable, errors.Join(errors.New("unrelated"), failureEnvelope{ambiguous}))},
		{"outer_failure_source", failureWithCause{Failure: retryable, cause: failureEnvelope{ambiguous}}},
		{"opaque_sources", errors.Join(opaqueFailure{retryable}, failureEnvelope{opaqueFailure{ambiguous}})},
		{"custom_as", errors.Join(retryable, failureAlias{ambiguous})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := FailureOf(tt.err)
			if !ok || got != ambiguous {
				t.Fatalf("metadata=%+v found=%v; want ambiguous metadata %+v", got, ok, ambiguous)
			}
		})
	}
	got, ok := FailureOf(errors.Join(retryable, Failure{Kind: Terminal}))
	if !ok || got != retryable {
		t.Fatalf("non-ambiguous first-source behavior changed: %+v found=%v", got, ok)
	}
}

type failureWithCause struct {
	Failure
	cause error
}

// Unwrap exposes a nested failure even when this wrapper supplies its own metadata.
func (f failureWithCause) Unwrap() error { return f.cause }

type failureAlias struct{ info Failure }

// Error detects accidental formatting during custom matching.
func (failureAlias) Error() string { panic("failure alias must not be formatted") }

// As exposes failure metadata through the standard custom matching hook.
func (f failureAlias) As(target any) bool {
	if source, ok := target.(*FailureSource); ok {
		*source = opaqueFailure{f.info}
		return true
	}
	return false
}
