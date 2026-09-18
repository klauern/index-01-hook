package main

import (
	"testing"
	"time"
)

func TestResolveDateParts(t *testing.T) {
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.March, 7, 23, 30, 0, 0, location)
	tests := []struct {
		name   string
		parts  TypeSafeDateParts
		want   time.Time
		review bool
	}{
		{"absolute", TypeSafeDateParts{Mode: "absolute", Month: 3, Day: 8, Year: 2026}, time.Date(2026, time.March, 8, 0, 0, 0, 0, location), false},
		{"relative", TypeSafeDateParts{Mode: "relative", DayAnchor: "tomorrow"}, time.Date(2026, time.March, 8, 0, 0, 0, 0, location), false},
		{"weekday today or later", TypeSafeDateParts{Mode: "weekday", Weekday: "Saturday"}, time.Date(2026, time.March, 7, 0, 0, 0, 0, location), false},
		{"impossible", TypeSafeDateParts{Mode: "absolute", Month: 2, Day: 30, Year: 2026}, time.Time{}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, _, review := ResolveDateParts(test.parts, now, location)
			if review != test.review || (!review && !got.Equal(test.want)) {
				t.Fatalf("ResolveDateParts() = %v, review %v; want %v, review %v", got, review, test.want, test.review)
			}
		})
	}
}

func TestResolveDatePartsUsesMinimumConfidence(t *testing.T) {
	_, confidence, review := ResolveDateParts(TypeSafeDateParts{Mode: "absolute", Month: 3, Day: 8, Year: 2026, Confidence: map[string]float64{"mode": 0.99, "month": 0.91, "day": 0.74, "year": 0.88}}, time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC), time.UTC)
	if review || confidence != 0.74 {
		t.Fatalf("confidence = %v, review = %v; want 0.74, false", confidence, review)
	}
}
