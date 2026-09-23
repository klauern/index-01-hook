package main

import (
	"fmt"
	"strings"
	"time"
)

type TypeSafeDateParts struct {
	Mode       string
	Month      int
	Day        int
	Year       int
	DayAnchor  string
	Weekday    string
	WeekOffset int
	Confidence map[string]float64
}

const minimumDateConfidence = 0.50

// ResolveDateParts resolves a provider judgment without asking the provider to do arithmetic.
// It returns review=true for missing, impossible, or out-of-window dates.
func ResolveDateParts(parts TypeSafeDateParts, now time.Time, location *time.Location) (time.Time, float64, bool) {
	if location == nil {
		location = time.UTC
	}
	localNow := now.In(location)
	confidence := datePartsConfidence(parts)
	mode := strings.ToLower(strings.TrimSpace(parts.Mode))
	switch mode {
	case "absolute":
		if parts.Month < 1 || parts.Month > 12 || parts.Day < 1 || parts.Day > 31 || parts.Year < localNow.Year()-1 || parts.Year > localNow.Year()+2 {
			return time.Time{}, confidence, true
		}
		candidate := time.Date(parts.Year, time.Month(parts.Month), parts.Day, 0, 0, 0, 0, location)
		if candidate.Year() != parts.Year || int(candidate.Month()) != parts.Month || candidate.Day() != parts.Day {
			return time.Time{}, confidence, true
		}
		return candidate, confidence, confidence < minimumDateConfidence
	case "relative":
		days, ok := relativeDayOffset(parts.DayAnchor)
		if !ok {
			return time.Time{}, confidence, true
		}
		candidate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location).AddDate(0, 0, days+parts.WeekOffset*7)
		return candidate, confidence, confidence < minimumDateConfidence
	case "weekday":
		weekday, ok := parseWeekday(parts.Weekday)
		if !ok || parts.WeekOffset < 0 {
			return time.Time{}, confidence, true
		}
		base := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
		delta := (int(weekday) - int(base.Weekday()) + 7) % 7
		candidate := base.AddDate(0, 0, delta+parts.WeekOffset*7)
		return candidate, confidence, confidence < minimumDateConfidence
	default:
		return time.Time{}, confidence, true
	}
}

func datePartsConfidence(parts TypeSafeDateParts) float64 {
	confidence := 1.0
	used := []string{"mode"}
	switch strings.ToLower(strings.TrimSpace(parts.Mode)) {
	case "absolute":
		used = append(used, "month", "day", "year")
	case "relative":
		used = append(used, "day_anchor")
	case "weekday":
		used = append(used, "weekday")
	}
	for _, key := range used {
		if value, ok := parts.Confidence[key]; ok && value < confidence {
			confidence = value
		}
	}
	return confidence
}

func relativeDayOffset(anchor string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(anchor)) {
	case "today":
		return 0, true
	case "tomorrow":
		return 1, true
	case "yesterday":
		return -1, true
	default:
		return 0, false
	}
}

func parseWeekday(value string) (time.Weekday, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "sunday", "sun":
		return time.Sunday, true
	case "monday", "mon":
		return time.Monday, true
	case "tuesday", "tue", "tues":
		return time.Tuesday, true
	case "wednesday", "wed":
		return time.Wednesday, true
	case "thursday", "thu", "thur", "thurs":
		return time.Thursday, true
	case "friday", "fri":
		return time.Friday, true
	case "saturday", "sat":
		return time.Saturday, true
	default:
		return time.Sunday, false
	}
}

func validateDateParts(parts TypeSafeDateParts) error {
	if _, _, review := ResolveDateParts(parts, time.Now(), time.UTC); review {
		return fmt.Errorf("date parts require review")
	}
	return nil
}
