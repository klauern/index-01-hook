package evalcorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type sourceMarker struct {
	Marker      string `json:"marker"`
	Fingerprint string `json:"recording_fingerprint"`
	Index       *int   `json:"item_index"`
}

type sourceCandidate struct {
	ID     string `json:"candidate_id"`
	Source struct {
		TaskID  string         `json:"task_id"`
		Markers []sourceMarker `json:"markers"`
	} `json:"source"`
	Current struct {
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		Kind      string `json:"kind"`
	} `json:"observed_current"`
	Input struct {
		Transcript   *string         `json:"transcript"`
		Provenance   string          `json:"transcript_provenance"`
		Routing      *RoutingConfig  `json:"routing"`
		SavedOutput  json.RawMessage `json:"saved_output"`
		PromptSHA256 string          `json:"prompt_sha256"`
		Model        string          `json:"model"`
	} `json:"input"`
	Review             json.RawMessage `json:"review"`
	HistoricalDelivery struct {
		ExpectedItemCount *int `json:"expected_item_count"`
	} `json:"historical_delivery"`
}

type sourceObservation struct {
	TaskID     string          `json:"task_id"`
	ObservedAt string          `json:"observed_at"`
	Candidate  json.RawMessage `json:"candidate"`
}

type sourceEvent struct {
	ID            string            `json:"event_id"`
	Type          string            `json:"type"`
	From          sourceObservation `json:"from"`
	To            sourceObservation `json:"to"`
	Review        json.RawMessage   `json:"review"`
	ExpectedRoute json.RawMessage   `json:"expected_route"`
}

type sourceReview struct {
	Status            string `json:"status"`
	Basis             string `json:"basis"`
	ExpectedProject   string `json:"expected_project"`
	ExpectedProjectID string `json:"expected_project_id"`
}

var recordingMarkerPattern = regexp.MustCompile(`^\[index01:([0-9a-f]{64}):(0|[1-9][0-9]*)\]$`)

func parseCandidate(data json.RawMessage) (sourceCandidate, error) {
	var candidate sourceCandidate
	if err := json.Unmarshal(data, &candidate); err != nil {
		return candidate, invalidSource("candidate fields")
	}
	if !validID(candidate.ID) || !validID(candidate.Source.TaskID) || !validID(candidate.Current.ProjectID) || strings.TrimSpace(candidate.Current.Title) == "" {
		return candidate, invalidSource("candidate identity, project ID, or title")
	}
	if candidate.Current.Kind != "TEXT" && candidate.Current.Kind != "NOTE" {
		return candidate, invalidSource("candidate kind")
	}
	if count := candidate.HistoricalDelivery.ExpectedItemCount; count != nil && *count <= 0 {
		return candidate, invalidSource("expected item count must be positive")
	}
	if len(candidate.Source.Markers) != 1 {
		return candidate, invalidSource("candidate requires one unambiguous recording marker")
	}
	m := candidate.Source.Markers[0]
	parts := recordingMarkerPattern.FindStringSubmatch(m.Marker)
	if len(parts) != 3 || m.Index == nil || *m.Index < 0 || parts[1] != m.Fingerprint || parts[2] != strconv.Itoa(*m.Index) {
		return candidate, invalidSource("recording marker evidence")
	}
	return candidate, nil
}

func parseReview(data json.RawMessage) (sourceReview, error) {
	var review sourceReview
	if len(data) == 0 || string(data) == "null" {
		return review, nil
	}
	if err := json.Unmarshal(data, &review); err != nil {
		return review, invalidSource("review fields")
	}
	if review.ExpectedProject != "" && review.ExpectedProjectID != "" && review.ExpectedProject != review.ExpectedProjectID {
		return review, invalidSource("conflicting review project IDs")
	}
	return review, nil
}

func reviewProject(review sourceReview) string {
	if review.ExpectedProject != "" {
		return review.ExpectedProject
	}
	return review.ExpectedProjectID
}

func approved(status string) bool { return status == "approved" || status == "accepted" }

