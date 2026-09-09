// Command routing-feedback compares private TickTick observations without network access.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type marker struct {
	Marker      string `json:"marker"`
	Fingerprint string `json:"recording_fingerprint"`
	Index       *int   `json:"item_index"`
}

type candidate struct {
	ID     string `json:"candidate_id"`
	Source struct {
		TaskID     string   `json:"task_id"`
		Markers    []marker `json:"markers"`
		CreatedAt  string   `json:"created_at"`
		ModifiedAt string   `json:"modified_at"`
	} `json:"source"`
	Current struct {
		ProjectID string `json:"project_id"`
		Kind      string `json:"kind"`
	} `json:"observed_current"`
}

type snapshot struct {
	Version     int    `json:"format_version"`
	CollectedAt string `json:"collected_at"`
	Collection  *struct {
		Failures []json.RawMessage `json:"failures"`
	} `json:"collection"`
	Candidates []json.RawMessage `json:"candidates"`
}

type observation struct {
	TaskID              string          `json:"task_id"`
	ObservedAt          string          `json:"observed_at"`
	Candidate           json.RawMessage `json:"candidate"`
	LastKnownModifiedAt string          `json:"last_known_modified_at,omitempty"`
}

type event struct {
	ID            string          `json:"event_id"`
	Type          string          `json:"type"`
	From          observation     `json:"from"`
	To            observation     `json:"to"`
	Actor         string          `json:"actor"`
	Review        json.RawMessage `json:"review"`
	ExpectedRoute json.RawMessage `json:"expected_route"`
}

type finding struct {
	TaskID     string            `json:"task_id"`
	Status     string            `json:"status"`
	Reason     string            `json:"reason,omitempty"`
	Candidates []json.RawMessage `json:"candidates,omitempty"`
}

type ledger struct {
	Version      int                        `json:"format_version"`
	Type         string                     `json:"type"`
	CollectedAt  string                     `json:"collected_at"`
	Digest       string                     `json:"latest_snapshot_digest"`
	Incomplete   bool                       `json:"incomplete"`
	Failures     []json.RawMessage          `json:"collection_failures"`
	Reviews      map[string]json.RawMessage `json:"reviews"`
	Observations []observation              `json:"observations"`
	Events       []event                    `json:"events"`
	Findings     []finding                  `json:"findings"`
}

var markerPattern = regexp.MustCompile(`^\[index01:([0-9a-f]{64}):(0|[1-9][0-9]*)\]$`)

func validID(s string) bool {
	return s != "" && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n\t")
}

func validTime(s string) bool { _, err := time.Parse(time.RFC3339Nano, s); return err == nil }

func validSourceTime(s string) bool {
	_, ok := sourceTime(s)
	return s == "" || ok
}

func sourceTime(s string) (time.Time, bool) {
	stamp, err := time.Parse(time.RFC3339Nano, s)
	if err == nil {
		return stamp, true
	}
	stamp, err = time.Parse("2006-01-02T15:04:05Z0700", s)
	return stamp, err == nil
}

func decodeCandidate(raw json.RawMessage) (candidate, error) {
	var c candidate
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, errors.New("candidate has invalid fields")
	}
	if !validID(c.ID) || !validID(c.Source.TaskID) || !validID(c.Current.ProjectID) || !validID(c.Current.Kind) {
		return c, errors.New("candidate requires complete candidate, task, project, and kind identifiers")
	}
	if len(c.Source.Markers) == 0 {
		return c, errors.New("candidate requires recording markers")
	}
	for _, m := range c.Source.Markers {
		parts := markerPattern.FindStringSubmatch(m.Marker)
		if len(parts) != 3 || m.Index == nil || *m.Index < 0 || parts[1] != m.Fingerprint || parts[2] != fmt.Sprint(*m.Index) {
			return c, errors.New("candidate has invalid recording marker fields")
		}
	}
	for _, stamp := range []string{c.Source.CreatedAt, c.Source.ModifiedAt} {
		if !validSourceTime(stamp) {
			return c, errors.New("candidate has invalid source timestamp")
		}
	}
	return c, nil
}

