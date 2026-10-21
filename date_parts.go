package main

import (
	"fmt"
	"github.com/klauern/index-01-hook/internal/application"
	"time"
)

// TypeSafeDateParts retains the root compatibility name for canonical application date fields.
type TypeSafeDateParts = application.DateParts

// ResolveDateParts delegates semantic date policy with the caller's explicit clock and location.
func ResolveDateParts(parts TypeSafeDateParts, now time.Time, location *time.Location) (time.Time, float64, bool) {
	return application.ResolveDateParts(parts, now, location)
}

// validateDateParts requires the same explicit time and location as semantic resolution.
// No production caller used the former hidden-clock helper.
func validateDateParts(parts TypeSafeDateParts, now time.Time, location *time.Location) error {
	if _, _, review := ResolveDateParts(parts, now, location); review {
		return fmt.Errorf("date parts require review")
	}
	return nil
}
