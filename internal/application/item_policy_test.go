package application

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestItemPolicyBoundaries checks byte limits and task-only fields without mutating inputs.
func TestItemPolicyBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		item  Item
		valid bool
	}{
		{"task", Item{Kind: Task, Title: "ok"}, true},
		{"note", Item{Kind: Note, Title: "ok"}, true},
		{"unknown kind", Item{Kind: "other", Title: "ok"}, false},
		{"empty title", Item{Kind: Task, Title: " \t"}, false},
		{"title boundary", Item{Kind: Task, Title: strings.Repeat("é", 250)}, true},
		{"title bytes", Item{Kind: Task, Title: strings.Repeat("é", 251)}, false},
		{"title trim", Item{Kind: Task, Title: " " + strings.Repeat("a", 500) + " "}, true},
		{"content boundary", Item{Kind: Task, Title: "ok", Content: strings.Repeat("a", 32768)}, true},
		{"content bytes", Item{Kind: Note, Title: "ok", Content: strings.Repeat("é", 16385)}, false},
		{"due", Item{Kind: Task, Title: "ok", Due: &now, AllDay: true}, true},
		{"all day missing due", Item{Kind: Task, Title: "ok", AllDay: true}, false},
		{"note due", Item{Kind: Note, Title: "ok", Due: &now}, false},
		{"note all day", Item{Kind: Note, Title: "ok", AllDay: true}, false},
		{"note priority", Item{Kind: Note, Title: "ok", Priority: 1}, false},
		{"note tags", Item{Kind: Note, Title: "ok", Tags: []string{"x"}}, false},
		{"note alias", Item{Kind: Note, Title: "ok", ProjectAlias: " "}, false},
		{"alias boundary", Item{Kind: Task, Title: "ok", ProjectAlias: " " + strings.Repeat("é", 50) + " "}, true},
		{"alias bytes", Item{Kind: Task, Title: "ok", ProjectAlias: strings.Repeat("é", 51)}, false},
		{"legacy frozen alias", Item{Kind: Task, Title: "ok", ProjectAlias: "A\nB"}, true},
		{"priority invalid", Item{Kind: Task, Title: "ok", Priority: 2}, false},
		{"tags invalid", Item{Kind: Task, Title: "ok", Tags: []string{" "}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.item
			if got := ValidateItem(tt.item); (got == nil) != tt.valid {
				t.Fatalf("valid=%v, err=%v", tt.valid, got)
			}
			if !reflect.DeepEqual(before, tt.item) {
				t.Fatal("validation mutated item")
			}
		})
	}
	for _, p := range []int{0, 1, 3, 5} {
		if !ValidPriority(p) {
			t.Errorf("priority %d rejected", p)
		}
	}
}

// TestAliasAndTagNormalization checks ordering, byte boundaries and distinct input contracts.
func TestAliasAndTagNormalization(t *testing.T) {
	aliases := []string{" Work ", "HOME", "work"}
	before := append([]string(nil), aliases...)
	got, err := NormalizeAliases(aliases)
	if err != nil || !reflect.DeepEqual(got, []string{"home", "work"}) {
		t.Fatalf("aliases=%v err=%v", got, err)
	}
	if !reflect.DeepEqual(aliases, before) {
		t.Fatal("aliases mutated")
	}
	for _, bad := range []string{" ", "a\nb", "a\rb", "a\x00b", strings.Repeat("é", 51)} {
		if got, err := NormalizeAliases([]string{"ok", bad}); err == nil || got != nil {
			t.Fatalf("invalid alias accepted: %q", bad)
		}
	}
	if _, err := NormalizeAliases([]string{strings.Repeat("é", 50)}); err != nil {
		t.Fatal(err)
	}
	got, err = NormalizeTags([]string{" B ", "a", "B", "b"})
	if err != nil || !reflect.DeepEqual(got, []string{"B", "a", "b"}) {
		t.Fatalf("tags=%v err=%v", got, err)
	}
	for _, tags := range [][]string{{" "}, {strings.Repeat("é", 51)}, strings.Fields(strings.Repeat("a ", 11))} {
		if got, err := NormalizeTags(tags); err == nil || got != nil {
			t.Fatalf("invalid tags accepted: %v", tags)
		}
	}
	if _, err := NormalizeTags([]string{strings.Repeat("é", 50)}); err != nil {
		t.Fatal(err)
	}
	a, e := NormalizeAliases(nil)
	b, f := NormalizeTags(nil)
	if a == nil || b == nil || e != nil || f != nil {
		t.Fatal("empty output shape changed")
	}
}
