package evalcorpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func fixtureCandidate(task, project, fingerprint string, index int) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"candidate_id":        "candidate-" + task,
		"source":              map[string]any{"task_id": task, "markers": []any{map[string]any{"marker": fmt.Sprintf("[index01:%s:%d]", fingerprint, index), "recording_fingerprint": fingerprint, "item_index": index}}},
		"observed_current":    map[string]any{"project_id": project, "title": "Synthetic title " + task, "content": "Task content must not become a transcript", "kind": "TEXT"},
		"input":               map[string]any{"transcript": nil, "transcript_provenance": "unavailable"},
		"review":              map[string]any{"status": "unreviewed", "expected_project_id": ""},
		"historical_delivery": map[string]any{"expected_item_count": 1},
	})
	return data
}

func change(data json.RawMessage, edit func(map[string]any)) json.RawMessage {
	var fields map[string]any
	_ = json.Unmarshal(data, &fields)
	edit(fields)
	result, _ := json.Marshal(fields)
	return result
}

func original(data json.RawMessage, text string) json.RawMessage {
	return change(data, func(m map[string]any) {
		m["input"] = map[string]any{"transcript": text, "transcript_provenance": "original"}
	})
}

func approvedCandidate(data json.RawMessage, project string) json.RawMessage {
	return change(data, func(m map[string]any) {
		m["review"] = map[string]any{"status": "approved", "expected_project_id": project}
	})
}

func fixtureSnapshot(rows ...json.RawMessage) []byte {
	if rows == nil {
		rows = []json.RawMessage{}
	}
	data, _ := json.Marshal(map[string]any{"format_version": 1, "collected_at": "2026-09-08T12:00:00Z", "collection": map[string]any{"failures": []any{}}, "candidates": rows})
	return data
}

func fixtureObservation(data json.RawMessage, at string) sourceObservation {
	c, _ := parseCandidate(data)
	return sourceObservation{TaskID: c.Source.TaskID, ObservedAt: at, Candidate: data}
}

func fixtureEvent(id string, from, to json.RawMessage, fromTime, toTime, status string) sourceEvent {
	data, _ := json.Marshal(map[string]any{"status": status, "basis": "owner_default"})
	c, _ := parseCandidate(to)
	route, _ := json.Marshal(map[string]string{"project_id": c.Current.ProjectID})
	return sourceEvent{ID: id, Type: "observed_move", From: fixtureObservation(from, fromTime), To: fixtureObservation(to, toTime), Review: data, ExpectedRoute: route}
}

func fixtureLedger(rows []json.RawMessage, events []sourceEvent) []byte {
	observations := []sourceObservation{}
	for _, row := range rows {
		observations = append(observations, fixtureObservation(row, "2026-09-08T12:00:00Z"))
	}
	if events == nil {
		events = []sourceEvent{}
	}
	data, _ := json.Marshal(map[string]any{"format_version": 1, "type": "routing_feedback_ledger", "collected_at": "2026-09-08T12:00:00Z", "observations": observations, "events": events, "reviews": map[string]any{}, "findings": []any{}, "incomplete": false, "collection_failures": []any{}})
	return data
}

func routing() *RoutingConfig {
	return &RoutingConfig{Clock: "2026-09-08T12:00:00Z", TimeZone: "America/Chicago", Aliases: map[string]string{"work": "work", "home": "home"}, DefaultProjectID: "default", NoteProjectID: "notes"}
}

