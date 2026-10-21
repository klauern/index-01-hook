package application

import "errors"

// FailureKind identifies a workflow failure independently of its provider.
type FailureKind string

const (
	// Authentication requires an operator to restore provider access.
	Authentication FailureKind = "authentication"
	// Configuration requires an operator to correct routing or adapter configuration.
	Configuration FailureKind = "configuration"
	// Retryable permits a bounded retry when no remote write is ambiguous.
	Retryable FailureKind = "retryable"
	// Malformed rejects an invalid request, response or application value.
	Malformed FailureKind = "malformed"
	// ReviewRequired requires review instead of automatic delivery.
	ReviewRequired FailureKind = "review"
	// AmbiguousWrite requires reconciliation before another remote create.
	AmbiguousWrite FailureKind = "ambiguous"
	// Terminal rejects an operation that automatic retry cannot repair.
	Terminal FailureKind = "terminal"
)

// Failure contains only classification and safe operation metadata.
// Adapters must use static operation and reason codes. Do not copy bodies, credentials or transport errors.
// StatusCode is optional numeric metadata. No cause or provider payload is retained.
type Failure struct {
	Kind       FailureKind
	Operation  string
	StatusCode int
	Reason     string
}

// Error returns a fixed safe diagnostic. Inspect metadata through FailureOf instead of formatting an adapter cause.
func (Failure) Error() string { return "application operation failed" }

// ApplicationFailure exposes a value copy through the same port used by adapters.
func (f Failure) ApplicationFailure() Failure { return f }

// FailureSource is the failure port consumed by workflow policy.
// A provider can preserve its public error type and expose neutral metadata through this port.
type FailureSource interface {
	error
	ApplicationFailure() Failure
}

// FailureOf finds neutral metadata through standard wrapped and joined errors.
// It never formats the error. Unknown errors return no classification.
func FailureOf(err error) (Failure, bool) {
	var source FailureSource
	if !errors.As(err, &source) {
		return Failure{}, false
	}
	return source.ApplicationFailure(), true
}

// Valid reports whether the category belongs to the application failure contract.
func (k FailureKind) Valid() bool {
	switch k {
	case Authentication, Configuration, Retryable, Malformed, ReviewRequired, AmbiguousWrite, Terminal:
		return true
	default:
		return false
	}
}
