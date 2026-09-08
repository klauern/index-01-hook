package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func row(id, project string) json.RawMessage {
	fingerprint := strings.Repeat("a", 64)
	if id == "two" {
		fingerprint = strings.Repeat("b", 64)
	}
	b, _ := json.Marshal(map[string]any{
		"candidate_id":     "candidate-" + id,
		"source":           map[string]any{"task_id": id, "markers": []any{map[string]any{"marker": "[index01:" + fingerprint + ":0]", "recording_fingerprint": fingerprint, "item_index": 0}}},
		"observed_current": map[string]any{"project_id": project, "project_name": "Private project", "kind": "TEXT", "title": "Private title", "content": "Private content"},
		"input":            map[string]any{"transcript": nil, "transcript_provenance": "missing"},
		"review":           map[string]any{"status": "unreviewed", "expected_project_id": ""},
	})
	return b
}

func edit(raw json.RawMessage, change func(map[string]any)) json.RawMessage {
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	change(fields)
	b, _ := json.Marshal(fields)
	return b
}

func snap(day string, rows ...json.RawMessage) []byte {
	if rows == nil {
		rows = []json.RawMessage{}
	}
	b, _ := json.Marshal(map[string]any{"format_version": 1, "collected_at": "2026-09-" + day + "T12:00:00Z", "collection": map[string]any{"failures": []any{}}, "candidates": rows})
	return b
}