func mustImport(t *testing.T, source []byte) Corpus {
	t.Helper()
	c, err := Import(source)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func includes(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestOriginalInputAndExplicitLabels(t *testing.T) {
	fp := strings.Repeat("a", 64)
	row := original(approvedCandidate(fixtureCandidate("one", "observed", fp, 0), "work"), "Synthetic fixture marked as a recovered original")
	source := fixtureSnapshot(row)
	c := mustImport(t, source)
	if c.Examples[0].Targets[0].Label.ProjectID != "work" || c.Examples[0].Targets[0].ObservedProjectID != "observed" {
		t.Fatal("expected and observed projects were mixed")
	}
	if c.Examples[0].Input.Text != "Synthetic fixture marked as a recovered original" || c.Examples[0].Input.Provenance != "original" {
		t.Fatal("original transcript not preserved")
	}
	if len(c.Examples[0].SavedOutput) != 0 {
		t.Fatal("saved output synthesized")
	}
	if !includes(Eligibility(c, c.Examples[0]), "missing_routing_configuration") {
		t.Fatal("missing config not reported")
	}
	c.Routing = routing()
	if blockers := Eligibility(c, c.Examples[0]); len(blockers) != 0 {
		t.Fatal(blockers)
	}
	hash := sha256.Sum256(source)
	if c.Source.SHA256 != hex.EncodeToString(hash[:]) || c.Source.CollectedAt != "2026-09-08T12:00:00Z" {
		t.Fatal("source provenance lost")
	}
	encoded, _ := json.Marshal(c)
	if bytes.Contains(encoded, []byte("[index01:")) || bytes.Contains(encoded, []byte("Task content must not")) {
		t.Fatal("marker or task content leaked into input")
	}
	loaded, err := Load(encoded)
	if err != nil || !reflect.DeepEqual(c, loaded) {
		t.Fatal("roundtrip changed corpus:", err)
	}
}

func TestMissingAndReconstructedInputAreNeverInvented(t *testing.T) {
	for _, provenance := range []string{"unavailable", "reconstructed", "synthetic"} {
		t.Run(provenance, func(t *testing.T) {
			row := approvedCandidate(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0), "home")
			row = change(row, func(m map[string]any) {
				m["input"] = map[string]any{"transcript": "Do not reuse this reconstructed text", "transcript_provenance": provenance}
			})
			c := mustImport(t, fixtureSnapshot(row))
			if c.Examples[0].Input.Text != "" || c.Examples[0].Input.Provenance != "missing" {
				t.Fatal("non-original input imported")
			}
			c.Routing = routing()
			if !includes(Eligibility(c, c.Examples[0]), "missing_original_transcript") {
				t.Fatal("missing original not blocked")
			}
		})
	}
	c := mustImport(t, fixtureSnapshot(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0)))
	if c.Examples[0].Targets[0].Label.Status != "unlabeled" || c.Examples[0].Targets[0].Label.ProjectID != "" {
		t.Fatal("observed project became a label")
	}
	c = mustImport(t, fixtureSnapshot(original(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0), "Synthetic original without a reviewed route")))
	c.Routing = routing()
	if !includes(Eligibility(c, c.Examples[0]), "missing_or_blocked_route_label") {
		t.Fatal("no-move snapshot fabricated a ready routing example")
	}
}

func TestUnreviewedLedgerAnnotationDoesNotHideExplicitCandidateLabel(t *testing.T) {
	row := approvedCandidate(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0), "work")
	data := change(fixtureLedger([]json.RawMessage{row}, nil), func(m map[string]any) {
		m["reviews"] = map[string]any{"one": map[string]any{"status": "unreviewed", "expected_project_id": ""}}
	})
	c := mustImport(t, data)
	if c.Examples[0].Targets[0].Label.ProjectID != "work" {
		t.Fatal("unreviewed ledger annotation hid explicit task label")
	}
	data = change(data, func(m map[string]any) { m["reviews"] = map[string]any{"one": map[string]any{"status": "rejected"}} })
	c = mustImport(t, data)
	if c.Examples[0].Targets[0].Label.Status != "blocked" {
		t.Fatal("explicit ledger rejection did not override stale candidate label")
	}
}

