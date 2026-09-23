package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMarkerStrictAndEmbedded(t *testing.T) {
	fp := strings.Repeat("a", 64)
	if m, ok := parseMarker("prefix [index01:" + fp + ":12] suffix"); !ok || m.Fingerprint != fp || m.Index != 12 {
		t.Fatal("valid marker was not parsed")
	}
	for _, s := range []string{"[index01:" + strings.Repeat("a", 63) + ":1]", "[index01:" + strings.Repeat("A", 64) + ":1]", "[index01:" + fp + "]"} {
		if _, ok := parseMarker(s); ok {
			t.Fatalf("accepted invalid marker %q", s)
		}
	}
}

func TestJoinAndPrivatePath(t *testing.T) {
	d := t.TempDir()
	fp := strings.Repeat("b", 64)
	markerText := "[index01:" + fp + ":0]"
	snap := snapshot{Tasks: []taskOut{{ID: "task-1", Content: markerText + " " + markerText}, {ID: "orphan", Content: "[index01:" + strings.Repeat("c", 64) + ":0]"}}}
	ev := evidence{Archive: []archive{{Fingerprint: fp, Transcript: "remember milk", Deliveries: []delivery{{ItemIndex: 0, TaskID: "task-1", Kind: "task", Title: "Milk"}}}}, Findings: []finding{{TaskID: "task-1", Status: "missing"}}}
	sb, _ := json.Marshal(snap)
	eb, _ := json.Marshal(ev)
	sp := filepath.Join(d, "snapshot.json")
	ep := filepath.Join(d, "evidence.json")
	op := filepath.Join(d, "cases.json")
	if err := os.WriteFile(sp, sb, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ep, eb, 0600); err != nil {
		t.Fatal(err)
	}
	if err := join(sp, ep, op); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(op)
	if err != nil {
		t.Fatal(err)
	}
	var got casesFile
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Cases) != 1 || got.Cases[0].WeakLabel != "unsupported" || got.Cases[0].ObservedStatus != "missing" {
		t.Fatalf("joined=%+v", got)
	}
	if got.Summary.Joined != 1 || got.Summary.ByStatus["missing"] != 1 {
		t.Fatalf("duplicate marker changed summary: %+v", got.Summary)
	}
	if err := privatePath("."); err == nil {
		t.Fatal("repository path accepted")
	}
	linkedWorktree := filepath.Join(d, "linked-worktree")
	if err := os.Mkdir(linkedWorktree, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedWorktree, ".git"), []byte("gitdir: elsewhere"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := privatePath(filepath.Join(linkedWorktree, "private.json")); err == nil {
		t.Fatal("linked worktree path accepted")
	}
	repositoryLink := filepath.Join(d, "repository-link")
	if err := os.Symlink(linkedWorktree, repositoryLink); err != nil {
		t.Fatal(err)
	}
	if err := privatePath(filepath.Join(repositoryLink, "private.json")); err == nil {
		t.Fatal("symlinked repository path accepted")
	}
	target := filepath.Join(d, "target.json")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	outputLink := filepath.Join(d, "output-link.json")
	if err := os.Symlink(target, outputLink); err != nil {
		t.Fatal(err)
	}
	if err := write0600(outputLink, map[string]string{"private": "content"}); err == nil {
		t.Fatal("symlink output path accepted")
	}
	if preserved, err := os.ReadFile(target); err != nil || string(preserved) != "preserve" {
		t.Fatalf("symlink target changed: %q, %v", preserved, err)
	}
}

