package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauern/index-01-hook/internal/evalcorpus"
)

func exportEvidenceFixture(t *testing.T) (EvaluationEvidence, time.Time) {
	t.Helper()
	fixture, _ := loadFeedbackFixture(t)
	example := fixture.Examples[0]
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	return EvaluationEvidence{
		Fingerprint: example.RecordingFingerprint, Transcript: example.Input.Text, RecordedAtMillis: now.Add(-2 * time.Hour).UnixMilli(),
		CapturedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(90 * 24 * time.Hour),
		Extraction: &FrozenExtraction{Provider: "deepseek", Model: "test-model", Items: []QueuedItem{{Kind: ItemKindTask, Title: example.Targets[0].Title}},
			Evidence: &ExtractionEvidence{Routing: fixture.Routing, SavedOutput: example.SavedOutput, PromptSHA256: evidenceHash([]byte("captured-prompt"))}},
		Deliveries: []EvaluationDelivery{{ItemIndex: 0, TaskID: "task-1", ProjectID: "project-home", Kind: ItemKindTask, Title: example.Targets[0].Title,
			Observation: &EvaluationObservation{TaskID: "task-1", ProjectID: "project-work", Marker: evaluationMarker(example.RecordingFingerprint, 0),
				Kind: "TEXT", Title: "Owner edited this title", ObservedAt: now.Add(-time.Hour), Status: "verified"}}},
	}, now
}

func importEvaluationExport(t *testing.T, recording EvaluationEvidence, now time.Time) (evalcorpus.Corpus, evaluationExportStatus) {
	t.Helper()
	ledger, status, err := buildEvaluationExport([]EvaluationEvidence{recording}, now)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := evalcorpus.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	return corpus, status
}

func TestEvaluationExportJoinsShadowDecisionWithDeliveryObservation(t *testing.T) {
	recording, now := exportEvidenceFixture(t)
	recording.Deliveries[0].Shadow = &ShadowVerification{ItemIndex: 0, Decision: "review", Outcome: string(OutcomeCreated), PromptVersion: typeSafeVerificationPromptVersion}
	ledger, _, err := buildEvaluationExport([]EvaluationEvidence{recording}, now)
	if err != nil || len(ledger.Findings) != 1 {
		t.Fatalf("ledger = %+v, error = %v", ledger, err)
	}
	finding := ledger.Findings[0]
	if finding.ShadowDecision != "review" || finding.ShadowOutcome != string(OutcomeCreated) || finding.Status != "observed_move" {
		t.Fatalf("shadow finding = %+v", finding)
	}
}

func TestEvaluationExportMoveImportsAndRejectsOldPrediction(t *testing.T) {
	recording, now := exportEvidenceFixture(t)
	corpus, status := importEvaluationExport(t, recording, now)
	if status.MoveLabels != 1 || status.EligibleExamples != 1 || status.ImportedExamples != 1 {
		t.Fatalf("status = %+v", status)
	}
	example := &corpus.Examples[0]
	if example.Targets[0].Label.ProjectID != "project-work" || example.Targets[0].Label.Status != "inferred" {
		t.Fatalf("label = %+v", example.Targets[0].Label)
	}
	if example.Targets[0].Title != recording.Deliveries[0].Title || example.Targets[0].Title == recording.Deliveries[0].Observation.Title {
		t.Fatal("manual title edit changed the historical prediction identity")
	}
	if example.Input.Model != "test-model" || example.Input.PromptSHA256 != evidenceHash([]byte("captured-prompt")) || example.Routing == nil {
		t.Fatal("export dropped the captured extraction context")
	}
	data, _ := json.Marshal(corpus)
	report, err := runFeedbackCorpus(corpus, data, "replay", nil)
	if err == nil || report.Counts["fail"] != 1 || report.Counts["error"] != 0 {
		t.Fatalf("old prediction: counts=%v err=%v", report.Counts, err)
	}
	example.SavedOutput = json.RawMessage(strings.ReplaceAll(string(example.SavedOutput), `"home"`, `"work"`))
	data, _ = json.Marshal(corpus)
	report, err = runFeedbackCorpus(corpus, data, "replay", nil)
	if err != nil || report.Counts["pass"] != 1 {
		t.Fatalf("corrected prediction: counts=%v err=%v", report.Counts, err)
	}
}

