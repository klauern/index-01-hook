package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
)

// Evaluation types stay in test files. Experimental actions cannot enter the worker.
const evaluationVersion = 1

type evaluationItem struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Route    string   `json:"route"`
	Due      string   `json:"due"`
	AllDay   bool     `json:"all_day"`
	Priority int      `json:"priority"`
	Tags     []string `json:"tags"`
	Closed   bool     `json:"closed"`
}

type evaluationAction struct {
	Step   int               `json:"step"`
	Op     string            `json:"op"`
	Target string            `json:"target"`
	Item   *evaluationItem   `json:"item,omitempty"`
	Fields map[string]string `json:"fields,omitempty"`
}

type evaluationMessage struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	RecordedAt string `json:"recorded_at"`
}

type evaluationTurn struct {
	Message evaluationMessage `json:"message"`
	// A snapshot replaces item state, but does not replace message history.
	RemoteState *[]evaluationItem `json:"remote_state,omitempty"`
	Output      json.RawMessage   `json:"output"`
}

type evaluationForbidden struct {
	Op     string `json:"op"`
	Target string `json:"target"`
}

type evaluationCase struct {
	Version         int                   `json:"version"`
	ID              string                `json:"id"`
	Suite           string                `json:"suite"`
	Split           string                `json:"split"`
	Adapter         string                `json:"adapter"`
	Clock           string                `json:"clock"`
	TimeZone        string                `json:"time_zone"`
	Aliases         []string              `json:"aliases"`
	HistoryLimit    int                   `json:"history_limit"`
	History         []evaluationMessage   `json:"history"`
	Initial         []evaluationItem      `json:"initial"`
	Turns           []evaluationTurn      `json:"turns"`
	ExpectedActions []evaluationAction    `json:"expected_actions"`
	ExpectedState   []evaluationItem      `json:"expected_state"`
	ExpectedVisible [][]string            `json:"expected_visible"`
	Forbidden       []evaluationForbidden `json:"forbidden"`
	Live            bool                  `json:"live"`
}

type evaluationObservation struct {
	Outcome string             `json:"outcome"`
	Actions []evaluationAction `json:"actions"`
	State   []evaluationItem   `json:"state"`
	Visible [][]string         `json:"visible"`
	Reason  string             `json:"reason,omitempty"`
}

type evaluationScore struct {
	Status       string   `json:"status"`
	Actions      string   `json:"actions"`
	State        string   `json:"state"`
	History      string   `json:"history"`
	Semantic     string   `json:"semantic"`
	FailureCount int      `json:"failure_count"`
	Diagnostics  []string `json:"diagnostics"`
}

func decodeEvaluation(data []byte, value any) error {
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing data")
	}
	return nil
}

func loadEvaluationCases(path string) ([]evaluationCase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cases []evaluationCase
	if err := decodeEvaluation(data, &cases); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, errors.New("empty case set")
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if err := validateEvaluationCase(c); err != nil {
			return nil, fmt.Errorf("%s: %w", c.ID, err)
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("duplicate case %s", c.ID)
		}
		seen[c.ID] = true
	}
	return cases, nil
}

func validateEvaluationState(state []evaluationItem) error {
	if state == nil {
		return errors.New("state is missing")
	}
	seen := map[string]bool{}
	for _, item := range state {
		if item.ID == "" || seen[item.ID] || item.Title == "" || item.Route == "" || item.Tags == nil {
			return errors.New("invalid item or duplicate identifier")
		}
		if item.Kind != "task" && item.Kind != "note" {
			return errors.New("invalid item kind")
		}
		if !slices.Contains([]int{0, 1, 3, 5}, item.Priority) || (item.AllDay && item.Due == "") {
			return errors.New("invalid task fields")
		}
		if item.Due != "" {
			if _, err := time.Parse(time.RFC3339, item.Due); err != nil {
				return errors.New("invalid due date")
			}
		}
		if item.Kind == "note" && (item.Due != "" || item.AllDay || item.Priority != 0 || len(item.Tags) != 0 || item.Closed) {
			return errors.New("note contains task fields")
		}
		seen[item.ID] = true
	}
	return nil
}

