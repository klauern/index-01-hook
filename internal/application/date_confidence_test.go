package application_test

import (
	"math"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/application"
)

// TestDateConfidenceRejectsInvalidUsedValues requires review for malformed supplied probability values.
func TestDateConfidenceRejectsInvalidUsedValues(t *testing.T) {
	now := time.Date(2035, 1, 2, 12, 0, 0, 0, time.UTC)
	for _, mode := range []string{"absolute", "relative", "weekday"} {
		fields := []string{"mode"}
		switch mode {
		case "absolute":
			fields = append(fields, "month", "day", "year")
		case "relative":
			fields = append(fields, "day_anchor")
		case "weekday":
			fields = append(fields, "weekday")
		}
		for _, field := range fields {
			for _, bad := range []struct {
				name  string
				value float64
			}{{"NaN", math.NaN()}, {"positive infinity", math.Inf(1)}, {"negative infinity", math.Inf(-1)}, {"below zero", -.01}, {"above one", 1.01}} {
				t.Run(mode+"/"+field+"/"+bad.name, func(t *testing.T) {
					parts := application.DateParts{Mode: mode, Year: 2035, Month: 1, Day: 3, DayAnchor: "tomorrow", Weekday: "thu", Confidence: map[string]float64{field: bad.value}}
					date, confidence, review := application.ResolveDateParts(parts, now, time.UTC)
					if !review || !date.IsZero() || confidence != 0 {
						t.Fatalf("malformed confidence produced date=%v confidence=%v review=%v", date, confidence, review)
					}
				})
			}
		}
	}
}

// TestDateConfidencePreservesValidAndUnusedValues retains confidence thresholds and existing missing-field behavior.
func TestDateConfidencePreservesValidAndUnusedValues(t *testing.T) {
	now := time.Date(2035, 1, 2, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		confidence map[string]float64
		want       float64
		review     bool
	}{
		{name: "missing values", want: 1},
		{name: "zero", confidence: map[string]float64{"mode": 0}, want: 0, review: true},
		{name: "low valid probability", confidence: map[string]float64{"mode": .49}, want: .49, review: true},
		{name: "threshold", confidence: map[string]float64{"mode": .5}, want: .5},
		{name: "one", confidence: map[string]float64{"mode": 1}, want: 1},
		{name: "minimum used field", confidence: map[string]float64{"mode": .9, "day_anchor": .8}, want: .8},
		{name: "invalid unused field", confidence: map[string]float64{"mode": .9, "year": math.NaN()}, want: .9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			date, confidence, review := application.ResolveDateParts(application.DateParts{Mode: "relative", DayAnchor: "tomorrow", Confidence: tc.confidence}, now, time.UTC)
			want := time.Date(2035, 1, 3, 0, 0, 0, 0, time.UTC)
			if !date.Equal(want) || confidence != tc.want || review != tc.review {
				t.Fatalf("date=%v confidence=%v review=%v", date, confidence, review)
			}
		})
	}
}