func TestEvaluationExportMoveIDDoesNotChangeOnLaterPoll(t *testing.T) {
	recording, now := exportEvidenceFixture(t)
	first, _, err := buildEvaluationExport([]EvaluationEvidence{recording}, now)
	if err != nil {
		t.Fatal(err)
	}
	recording.Deliveries[0].Observation.ObservedAt = now.Add(-time.Minute)
	later, _, err := buildEvaluationExport([]EvaluationEvidence{recording}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || len(later.Events) != 1 || first.Events[0].ID != later.Events[0].ID {
		t.Fatal("later poll changed the move event identifier")
	}
}

func TestEvaluationExportUnchangedAndUnresolvedRemainUnlabeled(t *testing.T) {
	for _, state := range []string{"unchanged", "missing", "wrong_marker", "changed_kind", "future", "no_observation"} {
		t.Run(state, func(t *testing.T) {
			recording, now := exportEvidenceFixture(t)
			observation := recording.Deliveries[0].Observation
			switch state {
			case "unchanged":
				observation.ProjectID = recording.Deliveries[0].ProjectID
			case "missing":
				observation.Status, observation.Marker, observation.ProjectID = "missing", "", ""
			case "wrong_marker":
				observation.Marker = ""
			case "changed_kind":
				observation.Kind = "NOTE"
			case "future":
				observation.ObservedAt = now.Add(time.Minute)
			case "no_observation":
				recording.Deliveries[0].Observation = nil
			}
			corpus, status := importEvaluationExport(t, recording, now)
			if status.MoveLabels != 0 || status.EligibleExamples != 0 || corpus.Examples[0].Targets[0].Label.Status != "unlabeled" {
				t.Fatalf("unsupported route became a label: status=%+v label=%+v", status, corpus.Examples[0].Targets[0].Label)
			}
		})
	}
}

func TestEvaluationExportNormalizesInboxWithoutChangingArchive(t *testing.T) {
	recording, now := exportEvidenceFixture(t)
	recording.Deliveries[0].ProjectID = "inbox000000000"
	recording.Deliveries[0].Observation.ProjectID = "inbox"
	recording.Extraction.Evidence.Routing.DefaultProjectID = "inbox000000000"
	corpus, status := importEvaluationExport(t, recording, now)
	if status.MoveLabels != 0 || corpus.Examples[0].Targets[0].ObservedProjectID != "inbox" || corpus.Examples[0].Routing.DefaultProjectID != "inbox" {
		t.Fatal("virtual and physical Inbox produced different routes")
	}
	if recording.Extraction.Evidence.Routing.DefaultProjectID != "inbox000000000" || recording.Deliveries[0].ProjectID != "inbox000000000" {
		t.Fatal("normalization changed archived original evidence")
	}
	if canonicalEvaluationProject("inbox-other") != "inbox-other" {
		t.Fatal("normalization changed an unrelated project")
	}
}

func TestEvaluationExportPreservesInputOnlyAndZeroItemArchive(t *testing.T) {
	recording, now := exportEvidenceFixture(t)
	zero := recording
	zero.Fingerprint = queueTestHash
	zero.Extraction = &FrozenExtraction{Provider: "deepseek", Model: "model", Items: []QueuedItem{}}
	zero.Deliveries = nil
	recording.Extraction, recording.Deliveries = nil, nil
	ledger, status, err := buildEvaluationExport([]EvaluationEvidence{recording, zero}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Archive) != 2 || len(ledger.Observations) != 0 || status.OriginalInputs != 2 || status.ZeroItemExtractions != 1 || status.EligibleExamples != 0 {
		t.Fatalf("input-only archive status = %+v", status)
	}
}

func TestEvaluationExportCreatesPrivateFileAndRefusesOverwrite(t *testing.T) {
	recording, now := exportEvidenceFixture(t)
	ledger, _, err := buildEvaluationExport([]EvaluationEvidence{recording}, now)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private", "evidence.json")
	if err := writeEvaluationExport(path, ledger); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{path, 0o600}, {filepath.Dir(path), 0o700}} {
		info, err := os.Stat(entry.path)
		if err != nil || info.Mode().Perm() != entry.mode {
			t.Fatalf("private path mode for %s: %v, %v", entry.path, info, err)
		}
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEvaluationExport(path, evaluationExportLedger{}); err == nil {
		t.Fatal("export overwrote an existing file")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("failed export changed the existing file")
	}
	if err := writeEvaluationExport("-", ledger); err == nil {
		t.Fatal("export accepted standard output")
	}
}

func TestEvaluationOperatorStatusAndExportAreReadOnlyAndRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "database.db")
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ConfigureEvaluationCapture(24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	saveQueueRecording(t, store, "Private original transcript")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	getenv := func(key string) string {
		if key == "INDEX01_DB_PATH" {
			return path
		}
		return ""
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, args := range [][]string{{"evaluation-status"}, {"evaluation-export", filepath.Join(t.TempDir(), "private.json")}} {
		var output bytes.Buffer
		if err := execute(logger, args, getenv, &output); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var status evaluationExportStatus
		if err := json.Unmarshal(output.Bytes(), &status); err != nil || status.OriginalInputs != 1 {
			t.Fatalf("aggregate output = %s, %v", output.String(), err)
		}
		if strings.Contains(output.String(), "Private original transcript") || strings.Contains(output.String(), queueTestHash) {
			t.Fatal("operator output disclosed private evidence")
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("status or export changed the database")
	}
}