func validateEvaluationCase(c evaluationCase) error {
	if c.Version != evaluationVersion || c.ID == "" || !slices.Contains([]string{"development", "held_out"}, c.Split) {
		return errors.New("invalid version, identifier, or split")
	}
	if (c.Suite != "current" || c.Adapter != "production-v1") && (c.Suite != "proposed" || c.Adapter != "scripted-state-v1") {
		return errors.New("invalid suite or adapter")
	}
	if _, err := time.Parse(time.RFC3339, c.Clock); err != nil {
		return errors.New("invalid clock")
	}
	if c.TimeZone == "" {
		return errors.New("time zone is missing")
	}
	if _, err := time.LoadLocation(c.TimeZone); err != nil {
		return errors.New("invalid time zone")
	}
	if c.Aliases == nil || c.History == nil || c.Forbidden == nil || c.HistoryLimit < 1 || len(c.Turns) == 0 ||
		c.ExpectedActions == nil || c.ExpectedVisible == nil || len(c.ExpectedVisible) != len(c.Turns) {
		return errors.New("required scoring input is missing")
	}
	if err := validateEvaluationState(c.Initial); err != nil {
		return err
	}
	if err := validateEvaluationState(c.ExpectedState); err != nil {
		return err
	}
	if c.Suite == "current" && (len(c.History) != 0 || len(c.Initial) != 0 || c.HistoryLimit != 1) {
		return errors.New("production extraction has no history or initial item state")
	}
	seen := map[string]bool{}
	messages := slices.Clone(c.History)
	for _, turn := range c.Turns {
		messages = append(messages, turn.Message)
		if len(turn.Output) == 0 || string(turn.Output) == "null" {
			return errors.New("scripted output is missing")
		}
		if turn.RemoteState != nil {
			if c.Suite == "current" {
				return errors.New("production extraction has no remote snapshot")
			}
			if err := validateEvaluationState(*turn.RemoteState); err != nil {
				return err
			}
		}
	}
	for _, msg := range messages {
		if msg.ID == "" || seen[msg.ID] || strings.TrimSpace(msg.Text) == "" {
			return errors.New("invalid or duplicate message")
		}
		if _, err := time.Parse(time.RFC3339, msg.RecordedAt); err != nil {
			return errors.New("invalid recording time")
		}
		seen[msg.ID] = true
	}
	for _, ids := range c.ExpectedVisible {
		if ids == nil {
			return errors.New("expected history is missing")
		}
	}
	for _, a := range c.ExpectedActions {
		if a.Step < 1 || a.Step > len(c.Turns) {
			return errors.New("expected action has invalid step")
		}
	}
	for _, f := range c.Forbidden {
		if !slices.Contains([]string{"create", "update", "close", "review", "no_action"}, f.Op) {
			return errors.New("invalid forbidden operation")
		}
	}
	state := copyEvaluationState(c.Initial)
	position := 0
	for step, turn := range c.Turns {
		if turn.RemoteState != nil {
			state = copyEvaluationState(*turn.RemoteState)
		}
		for position < len(c.ExpectedActions) && c.ExpectedActions[position].Step == step+1 {
			action := c.ExpectedActions[position]
			if c.Suite == "current" && action.Op != "create" {
				return errors.New("production adapter supports only creation")
			}
			for _, f := range c.Forbidden {
				if action.Op == f.Op && (f.Target == "*" || f.Target == action.Target) {
					return errors.New("expected action is forbidden")
				}
			}
			var err error
			state, err = applyEvaluationAction(state, action)
			if err != nil {
				return fmt.Errorf("invalid expected action: %w", err)
			}
			position++
		}
	}
	if position != len(c.ExpectedActions) {
		return errors.New("expected actions are out of order")
	}
	// Item order is not meaningful. Action order remains strict.
	if !cmp.Equal(orderedEvaluationState(state), orderedEvaluationState(c.ExpectedState)) {
		return errors.New("expected state disagrees with expected actions")
	}
	return nil
}

func orderedEvaluationState(items []evaluationItem) []evaluationItem {
	items = copyEvaluationState(items)
	slices.SortFunc(items, func(a, b evaluationItem) int { return strings.Compare(a.ID, b.ID) })
	return items
}

func copyEvaluationState(items []evaluationItem) []evaluationItem {
	out := make([]evaluationItem, len(items))
	copy(out, items)
	return out
}

