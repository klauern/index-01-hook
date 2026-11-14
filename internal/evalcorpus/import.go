package evalcorpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"slices"
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
	var c sourceCandidate
	if json.Unmarshal(data, &c) != nil {
		return c, invalidSource("candidate fields")
	}
	return c, validateCandidate(c)
}
func validateCandidate(c sourceCandidate) error {
	if !validCandidateIdentity(c) {
		return invalidSource("candidate identity, project ID, or title")
	}
	if c.Current.Kind != "TEXT" && c.Current.Kind != "NOTE" {
		return invalidSource("candidate kind")
	}
	if count := c.HistoricalDelivery.ExpectedItemCount; count != nil && *count <= 0 {
		return invalidSource("expected item count must be positive")
	}
	if len(c.Source.Markers) != 1 {
		return invalidSource("candidate requires one unambiguous recording marker")
	}
	if !validMarker(c.Source.Markers[0]) {
		return invalidSource("recording marker evidence")
	}
	return nil
}
func validCandidateIdentity(c sourceCandidate) bool {
	for _, id := range []string{c.ID, c.Source.TaskID, c.Current.ProjectID} {
		if !validID(id) {
			return false
		}
	}
	return strings.TrimSpace(c.Current.Title) != ""
}
func validMarker(m sourceMarker) bool {
	parts := recordingMarkerPattern.FindStringSubmatch(m.Marker)
	if len(parts) != 3 || m.Index == nil {
		return false
	}
	return *m.Index >= 0 && parts[1] == m.Fingerprint && parts[2] == strconv.Itoa(*m.Index)
}