func TestLatestEventByTimeAndExplicitOverrides(t *testing.T) {
	fp := strings.Repeat("a", 64)
	a := original(fixtureCandidate("one", "work", fp, 0), "Synthetic original fixture")
	b := original(fixtureCandidate("one", "home", fp, 0), "Synthetic original fixture")
	c := original(fixtureCandidate("one", "default", fp, 0), "Synthetic original fixture")
	first := fixtureEvent("first", a, b, "2026-09-01T12:00:00Z", "2026-09-02T12:00:00Z", "inferred")
	last := fixtureEvent("last", b, c, "2026-09-02T12:00:00Z", "2026-09-03T12:00:00Z", "inferred")
	corpus := mustImport(t, fixtureLedger([]json.RawMessage{c}, []sourceEvent{last, first}))
	label := corpus.Examples[0].Targets[0].Label
	if label.ProjectID != "default" || label.EventID != "last" || label.Basis != "owner_default" || label.Status != "inferred" {
		t.Fatal("latest event not selected:", label)
	}
	for _, status := range []string{"pending", "rejected"} {
		t.Run(status, func(t *testing.T) {
			last.Review = json.RawMessage(fmt.Sprintf(`{"status":%q}`, status))
			corpus := mustImport(t, fixtureLedger([]json.RawMessage{c}, []sourceEvent{last, first}))
			label := corpus.Examples[0].Targets[0].Label
			if label.Status != "blocked" || label.ProjectID != "" || label.EventID != "last" {
				t.Fatal("blocked latest event fell back to earlier label")
			}
			corpus = mustImport(t, fixtureLedger([]json.RawMessage{approvedCandidate(c, "home")}, []sourceEvent{last, first}))
			if label := corpus.Examples[0].Targets[0].Label; label.ProjectID != "home" || label.Status != "approved" || label.Basis != "candidate_review" {
				t.Fatal("explicit task label did not override event")
			}
		})
	}
	last.Review = json.RawMessage(`{"status":"approved"}`)
	last.ExpectedRoute = json.RawMessage(`{"project_id":"work"}`)
	corpus = mustImport(t, fixtureLedger([]json.RawMessage{c}, []sourceEvent{last, first}))
	if label := corpus.Examples[0].Targets[0].Label; label.ProjectID != "work" || label.Basis != "event_review" {
		t.Fatal("approved independent expected route ignored")
	}
}

func TestRecordingSiblingsShareInputAndSplit(t *testing.T) {
	fp := strings.Repeat("a", 64)
	first := original(approvedCandidate(fixtureCandidate("first", "home", fp, 0), "home"), "Two synthetic items")
	second := approvedCandidate(fixtureCandidate("second", "work", fp, 1), "work")
	first = change(first, func(m map[string]any) { m["historical_delivery"].(map[string]any)["expected_item_count"] = 2 })
	second = change(second, func(m map[string]any) { m["historical_delivery"].(map[string]any)["expected_item_count"] = 2 })
	other := approvedCandidate(fixtureCandidate("other", "work", strings.Repeat("b", 64), 0), "work")
	a := mustImport(t, fixtureSnapshot(second, other, first))
	b := mustImport(t, fixtureSnapshot(first, second, other))
	if len(a.Examples) != 2 || len(a.Examples[0].Targets) != 2 || a.Examples[0].Targets[0].TaskID != "first" || a.Examples[0].Targets[1].TaskID != "second" {
		t.Fatal("recording siblings were not grouped and sorted")
	}
	if !reflect.DeepEqual(a.Examples, b.Examples) {
		t.Fatal("source order changed grouped examples or split")
	}
	a.Routing = routing()
	if len(Eligibility(a, a.Examples[0])) != 0 {
		t.Fatal("sibling original did not supply recording input")
	}
	conflicting := original(second, "Different synthetic original")
	if _, err := Import(fixtureSnapshot(first, conflicting)); err == nil {
		t.Fatal("conflicting originals were merged")
	}
}

func TestRecordingCompletenessNeedsIndependentExpectedCount(t *testing.T) {
	fp := strings.Repeat("a", 64)
	row := original(approvedCandidate(fixtureCandidate("one", "home", fp, 0), "home"), "Synthetic two-item original")
	unknown := change(row, func(m map[string]any) { delete(m, "historical_delivery") })
	c := mustImport(t, fixtureSnapshot(unknown))
	c.Routing = routing()
	if c.Examples[0].ExpectedItemCount != nil || !includes(Eligibility(c, c.Examples[0]), "unknown_recording_completeness") {
		t.Fatal("observed item count invented recording completeness")
	}
	first := change(row, func(m map[string]any) { m["historical_delivery"].(map[string]any)["expected_item_count"] = 2 })
	c = mustImport(t, fixtureSnapshot(first))
	c.Routing = routing()
	if !includes(Eligibility(c, c.Examples[0]), "recording_item_count_mismatch") {
		t.Fatal("missing trailing sibling was eligible")
	}
	second := approvedCandidate(fixtureCandidate("two", "work", fp, 1), "work")
	second = change(second, func(m map[string]any) { m["historical_delivery"].(map[string]any)["expected_item_count"] = 2 })
	c = mustImport(t, fixtureSnapshot(first, second))
	c.Routing = routing()
	if blockers := Eligibility(c, c.Examples[0]); len(blockers) != 0 {
		t.Fatal("complete explicit recording blocked:", blockers)
	}
	conflict := change(second, func(m map[string]any) { m["historical_delivery"].(map[string]any)["expected_item_count"] = 3 })
	if _, err := Import(fixtureSnapshot(first, conflict)); err == nil {
		t.Fatal("conflicting recording counts merged")
	}
	invalid := change(row, func(m map[string]any) { m["historical_delivery"].(map[string]any)["expected_item_count"] = 0 })
	if _, err := Import(fixtureSnapshot(invalid)); err == nil {
		t.Fatal("zero recording count accepted")
	}
}

