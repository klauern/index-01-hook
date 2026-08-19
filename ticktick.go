package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxTickTickResponseBytes = 1 << 20
	maxTickTickTitleBytes    = 500
	maxTickTickContentBytes  = 64 << 10
	maxTickTickTags          = 10
	maxTickTickTagBytes      = 100
	tickTickInboxProjectID   = "inbox"
)

type TickTickErrorKind string

const (
	TickTickErrorAuthentication TickTickErrorKind = "authentication"
	TickTickErrorConfiguration  TickTickErrorKind = "configuration"
	TickTickErrorRetryable      TickTickErrorKind = "retryable"
	TickTickErrorMalformed      TickTickErrorKind = "malformed"
	TickTickErrorAmbiguous      TickTickErrorKind = "ambiguous"
)

// TickTickError deliberately retains only an operation, classification, HTTP
// status, and safe detail. Provider bodies, credentials, task content, and
// transport error strings must never be copied into it.
type TickTickError struct {
	Kind       TickTickErrorKind
	Operation  string
	StatusCode int
	Detail     string
}

func (e *TickTickError) Error() string {
	message := "ticktick " + e.Operation + " failed: " + string(e.Kind)
	if e.StatusCode != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.StatusCode)
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message
}

type TickTickClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type TickTickRoutingConfig struct {
	DefaultProjectID string
	NoteProjectID    string
	Aliases          map[string]string
}

// TickTickRouter can only route to the reserved Inbox or destinations that
// ValidateRouting observed as open and writable projects of the required kind.
// ResolveProject consumes an alias, never a provider project ID supplied by
// model output.
type TickTickRouter struct {
	client           *TickTickClient
	defaultProjectID string
	noteProjectID    string
	aliases          map[string]string
}

type TickTickTaskInput struct {
	Title         string
	Content       string
	RecordingHash string
	TaskIndex     int
	Priority      int
	Tags          []string
	Due           *time.Time
	AllDay        bool
	TimeZone      string
}

type TickTickNoteInput struct {
	Title         string
	Content       string
	RecordingHash string
	TaskIndex     int
}

type TickTickCreatedTask struct {
	ID        string
	ProjectID string
	Marker    string
}

type TickTickReconciliationStatus string

const (
	TickTickReconciliationConfirmed   TickTickReconciliationStatus = "confirmed"
	TickTickReconciliationReview      TickTickReconciliationStatus = "review"
	TickTickReconciliationUnconfirmed TickTickReconciliationStatus = "unconfirmed"
)

type TickTickReconciliationResult struct {
	Status    TickTickReconciliationStatus
	TaskID    string
	ProjectID string
}

type TickTickReconciliationInput struct {
	Kind         ItemKind
	ProjectAlias string
	Marker       string
	Title        string
	Content      string
}

type tickTickProject struct {
	ID            string          `json:"id"`
	Closed        bool            `json:"closed"`
	Kind          string          `json:"kind"`
	Permission    json.RawMessage `json:"permission"`
	closedPresent bool
}

