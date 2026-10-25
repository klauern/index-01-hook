package main

import (
	"errors"
	"strings"
	"testing"
)

// TestApplicationIdentityMarkerCompatibility preserves marker bytes across editable provider fields.
func TestApplicationIdentityMarkerCompatibility(t *testing.T) {
	hash := strings.Repeat("a", 64)
	want := "[index01:" + hash + ":3]"
	for _, project := range []string{"original", "moved"} {
		_, marker, err := normalizeTickTickTask(project, TickTickTaskInput{Title: "Edited task", Content: "Changed details", RecordingHash: " " + strings.ToUpper(hash) + " ", TaskIndex: 3, Priority: 5, Tags: []string{"changed"}})
		if err != nil || marker != want {
			t.Fatalf("task marker=%q err=%v", marker, err)
		}
		_, noteMarker, err := normalizeTickTickNote(project, TickTickNoteInput{Title: "Edited note", Content: "Different details", RecordingHash: hash, TaskIndex: 3})
		if err != nil || noteMarker != want {
			t.Fatalf("note marker=%q err=%v", noteMarker, err)
		}
	}
	if !validTickTickMarker(want) {
		t.Fatal("canonical persisted marker rejected")
	}
	for _, bad := range []string{" " + want, strings.Replace(want, hash, strings.ToUpper(hash), 1), strings.Replace(want, ":3]", ":03]", 1), strings.Replace(want, ":3]", ":+3]", 1), want + "\n"} {
		if validTickTickMarker(bad) {
			t.Errorf("accepted noncanonical marker %q", bad)
		}
	}
}

// TestApplicationIdentityProviderErrors preserves adapter categories and safe public diagnostics.
func TestApplicationIdentityProviderErrors(t *testing.T) {
	for _, tc := range []struct {
		hash   string
		index  int
		detail string
	}{
		{"private-synthetic-payload", 0, "recording hash is invalid"},
		{strings.Repeat("a", 64), -1, "task index is invalid"},
	} {
		marker, err := tickTickMarker(tc.hash, tc.index)
		var provider *TickTickError
		if marker != "" || !errors.As(err, &provider) || provider.Kind != TickTickErrorMalformed || provider.Operation != "normalize task" || provider.Detail != tc.detail {
			t.Fatalf("marker=%q error=%+v", marker, err)
		}
		if strings.Contains(err.Error(), "private-synthetic-payload") {
			t.Fatal("provider diagnostic exposed input")
		}
	}
}