func digest(data []byte) (string, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return "", errors.New("input is not valid JSON")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("cannot encode input")
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

func parseSnapshot(data []byte) (snapshot, error) {
	var s snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return s, errors.New("invalid snapshot fields")
	}
	if s.Version != 1 || !validTime(s.CollectedAt) || s.Collection == nil || s.Collection.Failures == nil || s.Candidates == nil {
		return s, errors.New("snapshot requires version 1, collection time, failure array, and candidate array")
	}
	for _, raw := range s.Candidates {
		if _, err := decodeCandidate(raw); err != nil {
			return s, err
		}
	}
	return s, nil
}

func parsePrevious(data []byte) (ledger, error) {
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return ledger{}, errors.New("previous input is not valid JSON")
	}
	if header.Type == "" {
		s, err := parseSnapshot(data)
		if err != nil {
			return ledger{}, err
		}
		d, _ := digest(data)
		return reconcile(ledger{Version: 1, Type: "routing_feedback_ledger"}, s, d)
	}
	var l ledger
	if err := json.Unmarshal(data, &l); err != nil {
		return l, errors.New("invalid ledger fields")
	}
	if l.Type != "routing_feedback_ledger" || l.Version != 1 || !validTime(l.CollectedAt) || len(l.Digest) != 64 || l.Observations == nil || l.Events == nil || l.Findings == nil || l.Failures == nil {
		return l, errors.New("invalid ledger schema")
	}
	seen := map[string]bool{}
	markers := map[string]bool{}
	for _, o := range l.Observations {
		c, err := decodeCandidate(o.Candidate)
		if err != nil {
			return l, err
		}
		if o.TaskID != c.Source.TaskID || seen[o.TaskID] || !validTime(o.ObservedAt) || after(o.ObservedAt, l.CollectedAt) || len(c.Source.Markers) != 1 || markers[c.Source.Markers[0].Marker] || !validModifiedBound(o) {
			return l, errors.New("ledger contains invalid trusted observations")
		}
		seen[o.TaskID] = true
		markers[c.Source.Markers[0].Marker] = true
	}
	seenEvents := map[string]bool{}
	for _, e := range l.Events {
		from, err1 := decodeCandidate(e.From.Candidate)
		to, err2 := decodeCandidate(e.To.Candidate)
		if err1 != nil || err2 != nil || !validID(e.ID) || seenEvents[e.ID] || e.Type != "observed_move" || e.Actor != "unknown" || !validTime(e.From.ObservedAt) || !validTime(e.To.ObservedAt) || !after(e.To.ObservedAt, e.From.ObservedAt) || after(e.To.ObservedAt, l.CollectedAt) || e.From.TaskID != from.Source.TaskID || e.To.TaskID != to.Source.TaskID || e.From.TaskID != e.To.TaskID || from.Current.ProjectID == to.Current.ProjectID || identityChanged(from, to) || len(e.Review) == 0 || len(e.ExpectedRoute) == 0 {
			return l, errors.New("ledger contains invalid move events")
		}
		if !validModifiedBound(e.From) || !validModifiedBound(e.To) {
			return l, errors.New("ledger contains invalid modification bounds")
		}
		seenEvents[e.ID] = true
	}
	return l, nil
}

func after(a, b string) bool {
	x, _ := time.Parse(time.RFC3339Nano, a)
	y, _ := time.Parse(time.RFC3339Nano, b)
	return x.After(y)
}

func identityChanged(a, b candidate) bool {
	return a.Current.Kind != b.Current.Kind || len(a.Source.Markers) != 1 || len(b.Source.Markers) != 1 || a.Source.Markers[0].Marker != b.Source.Markers[0].Marker
}

func validModifiedBound(o observation) bool {
	if o.LastKnownModifiedAt == "" {
		return true
	}
	bound, err := time.Parse(time.RFC3339Nano, o.LastKnownModifiedAt)
	if err != nil {
		return false
	}
	c, err := decodeCandidate(o.Candidate)
	if err != nil {
		return false
	}
	modified, known := sourceTime(c.Source.ModifiedAt)
	return !known || !modified.After(bound)
}

func maximumModified(stamps ...string) string {
	var maximum time.Time
	known := false
	for _, stamp := range stamps {
		value, valid := sourceTime(stamp)
		if valid && (!known || value.After(maximum)) {
			maximum, known = value, true
		}
	}
	if !known {
		return ""
	}
	return maximum.UTC().Format(time.RFC3339Nano)
}

func observedModified(o observation) string {
	c, _ := decodeCandidate(o.Candidate)
	return maximumModified(o.LastKnownModifiedAt, c.Source.ModifiedAt)
}