func parseReview(data json.RawMessage) (sourceReview, error) {
	var review sourceReview
	if len(data) == 0 || string(data) == "null" {
		return review, nil
	}
	if err := json.Unmarshal(data, &review); err != nil {
		return review, invalidSource("review fields")
	}
	if conflictingReviewProjects(review) {
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
	if !hasEventRoute(review) {
		return label, nil
	}
	project, err := eventRoute(e.ExpectedRoute)
	if err != nil {
		return Label{}, err
	}
	label.ProjectID = project
	label.Status, label.Basis = "inferred", "owner_default"
	if approved(review.Status) {
		label.Status, label.Basis = "approved", "event_review"
	}
	return label, nil
}
func hasEventRoute(r sourceReview) bool {
	return approved(r.Status) || ownerDefault(r)
}
func ownerDefault(r sourceReview) bool { return r.Status == "inferred" && r.Basis == "owner_default" }
func eventRoute(data json.RawMessage) (string, error) {
	var route struct {
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(data, &route) != nil || !validID(route.ProjectID) {
		return "", invalidSource("approved event requires an expected route project ID")
	}
	return route.ProjectID, nil
}

func sameIdentity(a, b sourceCandidate) bool {
	return a.Source.TaskID == b.Source.TaskID && a.Source.Markers[0].Marker == b.Source.Markers[0].Marker && a.Current.Kind == b.Current.Kind
}

func stamp(value string) time.Time { parsed, _ := time.Parse(time.RFC3339Nano, value); return parsed }

type sourceData struct {
	kind, collectedAt string
	candidates        []json.RawMessage
	events            []sourceEvent
	reviews           map[string]json.RawMessage
	blockedTasks      map[string][]string
	blockers          []string
	observationTimes  map[string]string
}

// Import converts observations into an audit corpus. It never reconstructs transcripts.
func Import(data []byte) (Corpus, error) {
	source, err := parseSource(data)
	if err != nil {
		return Corpus{}, err
	}
	byTask, err := indexCandidates(source.candidates)
	if err != nil {
		return Corpus{}, err
	}
	source.addUnresolvedTasks(byTask)
	latest, err := indexEvents(source.events, byTask, source.observationTimes)
	if err != nil {
		return Corpus{}, err
	}
	groups, err := groupCandidates(byTask, latest, source)
	if err != nil {
		return Corpus{}, err
	}
	hash := sha256.Sum256(data)
	corpus := Corpus{FormatVersion: Version, Type: CorpusType, Source: Source{Kind: source.kind, SHA256: hex.EncodeToString(hash[:]), CollectedAt: source.collectedAt}, Examples: []Example{}}
	for _, e := range groups {
		sort.Slice(e.Targets, func(i, j int) bool { return e.Targets[i].ItemIndex < e.Targets[j].ItemIndex })
		sort.Strings(e.Blockers)
		e.Blockers = slices.Compact(e.Blockers)
		corpus.Examples = append(corpus.Examples, *e)
	}
	sort.Slice(corpus.Examples, func(i, j int) bool { return corpus.Examples[i].ID < corpus.Examples[j].ID })
	if err := Validate(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

func parseSource(data []byte) (sourceData, error) {
	var header struct {
		Version     int    `json:"format_version"`
		Type        string `json:"type"`
		CollectedAt string `json:"collected_at"`
	}
	if json.Unmarshal(data, &header) != nil {
		return sourceData{}, invalidSource("schema version or collection time")
	}
	if header.Version != 1 || !validTime(header.CollectedAt) {
		return sourceData{}, invalidSource("schema version or collection time")
	}
	source := sourceData{kind: "snapshot", collectedAt: header.CollectedAt, reviews: map[string]json.RawMessage{}, blockedTasks: map[string][]string{}, blockers: []string{}, observationTimes: map[string]string{}}
	switch header.Type {
	case "":
		err := source.readSnapshot(data)
		return source, err
	case "routing_feedback_ledger":
		source.kind = header.Type
		err := source.readLedger(data)
		return source, err
	default:
		return sourceData{}, invalidSource("unsupported source type")
	}
}

type sourceSnapshot struct {
	Collection *struct {
		Failures []json.RawMessage `json:"failures"`
	} `json:"collection"`
	Candidates []json.RawMessage `json:"candidates"`
}

func (s sourceSnapshot) completeShape() bool {
	if s.Collection == nil {
		return false
	}
	return s.Collection.Failures != nil && s.Candidates != nil
}
func (s *sourceData) readSnapshot(data []byte) error {
	var snapshot sourceSnapshot
	if json.Unmarshal(data, &snapshot) != nil || !snapshot.completeShape() {
		return invalidSource("snapshot requires collection failures and candidates arrays")
	}
	s.candidates = snapshot.Candidates
	if len(snapshot.Collection.Failures) > 0 {
		s.blockers = append(s.blockers, "source_collection_incomplete")
	}
	return nil
}

type sourceFinding struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}
type sourceLedger struct {
	Observations []sourceObservation        `json:"observations"`
	Events       []sourceEvent              `json:"events"`
	Reviews      map[string]json.RawMessage `json:"reviews"`
	Incomplete   *bool                      `json:"incomplete"`
	Failures     []json.RawMessage          `json:"collection_failures"`
	Findings     []sourceFinding            `json:"findings"`
}

func (s sourceLedger) completeShape() bool {
	arraysPresent := s.Observations != nil && s.Events != nil && s.Findings != nil
	return arraysPresent && s.Incomplete != nil && s.Failures != nil
}
func (s *sourceData) readLedger(data []byte) error {
	var ledger sourceLedger
	if json.Unmarshal(data, &ledger) != nil || !ledger.completeShape() {
		return invalidSource("ledger requires observation, event, finding, failure arrays and incomplete flag")
	}
	if *ledger.Incomplete || len(ledger.Failures) > 0 {
		s.blockers = append(s.blockers, "source_collection_incomplete")
	}
	if err := s.readObservations(ledger.Observations); err != nil {
		return err
	}
	s.events = ledger.Events
	if ledger.Reviews != nil {
		s.reviews = ledger.Reviews
	}
	return s.readFindings(ledger.Findings)
}
func (s *sourceData) readObservations(observations []sourceObservation) error {
	for _, o := range observations {
		c, err := parseCandidate(o.Candidate)
		if err != nil {
			return err
		}
		if c.Source.TaskID != o.TaskID || !validObservationTime(o.ObservedAt, s.collectedAt) {
			return invalidSource("trusted observation identity or time")
		}
		s.candidates = append(s.candidates, o.Candidate)
		s.observationTimes[o.TaskID] = o.ObservedAt
	}
	return nil
}
func validObservationTime(observed, collected string) bool {
	return validTime(observed) && !stamp(observed).After(stamp(collected))
}
func (s *sourceData) readFindings(findings []sourceFinding) error {
	for _, f := range findings {
		if err := s.readFinding(f); err != nil {
			return err
		}
	}
	return nil
}
func (s *sourceData) readFinding(f sourceFinding) error {
	if !validID(f.TaskID) {
		return invalidSource("finding task ID")
	}
	switch f.Status {
	case "requires_review", "not_observed", "incomplete":
		s.blockedTasks[f.TaskID] = append(s.blockedTasks[f.TaskID], "source_"+f.Status)
	case "observed_move", "new_observation", "unchanged":
	default:
		return invalidSource("finding status")
	}
	return nil
}

func indexCandidates(candidates []json.RawMessage) (map[string]sourceCandidate, error) {
	if len(candidates) == 0 {
		return nil, invalidSource("no trusted candidates")
	}
	byTask := map[string]sourceCandidate{}
	seenIDs, seenMarkers := map[string]bool{}, map[string]bool{}
	for _, raw := range candidates {
		c, err := parseCandidate(raw)
		if err != nil {
			return nil, err
		}
		_, seenTask := byTask[c.Source.TaskID]
		duplicate := seenTask || seenIDs[c.ID] || seenMarkers[c.Source.Markers[0].Marker]
		if duplicate {
			return nil, invalidSource("duplicate candidate, task, marker, or item identity")
		}
		byTask[c.Source.TaskID] = c
		seenIDs[c.ID], seenMarkers[c.Source.Markers[0].Marker] = true, true
	}
	return byTask, nil
}

func indexEvents(events []sourceEvent, byTask map[string]sourceCandidate, observationTimes map[string]string) (map[string]sourceEvent, error) {
	index := eventIndex{latest: map[string]sourceEvent{}, seen: map[string]bool{}, candidates: byTask, times: observationTimes}
	for _, e := range events {
		if err := index.add(e); err != nil {
			return nil, err
		}
	}
	return index.latest, nil
}

type eventIndex struct {
	latest     map[string]sourceEvent
	seen       map[string]bool
	candidates map[string]sourceCandidate
	times      map[string]string
}

func (index *eventIndex) add(e sourceEvent) error {
	if err := index.validate(e); err != nil {
		return err
	}
	index.seen[e.ID] = true
	if _, err := eventLabel(e); err != nil {
		return err
	}
	old, exists := index.latest[e.To.TaskID]
	if exists && stamp(old.To.ObservedAt).Equal(stamp(e.To.ObservedAt)) {
		return invalidSource("conflicting move events at the same time")
	}
	if !exists || stamp(e.To.ObservedAt).After(stamp(old.To.ObservedAt)) {
		index.latest[e.To.TaskID] = e
	}
	return nil
}
func (index *eventIndex) validate(e sourceEvent) error {
	from, err := parseCandidate(e.From.Candidate)
	if err != nil {
		return err
	}
	to, err := parseCandidate(e.To.Candidate)
	if err != nil {
		return err
	}
	trusted, exists := index.candidates[e.To.TaskID]
	if !exists || !index.validIdentity(e) {
		return invalidSource("move event identity, marker, or chronology")
	}
	if !validMoveCandidates(e, from, to, trusted) || !validMoveChronology(e, index.times[e.To.TaskID]) {
		return invalidSource("move event identity, marker, or chronology")
	}
	return nil
}
func (index *eventIndex) validIdentity(e sourceEvent) bool {
	return validID(e.ID) && !index.seen[e.ID] && e.Type == "observed_move"
}
func validMoveCandidates(e sourceEvent, from, to, trusted sourceCandidate) bool {
	if e.From.TaskID != from.Source.TaskID || e.To.TaskID != to.Source.TaskID {
		return false
	}
	if !sameIdentity(from, to) || !sameIdentity(to, trusted) {
		return false
	}
	return from.Current.ProjectID != to.Current.ProjectID
}
func validMoveChronology(e sourceEvent, observed string) bool {
	if !validTime(e.From.ObservedAt) || !validTime(e.To.ObservedAt) {
		return false
	}
	return stamp(e.To.ObservedAt).After(stamp(e.From.ObservedAt)) && !stamp(e.To.ObservedAt).After(stamp(observed))
}

func groupCandidates(byTask map[string]sourceCandidate, latest map[string]sourceEvent, source sourceData) (map[string]*Example, error) {
	blockedTasks, sourceBlockers, reviews := source.blockedTasks, source.blockers, source.reviews
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
		if err := mergeCandidateEvidence(e, c); err != nil {
			return nil, err
		}
		e.Blockers = append(e.Blockers, blockedTasks[taskID]...)
		label, err := candidateLabel(c, latest, reviews)
		if err != nil {
			return nil, err
		}
		itemKind := "task"
		if c.Current.Kind == "NOTE" {
			itemKind = "note"
		}
		e.Targets = append(e.Targets, Target{TaskID: taskID, ItemIndex: *m.Index, Title: c.Current.Title, Kind: itemKind, ObservedProjectID: c.Current.ProjectID, Label: label})
	}
	return groups, nil
}

func mergeCandidateEvidence(e *Example, c sourceCandidate) error {
	steps := []func(*Example, sourceCandidate) error{mergeTranscript, mergeRouting, mergeSavedOutput, mergeInputMetadata, mergeItemCount}
	for _, step := range steps {
		if err := step(e, c); err != nil {
			return err
		}
	}
	return nil
}
func originalTranscript(c sourceCandidate) bool {
	if c.Input.Transcript == nil {
		return false
	}
	return strings.TrimSpace(*c.Input.Transcript) != "" && c.Input.Provenance == "original"
}
func mergeTranscript(e *Example, c sourceCandidate) error {
	if !originalTranscript(c) {
		return nil
	}
	if e.Input.Text != "" && e.Input.Text != *c.Input.Transcript {
		return invalidSource("conflicting original transcripts for one recording")
	}
	e.Input.Text, e.Input.Provenance = *c.Input.Transcript, "original"
	return nil
}
func mergeRouting(e *Example, c sourceCandidate) error {
	if c.Input.Routing == nil {
		return nil
	}
	if e.Routing != nil && !reflect.DeepEqual(e.Routing, c.Input.Routing) {
		return invalidSource("conflicting routing configurations for one recording")
	}
	e.Routing = c.Input.Routing
	return nil
}
func normalizeSavedOutput(data json.RawMessage) (json.RawMessage, error) {
	var output map[string]json.RawMessage
	if json.Unmarshal(data, &output) != nil || output == nil {
		return nil, invalidSource("saved output must be an independent JSON object")
	}
	// Normalize whitespace and key order without rounding JSON numbers.
	var normalized any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return nil, invalidSource("saved output JSON")
	}
	encoded, _ := json.Marshal(normalized)
	return encoded, nil
}
func mergeSavedOutput(e *Example, c sourceCandidate) error {
	if len(c.Input.SavedOutput) == 0 || string(c.Input.SavedOutput) == "null" {
		return nil
	}
	encoded, err := normalizeSavedOutput(c.Input.SavedOutput)
	if err != nil {
		return err
	}
	if len(e.SavedOutput) > 0 && string(e.SavedOutput) != string(encoded) {
		return invalidSource("conflicting saved outputs for one recording")
	}
	e.SavedOutput = encoded
	return nil
}
func mergeInputMetadata(e *Example, c sourceCandidate) error {
	if err := mergeEvidenceString(&e.Input.PromptSHA256, c.Input.PromptSHA256, "prompt hashes"); err != nil {
		return err
	}
	return mergeEvidenceString(&e.Input.Model, c.Input.Model, "models")
}
func mergeEvidenceString(target *string, value, name string) error {
	if value == "" {
		return nil
	}
	if *target != "" && *target != value {
		return invalidSource("conflicting " + name + " for one recording")
	}
	*target = value
	return nil
}
func mergeItemCount(e *Example, c sourceCandidate) error {
	count := c.HistoricalDelivery.ExpectedItemCount
	if count == nil {
		return nil
	}
	if e.ExpectedItemCount != nil && *e.ExpectedItemCount != *count {
		return invalidSource("conflicting expected item counts for one recording")
	}
	value := *count
	e.ExpectedItemCount = &value
	return nil
}

func candidateLabel(c sourceCandidate, latest map[string]sourceEvent, reviews map[string]json.RawMessage) (Label, error) {
	label, err := latestLabel(c, latest)
	if err != nil {
		return Label{}, err
	}
	review, err := effectiveReview(c, reviews)
	if err != nil {
		return Label{}, err
	}
	return reviewedLabel(label, review)
}
func latestLabel(c sourceCandidate, latest map[string]sourceEvent) (Label, error) {
	event, exists := latest[c.Source.TaskID]
	if !exists {
		return Label{Status: "unlabeled", Basis: "missing"}, nil
	}
	label, err := eventLabel(event)
	if err != nil {
		return Label{}, err
	}
	to, _ := parseCandidate(event.To.Candidate)
	if to.Current.ProjectID != c.Current.ProjectID {
		label = Label{Status: "blocked", Basis: "latest_event_route_mismatch", EventID: event.ID}
	}
	return label, nil
}
func effectiveReview(c sourceCandidate, reviews map[string]json.RawMessage) (sourceReview, error) {
	review, err := parseReview(c.Review)
	if err != nil {
		return sourceReview{}, err
	}
	newer, exists := reviews[c.Source.TaskID]
	if !exists {
		return review, nil
	}
	ledgerReview, err := parseReview(newer)
	if err != nil {
		return sourceReview{}, err
	}
	if overridesCandidateReview(ledgerReview.Status) {
		return ledgerReview, nil
	}
	return review, nil
}
func overridesCandidateReview(status string) bool {
	return approved(status) || status == "pending" || status == "rejected"
}
func reviewedLabel(label Label, review sourceReview) (Label, error) {
	if approved(review.Status) {
		if !validID(reviewProject(review)) {
			return Label{}, invalidSource("approved candidate review requires an exact expected project ID")
		}
		return Label{Status: "approved", Basis: "candidate_review", ProjectID: reviewProject(review)}, nil
	}
	if review.Status == "rejected" || review.Status == "pending" {
		return Label{Status: "blocked", Basis: "candidate_review_" + review.Status}, nil
	}
	return label, nil
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

func conflictingReviewProjects(r sourceReview) bool {
	if r.ExpectedProject == "" || r.ExpectedProjectID == "" {
		return false
	}
	return r.ExpectedProject != r.ExpectedProjectID
}

func (s *sourceData) addUnresolvedTasks(byTask map[string]sourceCandidate) {
	for taskID := range s.blockedTasks {
		if _, exists := byTask[taskID]; !exists {
			s.blockers = append(s.blockers, "source_unresolved_task_identity")
		}
	}
}