// TestPullWritesPrivateSnapshotAndKeepsTokenSecret guards the live pull path.
func TestPullWritesPrivateSnapshotAndKeepsTokenSecret(t *testing.T) {
	const secret = "ticktick-secret-token"
	old := apiBase
	defer func() { apiBase = old }()
	fp := strings.Repeat("a", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("authorization header was %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/open/v1/project":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": "p1", "name": "Home", "kind": "TASK", "permission": "write"}})
		case "/open/v1/project/p1/data":
			_ = json.NewEncoder(w).Encode(map[string]any{"tasks": []map[string]any{{"id": "t1", "projectId": "p1", "title": "Buy milk", "content": "prefix [index01:" + fp + ":0]", "kind": "TEXT"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	apiBase = srv.URL
	t.Setenv("INDEX01_TICKTICK_TOKEN", secret)
	out := filepath.Join(t.TempDir(), "snapshot.json")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = writer
	pullErr := pull(out, srv.Client())
	_ = writer.Close()
	os.Stdout = oldStdout
	var stdout strings.Builder
	_, _ = io.Copy(&stdout, reader)
	_ = reader.Close()
	if pullErr != nil {
		t.Fatal(pullErr)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot mode=%v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var snap snapshot
	if err = json.Unmarshal(data, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 1 || snap.Tasks[0].Title != "Buy milk" {
		t.Fatalf("snapshot tasks=%+v", snap.Tasks)
	}
	if !strings.Contains(stdout.String(), "tasks: 1") || !strings.Contains(stdout.String(), "marker: 1") {
		t.Fatalf("summary=%q", stdout.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(string(data), secret) {
		t.Fatal("the token leaked into the summary or the snapshot")
	}
}

// TestPullRejectsNon200WithoutLeakingToken keeps provider errors secret-safe.
func TestPullRejectsNon200WithoutLeakingToken(t *testing.T) {
	const secret = "ticktick-secret-token"
	old := apiBase
	defer func() { apiBase = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "denied", http.StatusUnauthorized)
	}))
	defer srv.Close()
	apiBase = srv.URL
	t.Setenv("INDEX01_TICKTICK_TOKEN", secret)
	err := pull(filepath.Join(t.TempDir(), "snapshot.json"), srv.Client())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error=%v, want the status in the message", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("the error leaked the token")
	}
}

// TestJoinWeakLabelsForMoveMissingAndUnknown pins the weak-label mapping.
func TestJoinWeakLabelsForMoveMissingAndUnknown(t *testing.T) {
	dir := t.TempDir()
	fpMove, fpMissing, fpUnknown := strings.Repeat("d", 64), strings.Repeat("e", 64), strings.Repeat("f", 64)
	missingCandidate := realCandidate{Kind: "task", Title: "Gone from extraction", Content: "full archived content", Due: "2026-09-21", AllDay: true, Priority: 3, Tags: []string{"private"}, ProjectAlias: "home"}
	snap := snapshot{Tasks: []taskOut{
		{ID: "t-move", Content: "[index01:" + fpMove + ":0]"},
		{ID: "t-unknown", Content: "[index01:" + fpUnknown + ":0]"},
	}}
	ev := evidence{
		Archive: []archive{
			{Fingerprint: fpMove, Transcript: "move me", Deliveries: []delivery{{ItemIndex: 0, TaskID: "t-move", Kind: "task", Title: "Move me"}}},
			{Fingerprint: fpMissing, Transcript: "gone", Extraction: &extraction{Items: []realCandidate{missingCandidate}}, Deliveries: []delivery{{ItemIndex: 0, TaskID: "t-missing", Kind: "task", Title: "Delivery fallback"}}},
			{Fingerprint: fpUnknown, Transcript: "unclear", Deliveries: []delivery{{ItemIndex: 0, TaskID: "t-unknown", Kind: "task", Title: "Unclear"}}},
		},
		Findings: []finding{
			{TaskID: "t-move", Status: "observed_move"},
			{TaskID: "t-missing", Status: "missing"},
			{TaskID: "t-unknown", Status: "unrecognized_status"},
		},
	}
	sb, _ := json.Marshal(snap)
	eb, _ := json.Marshal(ev)
	sp, ep, op := filepath.Join(dir, "s.json"), filepath.Join(dir, "e.json"), filepath.Join(dir, "c.json")
	if err := os.WriteFile(sp, sb, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ep, eb, 0600); err != nil {
		t.Fatal(err)
	}
	if err := join(sp, ep, op); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(op)
	if err != nil {
		t.Fatal(err)
	}
	var got casesFile
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	byTranscript := map[string]caseOut{}
	for _, c := range got.Cases {
		byTranscript[c.Transcript] = c
	}
	move, ok := byTranscript["move me"]
	if !ok || move.WeakLabel != "supported" || move.WeakRouteOK {
		t.Fatalf("moved item=%+v, want supported with a wrong route", move)
	}
	missing, ok := byTranscript["gone"]
	if !ok || missing.WeakLabel != "unsupported" || missing.ObservedStatus != "missing" {
		t.Fatalf("missing item=%+v, want unsupported from the archive", missing)
	}
	if missing.Candidate.Title != missingCandidate.Title || missing.Candidate.Content != missingCandidate.Content || missing.Candidate.Due != missingCandidate.Due || !missing.Candidate.AllDay || missing.Candidate.Priority != missingCandidate.Priority || len(missing.Candidate.Tags) != 1 || missing.Candidate.Tags[0] != "private" || missing.Candidate.ProjectAlias != missingCandidate.ProjectAlias {
		t.Fatalf("missing candidate=%+v, want archived extraction candidate %+v", missing.Candidate, missingCandidate)
	}
	unknown, ok := byTranscript["unclear"]
	if !ok || unknown.WeakLabel != "unknown" {
		t.Fatalf("unknown item=%+v, want an unknown label", unknown)
	}
	if got.Summary.Joined != 3 || got.Summary.WithTranscript != 3 {
		t.Fatalf("summary=%+v", got.Summary)
	}
	if got.Summary.ByStatus["missing"] != 1 || got.Summary.ByStatus["observed_move"] != 1 {
		t.Fatalf("status counts=%+v", got.Summary.ByStatus)
	}
}