func (p *tickTickProject) UnmarshalJSON(data []byte) error {
	var fields struct {
		ID         string          `json:"id"`
		Closed     json.RawMessage `json:"closed"`
		Kind       string          `json:"kind"`
		Permission json.RawMessage `json:"permission"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	p.ID = fields.ID
	p.Kind = fields.Kind
	p.Permission = fields.Permission
	p.closedPresent = fields.Closed != nil
	if !p.closedPresent {
		p.Closed = false
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(fields.Closed), []byte("null")) {
		return errors.New("closed field is null")
	}
	return json.Unmarshal(fields.Closed, &p.Closed)
}

// TickTickProjectSummary contains only fields that are safe for operator output.
// It excludes project names and provider permission values.
type TickTickProjectSummary struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Closed   bool   `json:"closed"`
	Writable bool   `json:"writable"`
}

type tickTickCreatePayload struct {
	Title     string   `json:"title"`
	ProjectID string   `json:"projectId"`
	Content   string   `json:"content"`
	Priority  int      `json:"priority"`
	Tags      []string `json:"tags,omitempty"`
	DueDate   string   `json:"dueDate,omitempty"`
	IsAllDay  *bool    `json:"isAllDay,omitempty"`
	TimeZone  string   `json:"timeZone,omitempty"`
}

type tickTickNoteCreatePayload struct {
	Title     string `json:"title"`
	ProjectID string `json:"projectId"`
	Content   string `json:"content"`
	Kind      string `json:"kind"`
}

type tickTickTaskResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Kind      string `json:"kind"`
}

type tickTickProjectData struct {
	Tasks []tickTickTaskResponse `json:"tasks"`
}

func NewTickTickClient(baseURL, token string, httpClient *http.Client) (*TickTickClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, &TickTickError{Kind: TickTickErrorConfiguration, Operation: "configure client", Detail: "base URL is invalid"}
	}
	if parsed.Scheme != "https" {
		return nil, &TickTickError{Kind: TickTickErrorConfiguration, Operation: "configure client", Detail: "base URL must use HTTPS"}
	}
	if strings.TrimSpace(token) == "" {
		return nil, &TickTickError{Kind: TickTickErrorConfiguration, Operation: "configure client", Detail: "token is required"}
	}
	if httpClient == nil {
		return nil, &TickTickError{Kind: TickTickErrorConfiguration, Operation: "configure client", Detail: "HTTP client is required"}
	}
	isolatedClient := *httpClient
	isolatedClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &TickTickClient{baseURL: baseURL, token: token, httpClient: &isolatedClient}, nil
}

func (c *TickTickClient) ValidateRouting(ctx context.Context, config TickTickRoutingConfig) (*TickTickRouter, error) {
	defaultID := strings.TrimSpace(config.DefaultProjectID)
	if defaultID == "" {
		return nil, configurationError("validate routing", "default project is required")
	}
	if strings.EqualFold(defaultID, tickTickInboxProjectID) {
		defaultID = tickTickInboxProjectID
	}
	if !safeProviderIdentifier(defaultID) {
		return nil, configurationError("validate routing", "default project identifier is invalid")
	}
	noteID := strings.TrimSpace(config.NoteProjectID)
	if noteID == "" {
		return nil, configurationError("validate routing", "note project is required")
	}
	if !safeProviderIdentifier(noteID) || strings.EqualFold(noteID, tickTickInboxProjectID) {
		return nil, configurationError("validate routing", "note project identifier is invalid")
	}

	aliases := make(map[string]string, len(config.Aliases))
	for rawAlias, rawProjectID := range config.Aliases {
		alias := strings.ToLower(strings.TrimSpace(rawAlias))
		projectID := strings.TrimSpace(rawProjectID)
		if alias == "" {
			return nil, configurationError("validate routing", "alias must not be blank")
		}
		if projectID == "" {
			return nil, configurationError("validate routing", "project for alias "+alias+" is required")
		}
		if !safeProviderIdentifier(projectID) || strings.EqualFold(projectID, tickTickInboxProjectID) {
			return nil, configurationError("validate routing", "project identifier for alias "+alias+" is invalid")
		}
		if _, duplicate := aliases[alias]; duplicate {
			return nil, configurationError("validate routing", "alias "+alias+" is duplicated")
		}
		aliases[alias] = projectID
	}

	projects, err := c.listProjects(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]tickTickProject, len(projects))
	for _, project := range projects {
		if defaultID == tickTickInboxProjectID && strings.EqualFold(strings.TrimSpace(project.ID), tickTickInboxProjectID) {
			continue
		}
		if !project.closedPresent {
			return nil, malformedError("list projects", "project response has no closed field")
		}
		if strings.TrimSpace(project.ID) == "" {
			return nil, malformedError("list projects", "project response contains an empty ID")
		}
		if _, duplicate := byID[project.ID]; duplicate {
			return nil, malformedError("list projects", "project response contains a duplicate ID")
		}
		byID[project.ID] = project
	}
	if defaultID == tickTickInboxProjectID {
		byID[tickTickInboxProjectID] = tickTickProject{
			ID: tickTickInboxProjectID, Kind: "TASK", closedPresent: true,
		}
	}
	if err := validateTickTickDestination(byID, defaultID, "default", "TASK"); err != nil {
		return nil, err
	}
	for alias, projectID := range aliases {
		if err := validateTickTickDestination(byID, projectID, alias, "TASK"); err != nil {
			return nil, err
		}
	}
	if err := validateTickTickDestination(byID, noteID, "note", "NOTE"); err != nil {
		return nil, err
	}
	return &TickTickRouter{client: c, defaultProjectID: defaultID, noteProjectID: noteID, aliases: aliases}, nil
}

func validateTickTickDestination(projects map[string]tickTickProject, projectID, routeName, projectKind string) error {
	project, exists := projects[projectID]
	if !exists {
		return configurationError("validate routing", routeName+" project is unavailable")
	}
	if project.Closed {
		return configurationError("validate routing", routeName+" project is closed")
	}
	if !strings.EqualFold(project.Kind, projectKind) {
		return configurationError("validate routing", routeName+" project has an invalid kind")
	}
	if !writableTickTickPermission(project.Permission) {
		return configurationError("validate routing", routeName+" project is not writable")
	}
	return nil
}

func writableTickTickPermission(raw json.RawMessage) bool {
	if len(raw) == 0 {
		// TickTick omits permission for projects owned by the caller.
		return true
	}
	if string(raw) == "null" {
		return true
	}
	var permission string
	return json.Unmarshal(raw, &permission) == nil && strings.EqualFold(permission, "write")
}

// ResolveItemProject selects a project from stored item data. Note items use
// only the configured note project. The model cannot supply a note project ID.
func (r *TickTickRouter) ResolveItemProject(kind ItemKind, alias string) (string, error) {
	switch kind {
	case ItemKindTask:
		return r.ResolveProject(alias)
	case ItemKindNote:
		if strings.TrimSpace(alias) != "" {
			return "", configurationError("resolve project", "note project alias is not allowed")
		}
		if strings.TrimSpace(r.noteProjectID) == "" {
			return "", configurationError("resolve project", "note project is not configured")
		}
		return r.noteProjectID, nil
	default:
		return "", configurationError("resolve project", "item kind is invalid")
	}
}

func (r *TickTickRouter) ResolveProject(alias string) (string, error) {
	alias = strings.ToLower(strings.TrimSpace(alias))
	if alias == "" {
		return r.defaultProjectID, nil
	}
	projectID, exists := r.aliases[alias]
	if !exists {
		return "", configurationError("resolve project", "project alias is not configured")
	}
	return projectID, nil
}

func (r *TickTickRouter) CreateTask(ctx context.Context, alias string, input TickTickTaskInput) (TickTickCreatedTask, error) {
	projectID, err := r.ResolveProject(alias)
	if err != nil {
		return TickTickCreatedTask{}, err
	}
	payload, marker, err := normalizeTickTickTask(projectID, input)
	if err != nil {
		return TickTickCreatedTask{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return TickTickCreatedTask{}, malformedError("create task", "task payload cannot be encoded")
	}
	request, err := r.client.newRequest(ctx, http.MethodPost, "/task", bytes.NewReader(body))
	if err != nil {
		return TickTickCreatedTask{}, malformedError("create task", "task request cannot be built")
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := r.client.httpClient.Do(request)
	if err != nil {
		// Once RoundTrip starts, a transport error cannot prove the provider did
		// not commit the task. Reconcile the marker before considering a retry.
		return TickTickCreatedTask{}, &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create task"}
	}
	defer ignoreCloseError(response.Body)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		discardResponse(response.Body)
		return TickTickCreatedTask{}, classifyTickTickCreateStatus("create task", response.StatusCode)
	}
	var created tickTickTaskResponse
	if err := decodeTickTickJSON(response.Body, &created); err != nil {
		return TickTickCreatedTask{}, &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create task"}
	}
	if strings.TrimSpace(created.ID) == "" || strings.TrimSpace(created.ProjectID) == "" || created.ProjectID != projectID {
		return TickTickCreatedTask{}, &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create task"}
	}
	return TickTickCreatedTask{ID: created.ID, ProjectID: created.ProjectID, Marker: marker}, nil
}

// CreateNote creates an explicit NOTE item in the configured note project.
// It reads the item back before it confirms delivery.
func (r *TickTickRouter) CreateNote(ctx context.Context, input TickTickNoteInput) (TickTickCreatedTask, error) {
	projectID, err := r.ResolveItemProject(ItemKindNote, "")
	if err != nil {
		return TickTickCreatedTask{}, err
	}
	payload, marker, err := normalizeTickTickNote(projectID, input)
	if err != nil {
		return TickTickCreatedTask{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return TickTickCreatedTask{}, malformedError("create note", "note payload cannot be encoded")
	}
	request, err := r.client.newRequest(ctx, http.MethodPost, "/task", bytes.NewReader(body))
	if err != nil {
		return TickTickCreatedTask{}, malformedError("create note", "note request cannot be built")
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := r.client.httpClient.Do(request)
	if err != nil {
		return TickTickCreatedTask{}, &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create note"}
	}
	defer ignoreCloseError(response.Body)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		discardResponse(response.Body)
		return TickTickCreatedTask{}, classifyTickTickCreateStatus("create note", response.StatusCode)
	}
	var created tickTickTaskResponse
	if err := decodeTickTickJSON(response.Body, &created); err != nil {
		return TickTickCreatedTask{}, &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create note"}
	}
	if strings.TrimSpace(created.ID) == "" || created.ProjectID != projectID {
		return TickTickCreatedTask{}, &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "create note"}
	}
	if err := r.readBackNote(ctx, created.ID, projectID, marker); err != nil {
		return TickTickCreatedTask{}, err
	}
	return TickTickCreatedTask{ID: created.ID, ProjectID: created.ProjectID, Marker: marker}, nil
}

func normalizeTickTickNote(projectID string, input TickTickNoteInput) (tickTickNoteCreatePayload, string, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" || len(title) > maxTickTickTitleBytes {
		return tickTickNoteCreatePayload{}, "", malformedError("normalize note", "title is invalid")
	}
	marker, err := tickTickMarker(input.RecordingHash, input.TaskIndex)
	if err != nil {
		return tickTickNoteCreatePayload{}, "", err
	}
	if len(marker)+2+len(input.Content) > maxTickTickContentBytes {
		return tickTickNoteCreatePayload{}, "", malformedError("normalize note", "content is too large")
	}
	return tickTickNoteCreatePayload{
		Title: title, ProjectID: projectID, Kind: "NOTE",
		Content: marker + "\n\n" + input.Content,
	}, marker, nil
}

func (r *TickTickRouter) readBackNote(ctx context.Context, taskID, projectID, marker string) error {
	path := "/project/" + url.PathEscape(projectID) + "/task/" + url.PathEscape(taskID)
	request, err := r.client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "read note"}
	}
	response, err := r.client.httpClient.Do(request)
	if err != nil {
		return &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "read note"}
	}
	defer ignoreCloseError(response.Body)
	if response.StatusCode != http.StatusOK {
		discardResponse(response.Body)
		return &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "read note", StatusCode: response.StatusCode}
	}
	var item tickTickTaskResponse
	if err := decodeTickTickJSON(response.Body, &item); err != nil {
		return &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "read note"}
	}
	if item.ID != taskID || item.ProjectID != projectID {
		return &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "read note"}
	}
	if !strings.EqualFold(item.Kind, "NOTE") {
		return &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "read note"}
	}
	if !hasExactMarkerLine(item.Content, marker) {
		return &TickTickError{Kind: TickTickErrorAmbiguous, Operation: "read note"}
	}
	return nil
}

func normalizeTickTickTask(projectID string, input TickTickTaskInput) (tickTickCreatePayload, string, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" || len(title) > maxTickTickTitleBytes {
		return tickTickCreatePayload{}, "", malformedError("normalize task", "title is invalid")
	}
	marker, err := tickTickMarker(input.RecordingHash, input.TaskIndex)
	if err != nil {
		return tickTickCreatePayload{}, "", err
	}
	if len(marker)+2+len(input.Content) > maxTickTickContentBytes {
		return tickTickCreatePayload{}, "", malformedError("normalize task", "content is too large")
	}
	if !validTickTickPriority(input.Priority) {
		return tickTickCreatePayload{}, "", malformedError("normalize task", "priority is invalid")
	}
	tags, err := normalizeTickTickTags(input.Tags)
	if err != nil {
		return tickTickCreatePayload{}, "", err
	}
	payload := tickTickCreatePayload{
		Title:     title,
		ProjectID: projectID,
		Content:   marker + "\n\n" + input.Content,
		Priority:  input.Priority,
		Tags:      tags,
	}
	if input.Due != nil {
		allDay := input.AllDay
		payload.DueDate = input.Due.Format(time.RFC3339)
		payload.IsAllDay = &allDay
		payload.TimeZone = strings.TrimSpace(input.TimeZone)
	}
	return payload, marker, nil
}

func tickTickMarker(recordingHash string, taskIndex int) (string, error) {
	recordingHash = strings.ToLower(strings.TrimSpace(recordingHash))
	decoded, err := hex.DecodeString(recordingHash)
	if err != nil || len(decoded) != 32 {
		return "", malformedError("normalize task", "recording hash is invalid")
	}
	if taskIndex < 0 {
		return "", malformedError("normalize task", "task index is invalid")
	}
	return fmt.Sprintf("[index01:%s:%d]", recordingHash, taskIndex), nil
}

func validTickTickPriority(priority int) bool {
	switch priority {
	case 0, 1, 3, 5:
		return true
	default:
		return false
	}

}

func normalizeTickTickTags(tags []string) ([]string, error) {
	if len(tags) > maxTickTickTags {
		return nil, malformedError("normalize task", "too many tags")
	}
	result := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, raw := range tags {
		tag := strings.TrimSpace(raw)
		if tag == "" || len(tag) > maxTickTickTagBytes {
			return nil, malformedError("normalize task", "tag is invalid")
		}
		if _, duplicate := seen[tag]; duplicate {
			continue
		}
		seen[tag] = struct{}{}
		result = append(result, tag)
	}
	return result, nil
}

func (r *TickTickRouter) ReconcileItem(ctx context.Context, input TickTickReconciliationInput) (TickTickReconciliationResult, error) {
	projectID, err := r.ResolveItemProject(input.Kind, input.ProjectAlias)
	if err != nil {
		return TickTickReconciliationResult{}, err
	}
	operation := "reconcile task"
	if input.Kind == ItemKindNote {
		operation = "reconcile note"
	}
	if !validTickTickMarker(input.Marker) {
		return TickTickReconciliationResult{}, malformedError(operation, "marker is invalid")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" || len(title) > maxTickTickTitleBytes {
		return TickTickReconciliationResult{}, malformedError(operation, "title is invalid")
	}
	expectedContent := input.Marker + "\n\n" + input.Content
	if len(expectedContent) > maxTickTickContentBytes {
		return TickTickReconciliationResult{}, malformedError(operation, "content is too large")
	}
	path := "/project/" + url.PathEscape(projectID) + "/data"
	request, err := r.client.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return TickTickReconciliationResult{}, malformedError(operation, "request cannot be built")
	}
	response, err := r.client.httpClient.Do(request)
	if err != nil {
		return TickTickReconciliationResult{}, &TickTickError{Kind: TickTickErrorRetryable, Operation: operation}
	}
	defer ignoreCloseError(response.Body)
	if response.StatusCode != http.StatusOK {
		discardResponse(response.Body)
		return TickTickReconciliationResult{}, classifyTickTickStatus(operation, response.StatusCode)
	}
	var data tickTickProjectData
	if err := decodeTickTickJSON(response.Body, &data); err != nil {
		return TickTickReconciliationResult{}, malformedError(operation, "project response is invalid")
	}
	if data.Tasks == nil {
		return TickTickReconciliationResult{}, malformedError(operation, "project response has no task list")
	}
	markerMatches := 0
	matches := make([]string, 0, 2)
	for _, task := range data.Tasks {
		kindMatches := input.Kind == ItemKindTask && strings.EqualFold(task.Kind, "TEXT") ||
			input.Kind == ItemKindNote && strings.EqualFold(task.Kind, "NOTE")
		if !kindMatches || !hasExactMarkerLine(task.Content, input.Marker) {
			continue
		}
		markerMatches++
		if strings.TrimSpace(task.Title) == title && task.Content == expectedContent {
			matches = append(matches, strings.TrimSpace(task.ID))
		}
	}
	switch {
	case markerMatches == 0:
		return TickTickReconciliationResult{Status: TickTickReconciliationUnconfirmed}, nil
	case markerMatches == 1 && len(matches) == 1:
		if matches[0] == "" {
			return TickTickReconciliationResult{}, malformedError(operation, "matched item has no ID")
		}
		return TickTickReconciliationResult{
			Status: TickTickReconciliationConfirmed, TaskID: matches[0], ProjectID: projectID,
		}, nil
	default:
		return TickTickReconciliationResult{Status: TickTickReconciliationReview}, nil
	}
}

func validTickTickMarker(marker string) bool {
	if marker == "" || strings.TrimSpace(marker) != marker || strings.ContainsAny(marker, "\r\n") {
		return false
	}
	if !strings.HasPrefix(marker, "[index01:") || !strings.HasSuffix(marker, "]") {
		return false
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(marker, "[index01:"), "]"), ":")
	if len(parts) != 2 {
		return false
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	canonical, err := tickTickMarker(parts[0], index)
	return err == nil && canonical == marker
}

func hasExactMarkerLine(content, marker string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSuffix(line, "\r") == marker {
			return true
		}
	}
	return false
}

// ListProjectSummaries returns validated, redacted project metadata for operators.
func (c *TickTickClient) ListProjectSummaries(ctx context.Context) ([]TickTickProjectSummary, error) {
	projects, err := c.listProjects(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]TickTickProjectSummary, 0, len(projects))
	seen := make(map[string]struct{}, len(projects))
	for _, project := range projects {
		if !project.closedPresent {
			return nil, malformedError("list project summaries", "project response has no closed field")
		}
		id := project.ID
		if id != strings.TrimSpace(id) || !safeProviderIdentifier(id) {
			return nil, malformedError("list project summaries", "project identifier is invalid")
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, malformedError("list project summaries", "project identifier is duplicated")
		}
		seen[id] = struct{}{}
		var kind string
		switch project.Kind {
		case "TASK":
			kind = "TASK"
		case "NOTE":
			kind = "NOTE"
		default:
			return nil, malformedError("list project summaries", "project kind is invalid")
		}
		summaries = append(summaries, TickTickProjectSummary{
			ID: id, Kind: kind, Closed: project.Closed, Writable: writableTickTickPermission(project.Permission),
		})
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].ID < summaries[j].ID })
	return summaries, nil
}

func (c *TickTickClient) listProjects(ctx context.Context) ([]tickTickProject, error) {
	request, err := c.newRequest(ctx, http.MethodGet, "/project", nil)
	if err != nil {
		return nil, malformedError("list projects", "request cannot be built")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, &TickTickError{Kind: TickTickErrorRetryable, Operation: "list projects"}
	}
	defer ignoreCloseError(response.Body)
	if response.StatusCode != http.StatusOK {
		discardResponse(response.Body)
		return nil, classifyTickTickStatus("list projects", response.StatusCode)
	}
	var projects []tickTickProject
	if err := decodeTickTickJSON(response.Body, &projects); err != nil || projects == nil {
		return nil, malformedError("list projects", "project response is invalid")
	}
	return projects, nil
}

func (c *TickTickClient) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func decodeTickTickJSON(body io.Reader, destination any) error {
	limited := &io.LimitedReader{R: body, N: maxTickTickResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("response contains trailing data")
	}
	if limited.N <= 0 {
		return fmt.Errorf("response exceeds size limit")
	}
	return nil
}

func discardResponse(body io.Reader) {
	_, _ = io.CopyN(io.Discard, body, maxTickTickResponseBytes+1)
}

func classifyTickTickStatus(operation string, status int) error {
	kind := TickTickErrorMalformed
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		kind = TickTickErrorAuthentication
	case status == http.StatusNotFound:
		kind = TickTickErrorConfiguration
	case status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500:
		kind = TickTickErrorRetryable
	}
	return &TickTickError{Kind: kind, Operation: operation, StatusCode: status}
}

func classifyTickTickCreateStatus(operation string, status int) error {
	err := classifyTickTickStatus(operation, status)
	var typed *TickTickError
	if errors.As(err, &typed) && typed.Kind == TickTickErrorRetryable {
		typed.Kind = TickTickErrorAmbiguous
	}
	return err
}

func configurationError(operation, detail string) error {
	return &TickTickError{Kind: TickTickErrorConfiguration, Operation: operation, Detail: detail}
}

func malformedError(operation, detail string) error {
	return &TickTickError{Kind: TickTickErrorMalformed, Operation: operation, Detail: detail}
}