func TestSourceFailuresAndFindingsBlockReadiness(t *testing.T) {
	row := original(approvedCandidate(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0), "home"), "Synthetic original")
	for _, status := range []string{"requires_review", "not_observed", "incomplete"} {
		data := change(fixtureLedger([]json.RawMessage{row}, nil), func(m map[string]any) { m["findings"] = []any{map[string]any{"task_id": "one", "status": status}} })
		c := mustImport(t, data)
		c.Routing = routing()
		if !includes(Eligibility(c, c.Examples[0]), "source_"+status) {
			t.Fatal("source finding did not block example")
		}
	}
	data := change(fixtureLedger([]json.RawMessage{row}, nil), func(m map[string]any) { m["incomplete"] = true })
	c := mustImport(t, data)
	c.Routing = routing()
	if !includes(Eligibility(c, c.Examples[0]), "source_collection_incomplete") {
		t.Fatal("whole ledger incomplete was ignored")
	}
	data = change(fixtureSnapshot(row), func(m map[string]any) { m["collection"].(map[string]any)["failures"] = []any{"synthetic failure"} })
	c = mustImport(t, data)
	c.Routing = routing()
	if !includes(Eligibility(c, c.Examples[0]), "source_collection_incomplete") {
		t.Fatal("snapshot failure was ignored")
	}
}

func TestAmbiguousSourceIdentityIsRejected(t *testing.T) {
	row := fixtureCandidate("one", "home", strings.Repeat("a", 64), 0)
	otherTaskSameMarker := fixtureCandidate("two", "home", strings.Repeat("a", 64), 0)
	multiple := change(row, func(m map[string]any) {
		s := m["source"].(map[string]any)
		markers := s["markers"].([]any)
		s["markers"] = append(markers, markers[0])
	})
	invalidMarker := change(row, func(m map[string]any) {
		m["source"].(map[string]any)["markers"].([]any)[0].(map[string]any)["item_index"] = 1
	})
	for name, data := range map[string][]byte{"duplicate task": fixtureSnapshot(row, row), "duplicate marker and index": fixtureSnapshot(row, otherTaskSameMarker), "multiple markers": fixtureSnapshot(multiple), "mismatched marker": fixtureSnapshot(invalidMarker), "empty": fixtureSnapshot()} {
		t.Run(name, func(t *testing.T) {
			if _, err := Import(data); err == nil {
				t.Fatal("ambiguous source accepted")
			}
		})
	}
	to := fixtureCandidate("one", "work", strings.Repeat("a", 64), 0)
	e := fixtureEvent("event", row, to, "2026-09-01T12:00:00Z", "2026-09-02T12:00:00Z", "inferred")
	e.From.TaskID = "another-task"
	if _, err := Import(fixtureLedger([]json.RawMessage{to}, []sourceEvent{e})); err == nil {
		t.Fatal("conflicting move identity accepted")
	}
	e = fixtureEvent("event", row, to, "2026-09-01T12:00:00Z", "2026-09-02T12:00:00Z", "inferred")
	other := e
	other.ID = "other"
	if _, err := Import(fixtureLedger([]json.RawMessage{to}, []sourceEvent{e, other})); err == nil {
		t.Fatal("simultaneous move labels accepted")
	}
}

