package application_test

import (
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/application"
)

// TestResolveDatePartsExplicitClock preserves calendar arithmetic across midnight and seasonal zone changes.
func TestResolveDatePartsExplicitClock(t *testing.T) {
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, now, want string
		location        *time.Location
		parts           application.DateParts
		review          bool
	}{
		{name: "local midnight", now: "2026-03-08T05:30:00Z", location: chicago, parts: application.DateParts{Mode: "relative", DayAnchor: "tomorrow"}, want: "2026-03-08T00:00:00-06:00"},
		{name: "UTC midnight", now: "2026-03-08T05:30:00Z", parts: application.DateParts{Mode: "relative", DayAnchor: "tomorrow"}, want: "2026-03-09T00:00:00Z"},
		{name: "spring calendar day", now: "2026-03-08T01:30:00-06:00", location: chicago, parts: application.DateParts{Mode: "relative", DayAnchor: "tomorrow"}, want: "2026-03-09T00:00:00-05:00"},
		{name: "spring yesterday", now: "2026-03-09T00:30:00-05:00", location: chicago, parts: application.DateParts{Mode: "relative", DayAnchor: "yesterday"}, want: "2026-03-08T00:00:00-06:00"},
		{name: "fall calendar day", now: "2026-11-01T00:30:00-05:00", location: chicago, parts: application.DateParts{Mode: "relative", DayAnchor: "tomorrow"}, want: "2026-11-02T00:00:00-06:00"},
		{name: "fall week", now: "2026-10-31T23:30:00-05:00", location: chicago, parts: application.DateParts{Mode: "relative", DayAnchor: "tomorrow", WeekOffset: 1}, want: "2026-11-08T00:00:00-06:00"},
		{name: "same weekday", now: "2026-03-07T23:30:00-06:00", location: chicago, parts: application.DateParts{Mode: "weekday", Weekday: " sat "}, want: "2026-03-07T00:00:00-06:00"},
		{name: "next weekday", now: "2026-03-07T23:30:00-06:00", location: chicago, parts: application.DateParts{Mode: "weekday", Weekday: "thu"}, want: "2026-03-12T00:00:00-05:00"},
		{name: "weekday week offset", now: "2026-03-07T23:30:00-06:00", location: chicago, parts: application.DateParts{Mode: "weekday", Weekday: "thu", WeekOffset: 1}, want: "2026-03-19T00:00:00-05:00"},
		{name: "invalid weekday offset", now: "2026-03-07T00:00:00Z", parts: application.DateParts{Mode: "weekday", Weekday: "thu", WeekOffset: -1}, review: true},
		{name: "invalid anchor", now: "2026-03-07T00:00:00Z", parts: application.DateParts{Mode: "relative", DayAnchor: "someday"}, review: true},
		{name: "impossible absolute", now: "2026-03-07T00:00:00Z", parts: application.DateParts{Mode: "absolute", Year: 2026, Month: 2, Day: 30}, review: true},
		{name: "lower year bound", now: "2026-03-07T00:00:00Z", parts: application.DateParts{Mode: "absolute", Year: 2025, Month: 1, Day: 1}, want: "2025-01-01T00:00:00Z"},
		{name: "upper year bound", now: "2026-03-07T00:00:00Z", parts: application.DateParts{Mode: "absolute", Year: 2028, Month: 2, Day: 29}, want: "2028-02-29T00:00:00Z"},
		{name: "outside year window", now: "2026-03-07T00:00:00Z", parts: application.DateParts{Mode: "absolute", Year: 2029, Month: 1, Day: 1}, review: true},
		{name: "future frozen request", now: "2035-12-31T23:59:00Z", parts: application.DateParts{Mode: " relative ", DayAnchor: " TOMORROW "}, want: "2036-01-01T00:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				got, confidence, review := application.ResolveDateParts(tc.parts, now, tc.location)
				if review != tc.review || confidence != 1 {
					t.Fatalf("review=%v confidence=%v", review, confidence)
				}
				if tc.want == "" {
					if !got.IsZero() {
						t.Fatalf("invalid date resolved=%v", got)
					}
				} else if got.Format(time.RFC3339) != tc.want {
					t.Fatalf("date=%s want=%s", got.Format(time.RFC3339), tc.want)
				}
			}
		})
	}
}

// TestDatePartsConfidenceCompatibility preserves used-field minimums and the existing threshold boundary.
func TestDatePartsConfidenceCompatibility(t *testing.T) {
	now := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		parts      application.DateParts
		confidence float64
		review     bool
	}{
		{"minimum", application.DateParts{Mode: "absolute", Year: 2026, Month: 3, Day: 8, Confidence: map[string]float64{"mode": .99, "month": .8, "day": .74, "year": .9}}, .74, false},
		{"threshold", application.DateParts{Mode: "relative", DayAnchor: "today", Confidence: map[string]float64{"day_anchor": .5}}, .5, false},
		{"below threshold", application.DateParts{Mode: "relative", DayAnchor: "today", Confidence: map[string]float64{"day_anchor": .49}}, .49, true},
		{"unused field", application.DateParts{Mode: "weekday", Weekday: "mon", Confidence: map[string]float64{"weekday": .8, "month": .1}}, .8, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, confidence, review := application.ResolveDateParts(tc.parts, now, time.UTC)
			if confidence != tc.confidence || review != tc.review {
				t.Fatalf("confidence=%v review=%v", confidence, review)
			}
		})
	}
}
