package application

import (
	"strings"
	"time"
)

// DateParts contains semantic date fields without provider transport types.
type DateParts struct {
	Mode       string
	Month      int
	Day        int
	Year       int
	DayAnchor  string
	Weekday    string
	WeekOffset int
	Confidence map[string]float64
}

// DueError identifies a safe date-policy failure without retaining supplied text.
type DueError string

const (
	DueMissing       DueError = "all-day task has no due date"
	DueDateInvalid   DueError = "all-day due date is invalid"
	DueTimeInvalid   DueError = "timed due date is invalid"
	DueOffsetInvalid DueError = "timed due date has an invalid time zone offset"
)

// Error returns the stable safe diagnostic for the date-policy failure.
func (e DueError) Error() string { return string(e) }

// ParseDue preserves date-only calendar values and validates timed zone offsets.
// The supplied location owns zone policy; the function never reads the clock.
func ParseDue(raw *string, allDay bool, location *time.Location) (*time.Time, error) {
	if location == nil {
		location = time.UTC
	}
	if raw == nil {
		if allDay {
			return nil, DueMissing
		}
		return nil, nil
	}
	value := strings.TrimSpace(*raw)
	if allDay {
		parsed, err := time.ParseInLocation("2006-01-02", value, location)
		if err != nil {
			return nil, DueDateInvalid
		}
		return &parsed, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, DueTimeInvalid
	}
	_, suppliedOffset := parsed.Zone()
	_, localOffset := parsed.In(location).Zone()
	if suppliedOffset != localOffset {
		return nil, DueOffsetInvalid
	}
	return &parsed, nil
}
