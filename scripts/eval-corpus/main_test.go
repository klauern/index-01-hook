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
	writeFixture(t, input, source())
	writeFixture(t, config, []byte(`{"clock":"2026-09-08T12:00:00Z","time_zone":"America/Chicago","aliases":{"home":"home"},"default_project_id":"default","note_project_id":"notes"}`))
	args := []string{"--input", input, "--config", config, "--out", out}
	var stdout bytes.Buffer
	mustRun(t, args, &stdout)
	assertPrivateSummary(t, stdout.String(), dir)
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
	assertPrivatePermissions(t, out)
	assertNoClobber(t, args, data)
}

func TestAuditImportWithoutConfiguration(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	out := filepath.Join(dir, "corpus.json")
	writeFixture(t, input, source())
	var stdout bytes.Buffer
	mustRun(t, []string{"--input", input, "--out", out}, &stdout)
	if !strings.Contains(stdout.String(), "ready: 0") || !strings.Contains(stdout.String(), "blocked: 1") {
		t.Fatal("unconfigured import reported ready")
	}
}

func TestInvalidConfigurationDoesNotCreateOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	out := filepath.Join(dir, "corpus.json")
	config := filepath.Join(dir, "config.json")
	writeFixture(t, input, source())
	writeFixture(t, config, []byte(`{"time_zone":"private-invalid-zone"}`))
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

func assertPrivateSummary(t *testing.T, text, dir string) {
	t.Helper()
	if !strings.Contains(text, "ready: 1") {
		t.Fatal("wrong CLI output:", text)
	}
	for _, secret := range []string{"Private", "private-task", dir} {
		if strings.Contains(text, secret) {
			t.Fatal("sensitive CLI output:", text)
		}
	}
}
func assertPrivatePermissions(t *testing.T, out string) {
	t.Helper()
	for path, mode := range map[string]os.FileMode{out: 0600, filepath.Dir(out): 0700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("mode for %s: %o; want %o", path, info.Mode().Perm(), mode)
		}
	}
}
func assertNoClobber(t *testing.T, args []string, data []byte) {
	t.Helper()
	var stdout bytes.Buffer
	out := args[len(args)-1]
	requireRunFailure(t, args, &stdout, "output overwritten")
	for _, path := range []string{args[1], args[3]} {
		args[len(args)-1] = path
		requireRunFailure(t, args, &stdout, "input overwritten")
	}
	link := filepath.Join(filepath.Dir(filepath.Dir(out)), "link.json")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = link
	requireRunFailure(t, args, &stdout, "output symlink followed")
	after, _ := os.ReadFile(out)
	if !bytes.Equal(after, data) {
		t.Fatal("existing output changed")
	}
	requireRunFailure(t, []string{"--input", args[1]}, &stdout, "missing output accepted")
}

func writeFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func mustRun(t *testing.T, args []string, output *bytes.Buffer) {
	t.Helper()
	if err := run(args, output); err != nil {
		t.Fatal(err)
	}
}

func requireRunFailure(t *testing.T, args []string, stdout *bytes.Buffer, message string) {
	t.Helper()
	if err := run(args, stdout); err == nil {
		t.Fatal(message)
	}
}