// Preserve recovered corpus context when a fresh provider snapshot lacks that context.
func preserveEnrichment(prior, current json.RawMessage) json.RawMessage {
	var oldFields, newFields map[string]json.RawMessage
	_ = json.Unmarshal(prior, &oldFields)
	_ = json.Unmarshal(current, &newFields)
	for _, field := range []string{"input", "historical_delivery"} {
		var oldContext, newContext map[string]json.RawMessage
		if json.Unmarshal(oldFields[field], &oldContext) != nil || len(oldContext) == 0 {
			continue
		}
		_ = json.Unmarshal(newFields[field], &newContext)
		if newContext == nil {
			newContext = map[string]json.RawMessage{}
		}
		for key, value := range oldContext {
			if missingContext(newContext[key]) && !missingContext(value) {
				newContext[key] = value
			}
		}
		newFields[field], _ = json.Marshal(newContext)
	}
	result, _ := json.Marshal(newFields)
	return result
}

func missingContext(raw json.RawMessage) bool {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value) == "" || value == "unknown" || strings.HasPrefix(value, "unavailable")
	}
	return false
}

func reconcile(previous ledger, current snapshot, currentDigest string) (ledger, error) {
	if previous.CollectedAt != "" && !after(current.CollectedAt, previous.CollectedAt) {
		if !after(previous.CollectedAt, current.CollectedAt) && previous.Digest == currentDigest {
			return previous, nil
		}
		return ledger{}, errors.New("current snapshot must be newer; equal times require the same snapshot")
	}
	result := ledger{Version: 1, Type: "routing_feedback_ledger", CollectedAt: current.CollectedAt, Digest: currentDigest, Incomplete: len(current.Collection.Failures) > 0, Failures: current.Collection.Failures, Observations: []observation{}, Events: append([]event{}, previous.Events...), Findings: []finding{}}
	result.Reviews = map[string]json.RawMessage{}
	for id, review := range previous.Reviews {
		result.Reviews[id] = review
	}
	trusted := map[string]observation{}
	markerOwner := map[string]string{}
	for _, o := range previous.Observations {
		o.LastKnownModifiedAt = observedModified(o)
		// Older ledgers may retain the last known timestamp only in move history.
		for _, e := range previous.Events {
			if e.To.TaskID == o.TaskID && !after(e.To.ObservedAt, o.ObservedAt) {
				o.LastKnownModifiedAt = maximumModified(o.LastKnownModifiedAt, observedModified(e.From), observedModified(e.To))
			}
		}
		trusted[o.TaskID] = o
		c, _ := decodeCandidate(o.Candidate)
		markerOwner[c.Source.Markers[0].Marker] = o.TaskID
	}
	byTask := map[string][]json.RawMessage{}
	markerCount := map[string]int{}
	candidateCount := map[string]int{}
	for _, raw := range current.Candidates {
		c, _ := decodeCandidate(raw)
		byTask[c.Source.TaskID] = append(byTask[c.Source.TaskID], raw)
		candidateCount[c.ID]++
		for _, m := range c.Source.Markers {
			markerCount[m.Marker]++
		}
	}
	for taskID, rows := range byTask {
		c, _ := decodeCandidate(rows[0])
		reason := ""
		if len(rows) != 1 || candidateCount[c.ID] != 1 {
			reason = "duplicate identity"
		}
		if len(c.Source.Markers) != 1 {
			reason = "multiple recording markers"
		}
		for _, m := range c.Source.Markers {
			if markerCount[m.Marker] != 1 {
				reason = "duplicate recording marker"
			}
			if owner := markerOwner[m.Marker]; owner != "" && owner != taskID {
				reason = "recording marker belongs to another task identity"
			}
		}
		prior, exists := trusted[taskID]
		if exists {
			old, _ := decodeCandidate(prior.Candidate)
			if identityChanged(old, c) {
				reason = "recording marker or item kind changed"
			}
			oldModified, oldKnown := sourceTime(prior.LastKnownModifiedAt)
			newModified, newKnown := sourceTime(c.Source.ModifiedAt)
			if oldKnown && newKnown && newModified.Before(oldModified) {
				reason = "source modification time regressed"
			}
		}
		if result.Incomplete {
			reason = "collection failures prevent trusted observation updates"
		}
		if reason != "" {
			status := "requires_review"
			if result.Incomplete {
				status = "incomplete"
			}
			result.Findings = append(result.Findings, finding{TaskID: taskID, Status: status, Reason: reason, Candidates: rows})
			continue
		}
		next := observation{TaskID: taskID, ObservedAt: current.CollectedAt, Candidate: rows[0]}
		next.LastKnownModifiedAt = maximumModified(prior.LastKnownModifiedAt, c.Source.ModifiedAt)
		if exists {
			next.Candidate = preserveEnrichment(prior.Candidate, next.Candidate)
		}
		status := "new_observation"
		if exists {
			status = "unchanged"
			old, _ := decodeCandidate(prior.Candidate)
			if old.Current.ProjectID != c.Current.ProjectID {
				status = "observed_move"
				key, _ := json.Marshal([]string{taskID, prior.ObservedAt, next.ObservedAt, old.Current.ProjectID, c.Current.ProjectID})
				hash := sha256.Sum256(key)
				expectedRoute, _ := json.Marshal(map[string]string{"project_id": c.Current.ProjectID})
				// The owner treats list moves as routing corrections unless overridden.
				result.Events = append(result.Events, event{
					ID: hex.EncodeToString(hash[:]), Type: status, From: prior, To: next, Actor: "unknown",
					Review:        json.RawMessage(`{"status":"inferred","basis":"owner_default","interpretation":"original_routing_error"}`),
					ExpectedRoute: expectedRoute,
				})
			}
		}
		trusted[taskID] = next
		if _, exists := result.Reviews[taskID]; !exists {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(rows[0], &fields)
			if review, exists := fields["review"]; exists {
				result.Reviews[taskID] = review
			}
		}
		result.Findings = append(result.Findings, finding{TaskID: taskID, Status: status})
	}
	for id, o := range trusted {
		result.Observations = append(result.Observations, o)
		if _, exists := byTask[id]; !exists {
			result.Findings = append(result.Findings, finding{TaskID: id, Status: "not_observed", Reason: "absence does not establish deletion, completion, or a routing correction"})
		}
	}
	sort.Slice(result.Observations, func(i, j int) bool { return result.Observations[i].TaskID < result.Observations[j].TaskID })
	sort.Slice(result.Findings, func(i, j int) bool { return result.Findings[i].TaskID < result.Findings[j].TaskID })
	sort.SliceStable(result.Events, func(i, j int) bool {
		if result.Events[i].To.ObservedAt != result.Events[j].To.ObservedAt {
			return after(result.Events[j].To.ObservedAt, result.Events[i].To.ObservedAt)
		}
		return result.Events[i].ID < result.Events[j].ID
	})
	return result, nil
}

func savePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return errors.New("cannot create output directory")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create output; choose a new path")
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return errors.New("cannot write output")
	}
	if err := f.Sync(); err != nil {
		return errors.New("cannot sync output")
	}
	if err := f.Close(); err != nil {
		return errors.New("cannot close output")
	}
	ok = true
	return nil
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("routing-feedback", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	previousPath := flags.String("previous", "", "previous snapshot or ledger")
	currentPath := flags.String("current", "", "current snapshot")
	outputPath := flags.String("out", "", "new private ledger path")
	if err := flags.Parse(args); err != nil {
		return errors.New("invalid arguments; require --previous, --current, and --out")
	}
	if *previousPath == "" || *currentPath == "" || *outputPath == "" || flags.NArg() != 0 {
		return errors.New("require --previous, --current, and --out")
	}
	previousData, err := os.ReadFile(*previousPath)
	if err != nil {
		return errors.New("cannot read previous input")
	}
	currentData, err := os.ReadFile(*currentPath)
	if err != nil {
		return errors.New("cannot read current input")
	}
	previous, err := parsePrevious(previousData)
	if err != nil {
		return err
	}
	current, err := parseSnapshot(currentData)
	if err != nil {
		return err
	}
	d, err := digest(currentData)
	if err != nil {
		return err
	}
	result, err := reconcile(previous, current, d)
	if err != nil {
		return err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return errors.New("cannot encode ledger")
	}
	if err := savePrivate(*outputPath, output.Bytes()); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, f := range result.Findings {
		counts[f.Status]++
	}
	fmt.Fprintf(stdout, "Observations: %d; move events retained: %d; new move events: %d; review: %d; not observed: %d; collection failures: %d\n", len(result.Observations), len(result.Events), len(result.Events)-len(previous.Events), counts["requires_review"], counts["not_observed"], len(result.Failures))
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "routing-feedback:", err)
		os.Exit(1)
	}
}