func TestEligibilityChecksMatchingConfigurationAndIndices(t *testing.T) {
	fp := strings.Repeat("a", 64)
	row := original(approvedCandidate(fixtureCandidate("one", "home", fp, 0), "unmapped"), "Synthetic original")
	c := mustImport(t, fixtureSnapshot(row))
	c.Routing = routing()
	if !includes(Eligibility(c, c.Examples[0]), "unsupported_target_project") {
		t.Fatal("unmapped label accepted")
	}
	c.Examples[0].Targets[0].Label.ProjectID = "home"
	c.Examples[0].Targets[0].ItemIndex = 1
	if !includes(Eligibility(c, c.Examples[0]), "noncontiguous_item_indices") {
		t.Fatal("unknown preceding item accepted")
	}
	first := c.Examples[0].Targets[0]
	first.ItemIndex = 0
	second := first
	second.TaskID = "two"
	second.ItemIndex = 1
	second.Title = "  SYNTHETIC   TITLE one "
	c.Examples[0].Targets = []Target{first, second}
	if !includes(Eligibility(c, c.Examples[0]), "ambiguous_target_title_and_kind") {
		t.Fatal("ambiguous title matching accepted")
	}
}

func TestStrictCorpusAndRoutingValidation(t *testing.T) {
	c := mustImport(t, fixtureSnapshot(approvedCandidate(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0), "home")))
	c.Routing = routing()
	data, _ := json.Marshal(c)
	cases := map[string]json.RawMessage{
		"unknown field": change(data, func(m map[string]any) { m["extra"] = true }),
		"version":       change(data, func(m map[string]any) { m["format_version"] = 2 }),
		"empty":         change(data, func(m map[string]any) { m["examples"] = []any{} }),
		"missing index": change(data, func(m map[string]any) {
			delete(m["examples"].([]any)[0].(map[string]any)["targets"].([]any)[0].(map[string]any), "item_index")
		}),
		"null blockers": change(data, func(m map[string]any) { m["examples"].([]any)[0].(map[string]any)["blockers"] = nil }),
		"invalid label": change(data, func(m map[string]any) {
			m["examples"].([]any)[0].(map[string]any)["targets"].([]any)[0].(map[string]any)["label"].(map[string]any)["status"] = "magic"
		}),
		"invalid SHA":          change(data, func(m map[string]any) { m["source"].(map[string]any)["sha256"] = "bad" }),
		"invalid saved output": change(data, func(m map[string]any) { m["examples"].([]any)[0].(map[string]any)["saved_output"] = []any{} }),
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(bad); err == nil {
				t.Fatal("invalid corpus accepted")
			}
		})
	}
	for name, edit := range map[string]func(*RoutingConfig){"zone": func(r *RoutingConfig) { r.TimeZone = "Invalid/Zone" }, "clock": func(r *RoutingConfig) { r.Clock = "tomorrow" }, "aliases": func(r *RoutingConfig) { r.Aliases = nil }, "project": func(r *RoutingConfig) { r.NoteProjectID = "" }, "duplicate alias": func(r *RoutingConfig) { r.Aliases["WORK"] = "different" }} {
		t.Run(name, func(t *testing.T) {
			r := routing()
			edit(r)
			data, _ := json.Marshal(r)
			if _, err := DecodeRouting(data); err == nil {
				t.Fatal("invalid routing config accepted")
			}
		})
	}
	c.Source.Kind = "synthetic"
	c.Examples[0].Input = Input{Text: "Synthetic hand-authored input", Provenance: "synthetic"}
	c.Examples[0].SavedOutput = json.RawMessage(`{"items":[]}`)
	data, _ = json.Marshal(c)
	if _, err := Load(data); err != nil {
		t.Fatal("declared synthetic corpus rejected:", err)
	}
	if len(Eligibility(c, c.Examples[0])) != 0 {
		t.Fatal("valid synthetic fixture blocked")
	}
	c.Examples[0].Input.Provenance = "reconstructed"
	if !includes(Eligibility(c, c.Examples[0]), "original_transcript_required") {
		t.Fatal("reconstructed input eligible")
	}
}

func TestImportDeduplicatesUnresolvedTaskBlockers(t *testing.T) {
	row := fixtureCandidate("one", "home", strings.Repeat("a", 64), 0)
	data := change(fixtureLedger([]json.RawMessage{row}, nil), func(m map[string]any) {
		m["findings"] = []any{
			map[string]any{"task_id": "missing-one", "status": "not_observed"},
			map[string]any{"task_id": "missing-two", "status": "not_observed"},
		}
	})
	c := mustImport(t, data)
	count := 0
	for _, blocker := range c.Examples[0].Blockers {
		if blocker == "source_unresolved_task_identity" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("unresolved identity blocker count=%d", count)
	}
}

