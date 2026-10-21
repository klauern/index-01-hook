package application_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/application"
)

// TestParseDue preserves all-day dates, seasonal offsets and safe failure categories.
func TestParseDue(t *testing.T) {
	location, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, raw      string
		absent, allDay bool
		want           string
		failure        application.DueError
	}{
		{name: "no date", absent: true},
		{name: "missing all-day", absent: true, allDay: true, failure: application.DueMissing},
		{name: "all-day before DST", raw: " 2026-03-08 ", allDay: true, want: "2026-03-08T00:00:00-06:00"},
		{name: "all-day after DST", raw: "2026-03-09", allDay: true, want: "2026-03-09T00:00:00-05:00"},
		{name: "leap date", raw: "2028-02-29", allDay: true, want: "2028-02-29T00:00:00-06:00"},
		{name: "impossible date", raw: "2026-02-30", allDay: true, failure: application.DueDateInvalid},
		{name: "date with time", raw: "2026-03-09T00:00:00-05:00", allDay: true, failure: application.DueDateInvalid},
		{name: "spring before shift", raw: "2026-03-08T01:30:00-06:00", want: "2026-03-08T01:30:00-06:00"},
		{name: "spring after shift", raw: "2026-03-08T03:30:00-05:00", want: "2026-03-08T03:30:00-05:00"},
		{name: "nonexistent spring hour", raw: "2026-03-08T02:30:00-06:00", failure: application.DueOffsetInvalid},
		{name: "fall first hour", raw: "2026-11-01T01:30:00-05:00", want: "2026-11-01T01:30:00-05:00"},
		{name: "fall repeated hour", raw: "2026-11-01T01:30:00-06:00", want: "2026-11-01T01:30:00-06:00"},
		{name: "wrong summer offset", raw: "2026-07-01T09:00:00-06:00", failure: application.DueOffsetInvalid},
		{name: "missing timed offset", raw: "2026-07-01T09:00:00", failure: application.DueTimeInvalid},
		{name: "private malformed value", raw: "synthetic-private-date", failure: application.DueTimeInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := &tc.raw
			if tc.absent {
				raw = nil
			}
			got, err := application.ParseDue(raw, tc.allDay, location)
			if tc.failure != "" {
				if got != nil || !errors.Is(err, tc.failure) {
					t.Fatalf("got=%v err=%v", got, err)
				}
				if strings.Contains(err.Error(), "synthetic-private-date") {
					t.Fatal("error exposed supplied text")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if got != nil {
					t.Fatal("missing date became a due date")
				}
				return
			}
			if got == nil || got.Format(time.RFC3339) != tc.want {
				t.Fatalf("got=%v want=%s", got, tc.want)
			}
		})
	}
	raw := "2026-01-01"
	due, err := application.ParseDue(&raw, true, nil)
	if err != nil || due.Location() != time.UTC {
		t.Fatalf("nil-location fallback=%v %v", due, err)
	}
}