func compare(t *testing.T, prev, current []byte) ledger {
	t.Helper()
	p, err := parsePrevious(prev)
	if err != nil {
		t.Fatal(err)
	}
	c, err := parseSnapshot(current)
	if err != nil {
		t.Fatal(err)
	}
	d, err := digest(current)
	if err != nil {
		t.Fatal(err)
	}
	l, err := reconcile(p, c, d)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func encoded(t *testing.T, l ledger) []byte {
	t.Helper()
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMovesAndReplay(t *testing.T) {
	a := snap("01", row("one", "A"))
	b := snap("02", row("one", "B"))
	l := compare(t, a, b)
	if len(l.Events) != 1 || l.Findings[0].Status != "observed_move" {
		t.Fatalf("unexpected ledger: %+v", l)
	}
	e := l.Events[0]
	var route map[string]string
	var review map[string]string
	if err := json.Unmarshal(e.ExpectedRoute, &route); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(e.Review, &review); err != nil {
		t.Fatal(err)
	}
	if route["project_id"] != "B" || review["status"] != "inferred" || review["basis"] != "owner_default" || review["interpretation"] != "original_routing_error" {
		t.Fatal("move did not apply the owner routing preference")
	}
	if e.Actor != "unknown" || !bytes.Contains(e.To.Candidate, []byte("Private title")) {
		t.Fatal("policy inference must preserve context without claiming a known actor")
	}
	l.Events[0].Review = json.RawMessage(`{"status":"accepted","note":"Owner reviewed"}`)
	l.Reviews["one"] = json.RawMessage(`{"status":"accepted","expected_project_id":"B"}`)
	replayed := compare(t, encoded(t, l), b)
	if !reflect.DeepEqual(l, replayed) {
		t.Fatal("same snapshot changed ledger")
	}
	c := snap("03", row("one", "B"))
	l = compare(t, encoded(t, l), c)
	if len(l.Events) != 1 {
		t.Fatal("unchanged route created an event")
	}
	l = compare(t, encoded(t, l), snap("04", row("one", "A")))
	l = compare(t, encoded(t, l), snap("05", row("one", "B")))
	if len(l.Events) != 3 {
		t.Fatal("return moves were lost")
	}
	if !bytes.Contains(l.Events[0].Review, []byte("accepted")) || !bytes.Contains(l.Reviews["one"], []byte("accepted")) {
		t.Fatal("review annotations lost")
	}
	if l.Events[0].ID == l.Events[2].ID {
		t.Fatal("distinct A to B moves share an event ID")
	}
	if _, err := parsePrevious(encoded(t, l)); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingPreferenceOverrideSurvivesLaterPolls(t *testing.T) {
	l := compare(t, snap("01", row("one", "A")), snap("02", row("one", "B")))
	// A temporary reorganization is an exception to the owner default.
	l.Events[0].Review = json.RawMessage(`{"status":"rejected","reason":"Temporary reorganization"}`)
	l.Events[0].ExpectedRoute = json.RawMessage(`{"project_id":"A"}`)
	l = compare(t, encoded(t, l), snap("03", row("one", "B")))
	l = compare(t, encoded(t, l), snap("04", row("one", "C")))
	if len(l.Events) != 2 || !bytes.Contains(l.Events[0].Review, []byte("rejected")) || string(l.Events[0].ExpectedRoute) != `{"project_id":"A"}` {
		t.Fatal("later polling replaced an explicit exception")
	}
	if string(l.Events[1].ExpectedRoute) != `{"project_id":"C"}` || !bytes.Contains(l.Events[1].Review, []byte("owner_default")) {
		t.Fatal("new move did not receive its own preferred destination")
	}
}

func TestProjectRenameAndMissing(t *testing.T) {
	r := edit(row("one", "A"), func(m map[string]any) { m["observed_current"].(map[string]any)["project_name"] = "Renamed" })
	l := compare(t, snap("01", row("one", "A")), snap("02", r))
	if len(l.Events) != 0 || l.Findings[0].Status != "unchanged" {
		t.Fatal("rename treated as move")
	}
	l = compare(t, encoded(t, l), snap("03"))
	if len(l.Observations) != 1 || l.Findings[0].Status != "not_observed" {
		t.Fatal("missing observation discarded")
	}
	l = compare(t, encoded(t, l), snap("04", row("one", "B")))
	if len(l.Events) != 1 || l.Events[0].From.ObservedAt != "2026-09-02T12:00:00Z" {
		t.Fatal("move must start from last actual observation")
	}
}

func TestAmbiguityDoesNotAdvanceTrustedState(t *testing.T) {
	base := row("one", "A")
	kind := edit(row("one", "B"), func(m map[string]any) { m["observed_current"].(map[string]any)["kind"] = "NOTE" })
	changed := edit(row("two", "B"), func(m map[string]any) {
		m["candidate_id"] = "candidate-one"
		m["source"].(map[string]any)["task_id"] = "one"
	})
	otherID := edit(row("one", "B"), func(m map[string]any) { m["candidate_id"] = "other"; m["source"].(map[string]any)["task_id"] = "other" })
	duplicateCandidate := edit(row("two", "B"), func(m map[string]any) { m["candidate_id"] = "candidate-one" })
	multiple := edit(row("one", "B"), func(m map[string]any) {
		s := m["source"].(map[string]any)
		ms := s["markers"].([]any)
		s["markers"] = append(ms, ms[0])
	})
	cases := map[string][]json.RawMessage{
		"duplicate task":                {row("one", "B"), row("one", "C")},
		"duplicate marker across tasks": {row("one", "B"), otherID},
		"different task same marker":    {otherID},
		"duplicate candidate ID":        {row("one", "B"), duplicateCandidate},
		"kind change":                   {kind}, "marker change": {changed}, "multiple markers": {multiple},
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			l := compare(t, snap("01", base), snap("02", rows...))
			if len(l.Events) != 0 || len(l.Observations) != 1 || l.Observations[0].ObservedAt != "2026-09-01T12:00:00Z" {
				t.Fatal("ambiguous observation advanced trusted state")
			}
			found := false
			for _, f := range l.Findings {
				if f.Status == "requires_review" {
					found = true
					if len(f.Candidates) == 0 {
						t.Fatal("review context missing")
					}
				}
			}
			if !found {
				t.Fatal("review finding missing")
			}
		})
	}
}

func TestIncompleteCollectionFreezesObservations(t *testing.T) {
	current := edit(snap("02", row("one", "B"), row("two", "C")), func(m map[string]any) {
		m["collection"].(map[string]any)["failures"] = []any{map[string]any{"project_id": "unavailable", "error": "private failure"}}
	})
	l := compare(t, snap("01", row("one", "A")), current)
	if !l.Incomplete || len(l.Events) != 0 || len(l.Observations) != 1 || l.Observations[0].ObservedAt != "2026-09-01T12:00:00Z" || len(l.Failures) != 1 {
		t.Fatal("incomplete collection advanced state")
	}
	for _, f := range l.Findings {
		if f.Status != "incomplete" {
			t.Fatal("incomplete status missing")
		}
	}
	l = compare(t, encoded(t, l), snap("03", row("one", "B")))
	if len(l.Events) != 1 {
		t.Fatal("recovered collection did not detect move")
	}
}

func TestInvalidSnapshots(t *testing.T) {
	valid := snap("01", row("one", "A"))
	cases := map[string][]byte{"bad JSON": []byte("{"), "bad version": edit(valid, func(m map[string]any) { m["format_version"] = 2 }), "bad time": edit(valid, func(m map[string]any) { m["collected_at"] = "yesterday" }), "missing collection": edit(valid, func(m map[string]any) { delete(m, "collection") }), "missing failures": edit(valid, func(m map[string]any) { m["collection"] = map[string]any{} }), "missing candidates": edit(valid, func(m map[string]any) { delete(m, "candidates") })}
	for _, field := range []string{"candidate_id", "task_id", "project_id", "kind", "markers", "item_index", "recording_fingerprint", "marker", "modified_at"} {
		bad := edit(row("one", "A"), func(m map[string]any) {
			s := m["source"].(map[string]any)
			c := m["observed_current"].(map[string]any)
			switch field {
			case "candidate_id":
				delete(m, field)
			case "task_id", "markers":
				delete(s, field)
			case "project_id", "kind":
				delete(c, field)
			case "modified_at":
				s[field] = "bad time"
			default:
				delete(s["markers"].([]any)[0].(map[string]any), field)
			}
		})
		cases[field] = snap("01", bad)
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSnapshot(data); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
}

func TestChronology(t *testing.T) {
	p, _ := parsePrevious(snap("02", row("one", "A")))
	for _, data := range [][]byte{snap("01", row("one", "B")), snap("02", row("one", "B"))} {
		c, _ := parseSnapshot(data)
		d, _ := digest(data)
		if _, err := reconcile(p, c, d); err == nil {
			t.Fatal("non-new observation accepted")
		}
	}
	l := compare(t, snap("01", row("one", "A")), snap("02", row("one", "B")))
	l.Observations[0].ObservedAt = "2026-09-03T12:00:00Z"
	if _, err := parsePrevious(encoded(t, l)); err == nil {
		t.Fatal("future trusted observation accepted")
	}
}

func TestSourceTimestampFormats(t *testing.T) {
	for _, timestamp := range []any{"2026-08-16T16:09:39+0000", "2026-08-16T16:09:39.123+0000", "2026-08-16T16:09:39Z", "2026-08-16T16:09:39.123-05:00", nil} {
		r := edit(row("one", "A"), func(m map[string]any) {
			s := m["source"].(map[string]any)
			s["created_at"] = timestamp
			s["modified_at"] = timestamp
		})
		if _, err := parseSnapshot(snap("01", r)); err != nil {
			t.Fatalf("source time %v: %v", timestamp, err)
		}
	}
}

func TestStaleProviderReadDoesNotCreateReverseMove(t *testing.T) {
	withModified := func(project, stamp string) json.RawMessage {
		return edit(row("one", project), func(m map[string]any) { m["source"].(map[string]any)["modified_at"] = stamp })
	}
	a := withModified("A", "2026-08-16T16:09:39+0000")
	b := withModified("B", "2026-08-17T16:09:39Z")
	l := compare(t, snap("01", a), snap("02", b))
	l = compare(t, encoded(t, l), snap("03", a))
	if len(l.Events) != 1 || l.Findings[0].Status != "requires_review" || l.Observations[0].ObservedAt != "2026-09-02T12:00:00Z" {
		t.Fatal("stale provider observation advanced state")
	}
	for name, stamp := range map[string]string{"equal": "2026-08-17T16:09:39+0000", "unknown": ""} {
		t.Run(name, func(t *testing.T) {
			next := compare(t, encoded(t, l), snap("04", withModified("A", stamp)))
			if len(next.Events) != 2 {
				t.Fatal("equal or unknown source time prevented move observation")
			}
		})
	}
}

func TestModificationBoundSurvivesMissingPolls(t *testing.T) {
	withModified := func(project string, stamp any) json.RawMessage {
		return edit(row("one", project), func(m map[string]any) { m["source"].(map[string]any)["modified_at"] = stamp })
	}
	a := withModified("Work", "2026-09-01T10:00:00+0000")
	b := withModified("Home", "2026-09-02T10:00:00+0000")
	l := compare(t, snap("01", a), snap("02", b))
	for _, day := range []string{"03", "04"} {
		l = compare(t, encoded(t, l), snap(day, withModified("Home", nil)))
		if l.Findings[0].Status != "unchanged" || l.Observations[0].LastKnownModifiedAt != "2026-09-02T10:00:00Z" {
			t.Fatal("unknown provider time lost the modification bound")
		}
		var fields map[string]any
		_ = json.Unmarshal(l.Observations[0].Candidate, &fields)
		if fields["source"].(map[string]any)["modified_at"] != nil {
			t.Fatal("raw source timestamp was invented")
		}
	}
	l = compare(t, encoded(t, l), snap("05", a))
	if len(l.Events) != 1 || l.Findings[0].Status != "requires_review" || l.Observations[0].ObservedAt != "2026-09-04T12:00:00Z" {
		t.Fatal("stale observation after missing timestamps created a reverse move")
	}
	l = compare(t, encoded(t, l), snap("06", withModified("Personal", "2026-09-06T10:00:00Z")))
	if len(l.Events) != 2 || l.Observations[0].LastKnownModifiedAt != "2026-09-06T10:00:00Z" {
		t.Fatal("genuine newer move was blocked")
	}
}

func TestLegacyLedgerModificationBound(t *testing.T) {
	withModified := func(project string, stamp any) json.RawMessage {
		return edit(row("one", project), func(m map[string]any) { m["source"].(map[string]any)["modified_at"] = stamp })
	}
	a := withModified("Work", "2026-09-01T10:00:00Z")
	b := withModified("Home", "2026-09-02T10:00:00Z")
	for _, historyOnly := range []bool{false, true} {
		l := compare(t, snap("01", a), snap("02", b))
		if historyOnly {
			l = compare(t, encoded(t, l), snap("03", withModified("Home", nil)))
		}
		for i := range l.Observations {
			l.Observations[i].LastKnownModifiedAt = ""
		}
		for i := range l.Events {
			l.Events[i].From.LastKnownModifiedAt = ""
			l.Events[i].To.LastKnownModifiedAt = ""
		}
		if _, err := parsePrevious(encoded(t, l)); err != nil {
			t.Fatal("legacy ledger rejected:", err)
		}
		l = compare(t, encoded(t, l), snap("04", withModified("Home", nil)))
		l = compare(t, encoded(t, l), snap("05", a))
		if len(l.Events) != 1 || l.Findings[0].Status != "requires_review" {
			t.Fatal("legacy ledger evidence did not restore modification bound")
		}
	}
}

func TestInvalidLedgerModificationBound(t *testing.T) {
	r := edit(row("one", "A"), func(m map[string]any) { m["source"].(map[string]any)["modified_at"] = "2026-09-01T10:00:00Z" })
	for _, bound := range []string{"invalid", "2026-08-01T10:00:00Z"} {
		l := compare(t, snap("01", r), snap("02", r))
		l.Observations[0].LastKnownModifiedAt = bound
		if _, err := parsePrevious(encoded(t, l)); err == nil {
			t.Fatal("invalid bound accepted")
		}
	}
}

func TestCorpusEnrichmentSurvivesPolling(t *testing.T) {
	enriched := edit(row("one", "A"), func(m map[string]any) {
		m["input"] = map[string]any{"transcript": "Synthetic recovered words", "transcript_provenance": "original", "recorded_at": "2026-08-01T12:00:00Z", "processing_clock": "2026-08-01T12:00:01Z", "time_zone": "America/Chicago", "available_aliases": []string{"work"}, "prompt_version": "v1"}
		m["historical_delivery"] = map[string]any{"original_project_id": "A", "original_alias": "work", "original_extracted_item": map[string]any{"title": "Synthetic original item"}, "lookup_status": "found"}
	})
	fresh := func(project string) json.RawMessage {
		return edit(row("one", project), func(m map[string]any) {
			m["input"] = map[string]any{"transcript": nil, "transcript_provenance": "unavailable", "recorded_at": nil, "processing_clock": nil, "time_zone": nil, "available_aliases": nil, "prompt_version": nil}
			m["historical_delivery"] = map[string]any{"original_project_id": nil, "original_alias": nil, "original_extracted_item": nil, "lookup_status": "unavailable_cluster_authentication"}
		})
	}
	l := compare(t, snap("01", enriched), snap("02", fresh("A")))
	check := func(raw json.RawMessage) {
		var got, want map[string]any
		_ = json.Unmarshal(raw, &got)
		_ = json.Unmarshal(enriched, &want)
		for _, field := range []string{"input", "historical_delivery"} {
			if !reflect.DeepEqual(got[field], want[field]) {
				t.Errorf("lost %s corpus enrichment", field)
			}
		}
	}
	check(l.Observations[0].Candidate)
	l = compare(t, encoded(t, l), snap("03", fresh("B")))
	if len(l.Events) != 1 {
		t.Fatal("move missing")
	}
	check(l.Observations[0].Candidate)
	check(l.Events[0].From.Candidate)
	check(l.Events[0].To.Candidate)
	clockOnly := edit(enriched, func(m map[string]any) {
		m["input"].(map[string]any)["transcript"] = nil
		m["input"].(map[string]any)["transcript_provenance"] = "unavailable"
	})
	l = compare(t, snap("01", clockOnly), snap("02", fresh("A")))
	if !bytes.Contains(l.Observations[0].Candidate, []byte("America/Chicago")) {
		t.Fatal("clock enrichment without transcript lost")
	}
}

func TestCLIReplayReportsNoNewEvents(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current.json")
	previous := filepath.Join(dir, "previous.json")
	data := snap("02", row("one", "B"))
	l := compare(t, snap("01", row("one", "A")), data)
	if err := os.WriteFile(previous, encoded(t, l), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(current, data, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	if err := run([]string{"--previous", previous, "--current", current, "--out", filepath.Join(dir, "replay.json")}, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "new move events: 0") {
		t.Fatal("replay reported new moves")
	}
}

func TestPrivateOutputAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	prev := filepath.Join(dir, "previous.json")
	current := filepath.Join(dir, "current.json")
	for name, data := range map[string][]byte{prev: snap("01", row("one", "A")), current: snap("02", row("one", "B"))} {
		if err := os.WriteFile(name, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "new-private-dir", "ledger.json")
	args := []string{"--previous", prev, "--current", current, "--out", out}
	var stdout bytes.Buffer
	if err := run(args, &stdout); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout.String(), "Private") || strings.Contains(stdout.String(), "one") || strings.Contains(stdout.String(), dir) {
		t.Fatal("stdout exposed private context")
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("output is not private")
	}
	info, err = os.Stat(filepath.Dir(out))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("new directory is not private")
	}
	written, _ := os.ReadFile(out)
	if err := run(args, &stdout); err == nil {
		t.Fatal("existing output overwritten")
	}
	for _, target := range []string{prev, current} {
		args[len(args)-1] = target
		if err := run(args, &stdout); err == nil {
			t.Fatal("input overwritten")
		}
	}
	link := filepath.Join(dir, "symlink.json")
	if err := os.Symlink(out, link); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = link
	if err := run(args, &stdout); err == nil {
		t.Fatal("output symlink followed")
	}
	after, _ := os.ReadFile(out)
	if !bytes.Equal(written, after) {
		t.Fatal("prior output changed")
	}
	if err := run([]string{"--previous", prev, "--current", current}, &stdout); err == nil {
		t.Fatal("missing out accepted")
	}
}
