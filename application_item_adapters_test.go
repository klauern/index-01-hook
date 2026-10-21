package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCanonicalItemFreezeBoundaries checks that invalid application items leave no partial queue state.
func TestCanonicalItemFreezeBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name string
		item QueuedItem
	}{
		{"title bytes", QueuedItem{Kind: ItemKindTask, Title: strings.Repeat("é", 251)}},
		{"content bytes", QueuedItem{Kind: ItemKindNote, Title: "ok", Content: strings.Repeat("é", 16385)}},
		{"alias bytes", QueuedItem{Kind: ItemKindTask, Title: "ok", ProjectAlias: strings.Repeat("é", 51)}},
		{"tag bytes", QueuedItem{Kind: ItemKindTask, Title: "ok", Tags: []string{strings.Repeat("é", 51)}}},
		{"note fields", QueuedItem{Kind: ItemKindNote, Title: "ok", ProjectAlias: " "}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, _ := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "synthetic policy fixture")
			claim, err := store.ClaimExtraction(context.Background(), "policy", time.Minute)
			if err != nil || claim == nil {
				t.Fatalf("claim: %v", err)
			}
			frozen := FrozenExtraction{Provider: "fixture", Model: "fixture", Items: []QueuedItem{{Kind: ItemKindTask, Title: "valid sibling"}, tt.item}}
			if err := store.FreezeExtraction(context.Background(), receipt.ID, "policy", claim.AttemptNumber, frozen); err == nil {
				t.Fatal("invalid item accepted")
			}
			for _, table := range []string{"extractions", "delivery_tasks"} {
				var count int
				if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("partial %s count=%d err=%v", table, count, err)
				}
			}
			frozen.Items = []QueuedItem{{Kind: ItemKindTask, Title: strings.Repeat("é", 250), Content: strings.Repeat("a", 32768), Tags: []string{strings.Repeat("é", 50)}, ProjectAlias: strings.Repeat("é", 50)}}
			if err := store.FreezeExtraction(context.Background(), receipt.ID, "policy", claim.AttemptNumber, frozen); err != nil {
				t.Fatalf("boundary rejected after invalid attempt: %v", err)
			}
		})
	}
}

// TestCanonicalPolicyKeepsLegacyErrors checks the legacy task boundary after application adoption.
func TestCanonicalPolicyKeepsLegacyErrors(t *testing.T) {
	for _, tt := range []struct {
		task QueuedTask
		want string
	}{
		{QueuedTask{Title: " "}, "task title is invalid"},
		{QueuedTask{Title: "ok", Notes: strings.Repeat("a", 32769)}, "task notes are too large"},
		{QueuedTask{Title: "ok", Priority: 2}, "task priority is invalid"},
		{QueuedTask{Title: "ok", Tags: []string{" "}}, "task tags are invalid"},
		{QueuedTask{Title: "ok", AllDay: true}, "all-day task requires a due date"},
		{QueuedTask{Title: "ok", ProjectAlias: strings.Repeat("a", 101)}, "project alias is too large"},
	} {
		err := validateFrozenExtraction(FrozenExtraction{Provider: "fixture", Model: "fixture", Tasks: []QueuedTask{tt.task}})
		if err == nil || err.Error() != tt.want {
			t.Fatalf("legacy err=%v want %q", err, tt.want)
		}
	}
}
