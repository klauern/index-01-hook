package main

import (
	"math"
	"testing"
	"time"
)

// TestDateCompatibilityRejectsNonFiniteConfidence protects both root compatibility boundaries.
func TestDateCompatibilityRejectsNonFiniteConfidence(t *testing.T) {
	parts := TypeSafeDateParts{Mode: "relative", DayAnchor: "tomorrow", Confidence: map[string]float64{"mode": math.NaN()}}
	now := time.Date(2035, 1, 2, 12, 0, 0, 0, time.UTC)
	date, confidence, review := ResolveDateParts(parts, now, time.UTC)
	if !review || !date.IsZero() || confidence != 0 {
		t.Fatalf("root resolver accepted malformed confidence: %v %v %v", date, confidence, review)
	}
	if err := validateDateParts(parts, now, time.UTC); err == nil {
		t.Fatal("root validator accepted malformed confidence")
	}
}
