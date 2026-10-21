package main

import (
	"github.com/klauern/index-01-hook/internal/application"
	"reflect"
	"strings"
	"testing"
)

// TestApplicationItemPolicyCompatibility compares application policy with the existing production boundaries.
func TestApplicationItemPolicyCompatibility(t *testing.T) {
	for _, kind := range []ItemKind{ItemKindTask, ItemKindNote, "unknown"} {
		for _, title := range []string{"ok", " ", strings.Repeat("é", 250), strings.Repeat("é", 251)} {
			for _, priority := range []int{0, 1, 2, 3, 5} {
				for _, alias := range []string{"", " Work ", "A\nB", strings.Repeat("é", 51)} {
					item := QueuedItem{Kind: kind, Title: title, Priority: priority, ProjectAlias: alias}
					old := validateFrozenExtraction(FrozenExtraction{Provider: "fixture", Model: "fixture", Items: []QueuedItem{item}})
					next := application.ValidateItem(item)
					if (old == nil) != (next == nil) {
						t.Fatalf("policy differs: kind=%q priority=%d titleBytes=%d aliasBytes=%d old=%v new=%v", kind, priority, len(title), len(alias), old, next)
					}
				}
			}
		}
	}
	for _, aliases := range [][]string{nil, {" WORK ", "home", "work"}, {"ok", "bad\nvalue"}, {strings.Repeat("é", 50)}, {strings.Repeat("é", 51)}} {
		old, e := normalizeProjectAliases(aliases)
		next, f := application.NormalizeAliases(aliases)
		if (e == nil) != (f == nil) || !reflect.DeepEqual(old, next) {
			t.Fatalf("alias policy differs: old=%v/%v new=%v/%v", old, e, next, f)
		}
	}
	for _, tags := range [][]string{nil, {" B ", "a", "B", "b"}, {" "}, {strings.Repeat("é", 50)}, {strings.Repeat("é", 51)}, strings.Fields(strings.Repeat("a ", 11))} {
		old, e := normalizeTickTickTags(tags)
		next, f := application.NormalizeTags(tags)
		if (e == nil) != (f == nil) || !reflect.DeepEqual(old, next) {
			t.Fatalf("tag policy differs: old=%v/%v new=%v/%v", old, e, next, f)
		}
	}
}