func eventLabel(e sourceEvent) (Label, error) {
	review, err := parseReview(e.Review)
	if err != nil {
		return Label{}, err
	}
	label := Label{Status: "blocked", Basis: "latest_event_" + review.Status, EventID: e.ID}
	if approved(review.Status) || (review.Status == "inferred" && review.Basis == "owner_default") {
		var route struct {
			ProjectID string `json:"project_id"`
		}
		if len(e.ExpectedRoute) == 0 || json.Unmarshal(e.ExpectedRoute, &route) != nil || !validID(route.ProjectID) {
			return Label{}, invalidSource("approved event requires an expected route project ID")
		}
		label.ProjectID = route.ProjectID
		if approved(review.Status) {
			label.Status, label.Basis = "approved", "event_review"
		} else {
			label.Status, label.Basis = "inferred", "owner_default"
		}
	}
	return label, nil
}

func sameIdentity(a, b sourceCandidate) bool {
	return a.Source.TaskID == b.Source.TaskID && a.Source.Markers[0].Marker == b.Source.Markers[0].Marker && a.Current.Kind == b.Current.Kind
}

func stamp(value string) time.Time { parsed, _ := time.Parse(time.RFC3339Nano, value); return parsed }

// Import converts observations into an audit corpus. It never reconstructs transcripts.
func Import(data []byte) (Corpus, error) {
	var root struct {
		Version     int    `json:"format_version"`
		Type        string `json:"type"`
		CollectedAt string `json:"collected_at"`
	}
	if err := json.Unmarshal(data, &root); err != nil || root.Version != 1 || !validTime(root.CollectedAt) {
		return Corpus{}, invalidSource("schema version or collection time")
	}
	var candidates []json.RawMessage
	var events []sourceEvent
	reviews := map[string]json.RawMessage{}
	blockedTasks := map[string][]string{}
	sourceBlockers := []string{}
	observationTimes := map[string]string{}
	kind := "snapshot"
	switch root.Type {
	case "":
		var s struct {
			Collection *struct {
				Failures []json.RawMessage `json:"failures"`
			} `json:"collection"`
			Candidates []json.RawMessage `json:"candidates"`
		}
		if err := json.Unmarshal(data, &s); err != nil || s.Collection == nil || s.Collection.Failures == nil || s.Candidates == nil {
			return Corpus{}, invalidSource("snapshot requires collection failures and candidates arrays")
		}
		candidates = s.Candidates
		if len(s.Collection.Failures) > 0 {
			sourceBlockers = append(sourceBlockers, "source_collection_incomplete")
		}
	case "routing_feedback_ledger":
		kind = root.Type
		var s struct {
			Observations []sourceObservation        `json:"observations"`
			Events       []sourceEvent              `json:"events"`
			Reviews      map[string]json.RawMessage `json:"reviews"`
			Incomplete   *bool                      `json:"incomplete"`
			Failures     []json.RawMessage          `json:"collection_failures"`
			Findings     []struct {
				TaskID string `json:"task_id"`
				Status string `json:"status"`
			} `json:"findings"`
		}
		if err := json.Unmarshal(data, &s); err != nil || s.Observations == nil || s.Events == nil || s.Findings == nil || s.Incomplete == nil || s.Failures == nil {
			return Corpus{}, invalidSource("ledger requires observation, event, finding, failure arrays and incomplete flag")
		}
		if *s.Incomplete || len(s.Failures) > 0 {
			sourceBlockers = append(sourceBlockers, "source_collection_incomplete")
		}
		for _, o := range s.Observations {
			c, err := parseCandidate(o.Candidate)
			if err != nil {
				return Corpus{}, err
			}
			if c.Source.TaskID != o.TaskID || !validTime(o.ObservedAt) || stamp(o.ObservedAt).After(stamp(root.CollectedAt)) {
				return Corpus{}, invalidSource("trusted observation identity or time")
			}
			candidates = append(candidates, o.Candidate)
			observationTimes[o.TaskID] = o.ObservedAt
		}
		events = s.Events
		if s.Reviews != nil {
			reviews = s.Reviews
		}
		for _, f := range s.Findings {
			if !validID(f.TaskID) {
				return Corpus{}, invalidSource("finding task ID")
			}
			switch f.Status {
			case "requires_review", "not_observed", "incomplete":
				blockedTasks[f.TaskID] = append(blockedTasks[f.TaskID], "source_"+f.Status)
			case "observed_move", "new_observation", "unchanged":
			default:
				return Corpus{}, invalidSource("finding status")
			}
		}
	default:
		return Corpus{}, invalidSource("unsupported source type")
	}
	if len(candidates) == 0 {
		return Corpus{}, invalidSource("no trusted candidates")
	}
	byTask := map[string]sourceCandidate{}
	seenIDs, seenMarkers := map[string]bool{}, map[string]bool{}
	for _, raw := range candidates {
		c, err := parseCandidate(raw)
		if err != nil {
			return Corpus{}, err
		}
		_, seenTask := byTask[c.Source.TaskID]
		if seenTask || seenIDs[c.ID] || seenMarkers[c.Source.Markers[0].Marker] {
			return Corpus{}, invalidSource("duplicate candidate, task, marker, or item identity")
		}
		byTask[c.Source.TaskID] = c
		seenIDs[c.ID], seenMarkers[c.Source.Markers[0].Marker] = true, true
	}
	for taskID := range blockedTasks {
		if _, exists := byTask[taskID]; !exists {
			sourceBlockers = append(sourceBlockers, "source_unresolved_task_identity")
		}
	}
	latest := map[string]sourceEvent{}
	seenEventIDs := map[string]bool{}
	for _, e := range events {
		from, err := parseCandidate(e.From.Candidate)
		if err != nil {
			return Corpus{}, err
		}
		to, err := parseCandidate(e.To.Candidate)
		if err != nil {
			return Corpus{}, err
		}
		trusted, exists := byTask[e.To.TaskID]
		if !validID(e.ID) || seenEventIDs[e.ID] || e.Type != "observed_move" || !exists || e.From.TaskID != from.Source.TaskID || e.To.TaskID != to.Source.TaskID || !sameIdentity(from, to) || !sameIdentity(to, trusted) || !validTime(e.From.ObservedAt) || !validTime(e.To.ObservedAt) || !stamp(e.To.ObservedAt).After(stamp(e.From.ObservedAt)) || stamp(e.To.ObservedAt).After(stamp(observationTimes[e.To.TaskID])) || from.Current.ProjectID == to.Current.ProjectID {
			return Corpus{}, invalidSource("move event identity, marker, or chronology")
		}
		seenEventIDs[e.ID] = true
		if _, err := eventLabel(e); err != nil {
			return Corpus{}, err
		}
		old, exists := latest[e.To.TaskID]
		if exists && stamp(old.To.ObservedAt).Equal(stamp(e.To.ObservedAt)) {
			return Corpus{}, invalidSource("conflicting move events at the same time")
		}
		if !exists || stamp(e.To.ObservedAt).After(stamp(old.To.ObservedAt)) {
			latest[e.To.TaskID] = e
		}
	}
	hash := sha256.Sum256(data)
	corpus := Corpus{FormatVersion: Version, Type: CorpusType, Source: Source{Kind: kind, SHA256: hex.EncodeToString(hash[:]), CollectedAt: root.CollectedAt}, Examples: []Example{}}
	groups := map[string]*Example{}
	for taskID, c := range byTask {
		m := c.Source.Markers[0]
		e := groups[m.Fingerprint]
		if e == nil {
			split := "development"
			hash := sha256.Sum256([]byte(m.Fingerprint))
			if int(hash[0])%5 == 0 {
				split = "held_out"
			}
			e = &Example{ID: "recording-" + m.Fingerprint, RecordingFingerprint: m.Fingerprint, Split: split, Input: Input{Provenance: "missing"}, Targets: []Target{}, Blockers: append([]string{}, sourceBlockers...)}
			groups[m.Fingerprint] = e
		}
		if c.Input.Transcript != nil && strings.TrimSpace(*c.Input.Transcript) != "" && c.Input.Provenance == "original" {
			if e.Input.Text != "" && e.Input.Text != *c.Input.Transcript {
				return Corpus{}, invalidSource("conflicting original transcripts for one recording")
			}
			e.Input.Text, e.Input.Provenance = *c.Input.Transcript, "original"
		}
		if c.Input.Routing != nil {
			if e.Routing != nil && !reflect.DeepEqual(e.Routing, c.Input.Routing) {
				return Corpus{}, invalidSource("conflicting routing configurations for one recording")
			}
			e.Routing = c.Input.Routing
		}
		if len(c.Input.SavedOutput) > 0 && string(c.Input.SavedOutput) != "null" {
			var output map[string]json.RawMessage
			if json.Unmarshal(c.Input.SavedOutput, &output) != nil || output == nil {
				return Corpus{}, invalidSource("saved output must be an independent JSON object")
			}
			// Normalize whitespace and key order before comparing sibling evidence.
			var normalized any
			decoder := json.NewDecoder(strings.NewReader(string(c.Input.SavedOutput)))
			decoder.UseNumber()
			if err := decoder.Decode(&normalized); err != nil {
				return Corpus{}, invalidSource("saved output JSON")
			}
			encoded, _ := json.Marshal(normalized)
			if len(e.SavedOutput) > 0 && string(e.SavedOutput) != string(encoded) {
				return Corpus{}, invalidSource("conflicting saved outputs for one recording")
			}
			e.SavedOutput = encoded
		}
		for _, field := range []struct {
			target      *string
			value, name string
		}{
			{&e.Input.PromptSHA256, c.Input.PromptSHA256, "prompt hashes"},
			{&e.Input.Model, c.Input.Model, "models"},
		} {
			if field.value != "" {
				if *field.target != "" && *field.target != field.value {
					return Corpus{}, invalidSource("conflicting " + field.name + " for one recording")
				}
				*field.target = field.value
			}
		}
		if count := c.HistoricalDelivery.ExpectedItemCount; count != nil {
			if e.ExpectedItemCount != nil && *e.ExpectedItemCount != *count {
				return Corpus{}, invalidSource("conflicting expected item counts for one recording")
			}
			value := *count
			e.ExpectedItemCount = &value
		}
		e.Blockers = append(e.Blockers, blockedTasks[taskID]...)
		label := Label{Status: "unlabeled", Basis: "missing"}
		if event, exists := latest[taskID]; exists {
			var err error
			label, err = eventLabel(event)
			if err != nil {
				return Corpus{}, err
			}
			to, _ := parseCandidate(event.To.Candidate)
			if to.Current.ProjectID != c.Current.ProjectID {
				label = Label{Status: "blocked", Basis: "latest_event_route_mismatch", EventID: event.ID}
			}
		}
		review, err := parseReview(c.Review)
		if err != nil {
			return Corpus{}, err
		}
		if newer, exists := reviews[taskID]; exists {
			ledgerReview, parseErr := parseReview(newer)
			err = parseErr
			if err != nil {
				return Corpus{}, err
			}
			if approved(ledgerReview.Status) || ledgerReview.Status == "pending" || ledgerReview.Status == "rejected" {
				review = ledgerReview
			}
		}
		if approved(review.Status) {
			if !validID(reviewProject(review)) {
				return Corpus{}, invalidSource("approved candidate review requires an exact expected project ID")
			}
			label = Label{Status: "approved", Basis: "candidate_review", ProjectID: reviewProject(review)}
		} else if review.Status == "rejected" || review.Status == "pending" {
			label = Label{Status: "blocked", Basis: "candidate_review_" + review.Status}
		}
		itemKind := "task"
		if c.Current.Kind == "NOTE" {
			itemKind = "note"
		}
		e.Targets = append(e.Targets, Target{TaskID: taskID, ItemIndex: *m.Index, Title: c.Current.Title, Kind: itemKind, ObservedProjectID: c.Current.ProjectID, Label: label})
	}
	for _, e := range groups {
		sort.Slice(e.Targets, func(i, j int) bool { return e.Targets[i].ItemIndex < e.Targets[j].ItemIndex })
		sort.Strings(e.Blockers)
		corpus.Examples = append(corpus.Examples, *e)
	}
	sort.Slice(corpus.Examples, func(i, j int) bool { return corpus.Examples[i].ID < corpus.Examples[j].ID })
	if err := Validate(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// DecodeRouting reads an explicit evaluation configuration without provider access.
func DecodeRouting(data []byte) (*RoutingConfig, error) {
	var routing RoutingConfig
	if err := strictDecode(data, &routing); err != nil {
		return nil, errors.New("invalid routing configuration fields")
	}
	if err := validateRouting(&routing); err != nil {
		return nil, err
	}
	return &routing, nil
}
