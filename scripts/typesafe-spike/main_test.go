package main

import "testing"

// TestFabricatedCandidatesPreserveOriginals checks ordering and independent maps.
func TestFabricatedCandidatesPreserveOriginals(t *testing.T) {
	original := []candidate{
		{Transcript: "synthetic task", Item: map[string]any{"title": "original title"}},
		{Transcript: "synthetic note", Item: map[string]any{"content": "original content"}},
	}
	got := withFabricatedCandidates(original)
	if len(got) != 4 {
		t.Fatalf("candidate count=%d", len(got))
	}
	assertCandidateField(t, got[0], "title", "original title")
	assertCandidateField(t, got[1], "content", "original content")
	assertCandidateField(t, got[2], "title", "original title with an invented detail")
	assertCandidateField(t, got[3], "content", "original content with an invented detail")
	if countFabricated(got) != 2 {
		t.Fatal("fabricated count changed")
	}
	got[2].Item["title"] = "mutated copy"
	assertCandidateField(t, original[0], "title", "original title")
}

func assertCandidateField(t *testing.T, item candidate, key, want string) {
	t.Helper()
	if item.Item[key] != want {
		t.Fatalf("%s=%v, want %q", key, item.Item[key], want)
	}
}

func TestLiveSpikeRequiresBothTokenAndApproval(t *testing.T) {
	t.Setenv("INDEX01_TYPESAFE_SPIKE_APPROVED", "false")
	if liveSpikeApproved("synthetic-token") {
		t.Fatal("unapproved spike allowed")
	}
	t.Setenv("INDEX01_TYPESAFE_SPIKE_APPROVED", "true")
	if liveSpikeApproved("") {
		t.Fatal("spike without token allowed")
	}
	if !liveSpikeApproved("synthetic-token") {
		t.Fatal("approved spike rejected")
	}
}