func applyEvaluationAction(state []evaluationItem, a evaluationAction) ([]evaluationItem, error) {
	state = copyEvaluationState(state)
	index := slices.IndexFunc(state, func(i evaluationItem) bool { return i.ID == a.Target })
	switch a.Op {
	case "create":
		if a.Item == nil || a.Target != a.Item.ID || index >= 0 || len(a.Fields) != 0 {
			return state, errors.New("invalid create or duplicate identifier")
		}
		state = append(state, *a.Item)
	case "update", "close":
		if index < 0 || a.Item != nil || state[index].Closed {
			return state, errors.New("mutation target is absent or closed")
		}
		if a.Op == "close" {
			if state[index].Kind != "task" || len(a.Fields) != 0 {
				return state, errors.New("invalid closure")
			}
			state[index].Closed = true
			break
		}
		if len(a.Fields) == 0 {
			return state, errors.New("update fields are missing")
		}
		for field, value := range a.Fields {
			switch field {
			case "title":
				state[index].Title = value
			case "content":
				state[index].Content = value
			case "route":
				state[index].Route = value
			case "due":
				state[index].Due = value
			default:
				return state, errors.New("unsupported update field")
			}
		}
	case "review", "no_action":
		if a.Target != "" || a.Item != nil || len(a.Fields) != 0 {
			return state, errors.New("non-mutation action contains item fields")
		}
	default:
		return state, errors.New("unknown operation")
	}
	return state, validateEvaluationState(state)
}

// Structural checks do not grade paraphrases or factual meaning.
func scoreEvaluation(c evaluationCase, o evaluationObservation) evaluationScore {
	s := evaluationScore{Status: "error", Actions: "skip", State: "skip", History: "skip", Semantic: "unsupported", Diagnostics: []string{}}
	if err := validateEvaluationCase(c); err != nil {
		s.Diagnostics = append(s.Diagnostics, "invalid fixture: "+err.Error())
		return s
	}
	fail := func(message string) {
		s.FailureCount++
		s.Diagnostics = append(s.Diagnostics, message)
	}
	for _, action := range o.Actions {
		for _, forbidden := range c.Forbidden {
			if action.Op == forbidden.Op && (forbidden.Target == "*" || action.Target == forbidden.Target) {
				fail(fmt.Sprintf("forbidden %s on %s at step %d", action.Op, action.Target, action.Step))
			}
		}
	}
	if o.Outcome != "ok" {
		s.Status = o.Outcome
		if !slices.Contains([]string{"error", "skip", "unsupported"}, o.Outcome) {
			s.Status = "error"
		}
		s.Diagnostics = append(s.Diagnostics, o.Reason)
		if s.FailureCount > 0 {
			s.Status = "fail"
		}
		return s
	}
	if o.Actions == nil || o.State == nil || o.Visible == nil {
		s.Diagnostics = append(s.Diagnostics, "required observation is missing")
		if s.FailureCount > 0 {
			s.Status = "fail"
		}
		return s
	}
	s.Actions, s.State, s.History = "pass", "pass", "pass"
	if diff := cmp.Diff(c.ExpectedActions, o.Actions); diff != "" {
		s.Actions = "fail"
		fail("actions (-want +got):\n" + diff)
	}
	// Replay independently to detect an invalid sequence even when final state matches.
	state := copyEvaluationState(c.Initial)
	position := 0
	for step, turn := range c.Turns {
		if turn.RemoteState != nil {
			state = copyEvaluationState(*turn.RemoteState)
		}
		for position < len(o.Actions) && o.Actions[position].Step == step+1 {
			var err error
			state, err = applyEvaluationAction(state, o.Actions[position])
			if err != nil {
				s.Actions = "fail"
				fail(err.Error())
			}
			position++
		}
	}
	if position != len(o.Actions) {
		s.Actions = "fail"
		fail("action step is invalid or out of order")
	}
	if err := validateEvaluationState(o.State); err != nil {
		s.State = "fail"
		fail(err.Error())
	}
	for _, check := range []struct {
		name string
		want []evaluationItem
	}{{"final state", c.ExpectedState}, {"replayed state", state}} {
		if diff := cmp.Diff(orderedEvaluationState(check.want), orderedEvaluationState(o.State)); diff != "" {
			s.State = "fail"
			fail(check.name + " (-want +got):\n" + diff)
		}
	}
	if diff := cmp.Diff(c.ExpectedVisible, o.Visible); diff != "" {
		s.History = "fail"
		fail("visible messages (-want +got):\n" + diff)
	}
	s.Status = "pass"
	if s.FailureCount > 0 {
		s.Status = "fail"
	}
	return s
}
