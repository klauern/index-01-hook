package main

import (
	"testing"
	"time"
)

// TestDateValidatorUsesSuppliedYearWindow proves that validation uses the frozen request clock.
func TestDateValidatorUsesSuppliedYearWindow(t *testing.T) {
	parts := TypeSafeDateParts{Mode: "absolute", Year: 2035, Month: 12, Day: 31}
	if err := validateDateParts(parts, time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC); err != nil {
		t.Fatal(err)
	}
	if err := validateDateParts(parts, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC); err == nil {
		t.Fatal("validator ignored explicit year window")
	}
}