func TestImportRecordingContext(t *testing.T) {
	row := original(approvedCandidate(fixtureCandidate("one", "home", strings.Repeat("a", 64), 0), "home"), "Synthetic original")
	row = change(row, func(m map[string]any) {
		input := m["input"].(map[string]any)
		input["routing"] = routing()
		input["saved_output"] = json.RawMessage(`{"items":[]}`)
		input["prompt_sha256"] = strings.Repeat("c", 64)
		input["model"] = "recorded-model"
	})
	c := mustImport(t, fixtureSnapshot(row))
	e := c.Examples[0]
	if c.Routing != nil || !reflect.DeepEqual(e.Routing, routing()) || len(Eligibility(c, e)) != 0 {
		t.Fatal("recording context was not sufficient for eligibility")
	}
	if string(e.SavedOutput) != `{"items":[]}` || e.Input.Model != "recorded-model" || e.Input.PromptSHA256 != strings.Repeat("c", 64) {
		t.Fatal("independent output or model provenance was lost")
	}
	encoded, _ := json.Marshal(c)
	loaded, err := Load(encoded)
	if err != nil || !reflect.DeepEqual(c, loaded) {
		t.Fatal("recording context did not roundtrip", err)
	}
	c.Routing = routing()
	c.Routing.Aliases["home"] = "wrong-project"
	if EffectiveRouting(c, e) != e.Routing || len(Eligibility(c, e)) != 0 {
		t.Fatal("global context replaced recording context")
	}
	e.Routing = nil
	if EffectiveRouting(c, e) != c.Routing || !includes(Eligibility(c, e), "unsupported_target_project") {
		t.Fatal("global fallback ignored")
	}
	c.Examples[0].Routing.Clock = "invalid"
	if Validate(c) == nil || !includes(Eligibility(c, c.Examples[0]), "invalid_routing_configuration") {
		t.Fatal("invalid recording context accepted")
	}
}

func TestImportRejectsConflictingSiblingContext(t *testing.T) {
	makeRow := func(task string, index int) json.RawMessage {
		row := original(approvedCandidate(fixtureCandidate(task, "home", strings.Repeat("a", 64), index), "home"), "Synthetic original")
		return change(row, func(m map[string]any) {
			m["historical_delivery"].(map[string]any)["expected_item_count"] = 2
			input := m["input"].(map[string]any)
			input["routing"] = routing()
			input["saved_output"] = json.RawMessage(`{"items":[],"recorded":true}`)
			input["prompt_sha256"] = strings.Repeat("c", 64)
			input["model"] = "recorded-model"
		})
	}
	first, second := makeRow("one", 0), makeRow("two", 1)
	for name, edit := range map[string]func(map[string]any){
		"routing": func(m map[string]any) { m["routing"].(map[string]any)["clock"] = "2026-09-09T12:00:00Z" },
		"output":  func(m map[string]any) { m["saved_output"] = map[string]any{"items": []any{}, "recorded": false} },
		"prompt":  func(m map[string]any) { m["prompt_sha256"] = strings.Repeat("d", 64) },
		"model":   func(m map[string]any) { m["model"] = "other-model" },
	} {
		t.Run(name, func(t *testing.T) {
			conflict := change(second, func(m map[string]any) { edit(m["input"].(map[string]any)) })
			for _, rows := range [][]json.RawMessage{{first, conflict}, {conflict, first}} {
				if _, err := Import(fixtureSnapshot(rows...)); err == nil {
					t.Fatal("conflicting sibling evidence accepted")
				}
			}
		})
	}
	second = change(second, func(m map[string]any) {
		m["input"].(map[string]any)["saved_output"] = json.RawMessage(`{ "recorded": true, "items": [] }`)
	})
	c := mustImport(t, fixtureSnapshot(first, second))
	if len(c.Examples) != 1 || len(Eligibility(c, c.Examples[0])) != 0 {
		t.Fatal("consistent siblings were rejected")
	}
	second = change(second, func(m map[string]any) { delete(m, "input") })
	c = mustImport(t, fixtureSnapshot(second, first))
	if c.Examples[0].Routing == nil || len(c.Examples[0].SavedOutput) == 0 {
		t.Fatal("missing sibling evidence erased preserved context")
	}
}
