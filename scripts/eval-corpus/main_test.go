package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

func source() []byte {
	fp := strings.Repeat("a", 64)
	data, _ := json.Marshal(map[string]any{"format_version": 1, "collected_at": "2026-09-08T12:00:00Z", "collection": map[string]any{"failures": []any{}}, "candidates": []any{map[string]any{
		"candidate_id":        "private-candidate",
		"source":              map[string]any{"task_id": "private-task", "markers": []any{map[string]any{"marker": "[index01:" + fp + ":0]", "recording_fingerprint": fp, "item_index": 0}}},
		"observed_current":    map[string]any{"project_id": "home", "kind": "TEXT", "title": "Private title"},
		"input":               map[string]any{"transcript": "Private synthetic fixture text", "transcript_provenance": "original"},
		"review":              map[string]any{"status": "approved", "expected_project": "home"},
		"historical_delivery": map[string]any{"expected_item_count": 1},
	}}})
	return data
}

func TestPrivateCLIOutputAndConfiguration(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	config := filepath.Join(dir, "config.json")
	out := filepath.Join(dir, "private", "corpus.json")
	if err := os.WriteFile(input, source(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"clock":"2026-09-08T12:00:00Z","time_zone":"America/Chicago","aliases":{"home":"home"},"default_project_id":"default","note_project_id":"notes"}`), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--input", input, "--config", config, "--out", out}
	var stdout bytes.Buffer
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ready: 1") || strings.Contains(stdout.String(), "Private") || strings.Contains(stdout.String(), "private-task") || strings.Contains(stdout.String(), dir) {
		t.Fatal("wrong or sensitive CLI output:", stdout.String())
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	c, err := evalcorpus.Load(data)
	if err != nil {
		t.Fatal(err)
	}
	if c.Routing == nil || len(c.Examples[0].SavedOutput) != 0 {
		t.Fatal("routing or output provenance changed")
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("output mode not private")
	}
	info, err = os.Stat(filepath.Dir(out))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("new output directory not private")
	}
	if err := run(args, &stdout); err == nil {
		t.Fatal("output overwritten")
	}
	for _, path := range []string{input, config} {
		args[len(args)-1] = path
		if err := run(args, &stdout); err == nil {
			t.Fatal("input overwritten")
		}
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = link
	if err := run(args, &stdout); err == nil {
		t.Fatal("output symlink followed")
	}
	after, _ := os.ReadFile(out)
	if !bytes.Equal(after, data) {
		t.Fatal("existing output changed")
	}
	if err := run([]string{"--input", input}, &stdout); err == nil {
		t.Fatal("missing output accepted")
	}
}

func TestAuditImportWithoutConfiguration(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	out := filepath.Join(dir, "corpus.json")
	if err := os.WriteFile(input, source(), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := run([]string{"--input", input, "--out", out}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "ready: 0") || !strings.Contains(stdout.String(), "blocked: 1") {
		t.Fatal("unconfigured import reported ready")
	}
}

func TestInvalidConfigurationDoesNotCreateOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	out := filepath.Join(dir, "corpus.json")
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(input, source(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"time_zone":"private-invalid-zone"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	err := run([]string{"--input", input, "--config", config, "--out", out}, &stdout)
	if err == nil {
		t.Fatal("invalid config accepted")
	}
	if strings.Contains(err.Error(), "private-invalid-zone") || strings.Contains(err.Error(), dir) {
		t.Fatal("private data in error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("output exists after validation failure")
	}
}
