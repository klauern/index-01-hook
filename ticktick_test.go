package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	testTickTickToken = "ticktick-test-token"
	testRecordingHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func fixtureTickTickClient(t *testing.T, transport roundTripFunc) *TickTickClient {
	t.Helper()
	client, err := NewTickTickClient("https://api.ticktick.test/open/v1", testTickTickToken, &http.Client{Transport: transport})
	if err != nil {
		t.Fatalf("NewTickTickClient() error = %v", err)
	}
	return client
}

func fixtureResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestTickTickValidateRoutingAcceptsConfiguredDefaultAndAlias(t *testing.T) {
	client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/open/v1/project" {
			t.Fatalf("request = %s %s, want GET /open/v1/project", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+testTickTickToken {
			t.Fatalf("Authorization = %q", got)
		}
		return fixtureResponse(http.StatusOK, `[
			{"id":"default","name":"Inbox","closed":false,"kind":"TASK","permission":"write"},
			{"id":"home","name":"Home","closed":false,"kind":"TASK"},
			{"id":"work","name":"Work","closed":false,"kind":"TASK","permission":"WRITE"},
			{"id":"owned","name":"Owned","closed":false,"kind":"TASK"},
			{"id":"notes","name":"Notes","closed":false,"kind":"NOTE"}
		]`), nil
	})

	router, err := client.ValidateRouting(context.Background(), TickTickRoutingConfig{
		DefaultProjectID: "default",
		NoteProjectID:    "notes",
		Aliases:          map[string]string{"home": "home", "work": "work", "owned": "owned"},
	})
	if err != nil {
		t.Fatalf("ValidateRouting() error = %v", err)
	}
	if got, err := router.ResolveProject(""); err != nil || got != "default" {
		t.Fatalf("ResolveProject(default) = %q, %v", got, err)
	}
	if got, err := router.ResolveProject(" WORK "); err != nil || got != "work" {
		t.Fatalf("ResolveProject(alias) = %q, %v", got, err)
	}
	if got, err := router.ResolveProject("home"); err != nil || got != "home" {
		t.Fatalf("ResolveProject(home alias) = %q, %v", got, err)
	}
	if got, err := router.ResolveProject("owned"); err != nil || got != "owned" {
		t.Fatalf("ResolveProject(owned alias) = %q, %v", got, err)
	}
	if _, err := router.ResolveProject("default"); tickTickErrorKind(err) != TickTickErrorConfiguration {
		t.Fatalf("ResolveProject(unconfigured ID) error = %v, kind = %q", err, tickTickErrorKind(err))
	}
	if got, err := router.ResolveItemProject(ItemKindNote, ""); err != nil || got != "notes" {
		t.Fatalf("ResolveItemProject(note) = %q, %v", got, err)
	}
	if _, err := router.ResolveItemProject(ItemKindNote, "work"); tickTickErrorKind(err) != TickTickErrorConfiguration {
		t.Fatalf("ResolveItemProject(note alias) error = %v", err)
	}
}

func TestTickTickValidateRoutingAcceptsVirtualInbox(t *testing.T) {
	client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/open/v1/project" {
			t.Fatalf("request = %s %s, want GET /open/v1/project", request.Method, request.URL.Path)
		}
		return fixtureResponse(http.StatusOK, `[
			{"id":"work","name":"Work","closed":false,"kind":"TASK"},
			{"id":"notes","name":"Notes","closed":false,"kind":"NOTE"}
		]`), nil
	})

	router, err := client.ValidateRouting(context.Background(), TickTickRoutingConfig{
		DefaultProjectID: " INBOX ",
		NoteProjectID:    "notes",
		Aliases:          map[string]string{"work": "work"},
	})
	if err != nil {
		t.Fatalf("ValidateRouting() error = %v", err)
	}
	if got, err := router.ResolveProject(""); err != nil || got != tickTickInboxProjectID {
		t.Fatalf("ResolveProject(default) = %q, %v", got, err)
	}
}

func TestTickTickValidateRoutingAlwaysUsesVirtualInbox(t *testing.T) {
	client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
		return fixtureResponse(http.StatusOK, `[
			{"id":"inbox","kind":"NOTE","permission":"read"},
			{"id":"notes","closed":false,"kind":"NOTE","permission":null}
		]`), nil
	})

	router, err := client.ValidateRouting(context.Background(), TickTickRoutingConfig{
		DefaultProjectID: "inbox", NoteProjectID: "notes",
	})
	if err != nil {
		t.Fatalf("ValidateRouting() error = %v", err)
	}
	if got, err := router.ResolveProject(""); err != nil || got != tickTickInboxProjectID {
		t.Fatalf("ResolveProject(default) = %q, %v", got, err)
	}
}

func TestTickTickValidateRoutingRejectsProjectsWithoutClosed(t *testing.T) {
	tests := []struct {
		name     string
		projects string
		config   TickTickRoutingConfig
	}{
		{
			name: "default", projects: `[
				{"id":"default","kind":"TASK","permission":"write"},
				{"id":"notes","closed":false,"kind":"NOTE","permission":"write"}
			]`,
			config: TickTickRoutingConfig{DefaultProjectID: "default", NoteProjectID: "notes"},
		},
		{
			name: "alias", projects: `[
				{"id":"default","closed":false,"kind":"TASK","permission":"write"},
				{"id":"private-alias","kind":"TASK","permission":"write"},
				{"id":"notes","closed":false,"kind":"NOTE","permission":"write"}
			]`,
			config: TickTickRoutingConfig{DefaultProjectID: "default", NoteProjectID: "notes", Aliases: map[string]string{"work": "private-alias"}},
		},
		{
			name: "note", projects: `[
				{"id":"default","closed":false,"kind":"TASK","permission":"write"},
				{"id":"private-notes","kind":"NOTE","permission":"write"}
			]`,
			config: TickTickRoutingConfig{DefaultProjectID: "default", NoteProjectID: "private-notes"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
				return fixtureResponse(http.StatusOK, test.projects), nil
			})
			_, err := client.ValidateRouting(context.Background(), test.config)
			if tickTickErrorKind(err) != TickTickErrorMalformed {
				t.Fatalf("ValidateRouting() error = %v, kind = %q", err, tickTickErrorKind(err))
			}
			for _, privateValue := range []string{"private-alias", "private-notes", testTickTickToken} {
				if strings.Contains(err.Error(), privateValue) {
					t.Fatalf("routing error contains private value %q: %v", privateValue, err)
				}
			}
		})
	}
}

func TestTickTickValidateRoutingRejectsInvalidConfiguredProjects(t *testing.T) {
	projects := `[
		{"id":"valid","closed":false,"kind":"TASK","permission":"write"},
		{"id":"closed","closed":true,"kind":"TASK","permission":"write"},
		{"id":"note","closed":false,"kind":"NOTE","permission":"write"},
		{"id":"closed-note","closed":true,"kind":"NOTE","permission":"write"},
		{"id":"read-only","closed":false,"kind":"TASK","permission":"read"},
		{"id":"read-only-note","closed":false,"kind":"NOTE","permission":"read"}
	]`
	tests := []struct {
		name    string
		config  TickTickRoutingConfig
		wantKey string
	}{
		{name: "missing default", config: TickTickRoutingConfig{DefaultProjectID: "missing", NoteProjectID: "note"}, wantKey: "default"},
		{name: "closed default", config: TickTickRoutingConfig{DefaultProjectID: "closed", NoteProjectID: "note"}, wantKey: "default"},
		{name: "non-task default", config: TickTickRoutingConfig{DefaultProjectID: "note", NoteProjectID: "note"}, wantKey: "default"},
		{name: "read-only default", config: TickTickRoutingConfig{DefaultProjectID: "read-only", NoteProjectID: "note"}, wantKey: "default"},
		{name: "missing alias", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "note", Aliases: map[string]string{"other": "missing"}}, wantKey: "other"},
		{name: "closed alias", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "note", Aliases: map[string]string{"other": "closed"}}, wantKey: "other"},
		{name: "blank alias", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "note", Aliases: map[string]string{" ": "valid"}}, wantKey: "alias"},
		{name: "missing note", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "missing"}, wantKey: "note"},
		{name: "task as note", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "valid"}, wantKey: "note"},
		{name: "closed note", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "closed-note"}, wantKey: "note"},
		{name: "read-only note", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "read-only-note"}, wantKey: "note"},
		{name: "blank note identifier", config: TickTickRoutingConfig{DefaultProjectID: "valid"}, wantKey: "note"},
		{name: "invalid default identifier", config: TickTickRoutingConfig{DefaultProjectID: "bad id", NoteProjectID: "note"}, wantKey: "identifier"},
		{name: "invalid note identifier", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "bad id"}, wantKey: "identifier"},
		{name: "inbox note identifier", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "INBOX"}, wantKey: "identifier"},
		{name: "invalid alias identifier", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "note", Aliases: map[string]string{"other": "bad id"}}, wantKey: "identifier"},
		{name: "inbox alias identifier", config: TickTickRoutingConfig{DefaultProjectID: "valid", NoteProjectID: "note", Aliases: map[string]string{"other": "InBoX"}}, wantKey: "identifier"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
				return fixtureResponse(http.StatusOK, projects), nil
			})
			_, err := client.ValidateRouting(context.Background(), tt.config)
			if tickTickErrorKind(err) != TickTickErrorConfiguration {
				t.Fatalf("ValidateRouting() error = %v, kind = %q", err, tickTickErrorKind(err))
			}
			if !strings.Contains(err.Error(), tt.wantKey) {
				t.Errorf("error = %q, want safe config key %q", err, tt.wantKey)
			}
		})
	}
}

func TestWritableTickTickPermissionAllowsOwnedOrWrite(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{name: "caller-owned null", raw: json.RawMessage(`null`), want: true},
		{name: "shared write", raw: json.RawMessage(`"write"`), want: true},
		{name: "uppercase write", raw: json.RawMessage(`"WRITE"`), want: true},
		{name: "caller-owned omitted field", raw: nil, want: true},
		{name: "empty string", raw: json.RawMessage(`""`)},
		{name: "read", raw: json.RawMessage(`"read"`)},
		{name: "comment", raw: json.RawMessage(`"comment"`)},
		{name: "boolean", raw: json.RawMessage(`true`)},
		{name: "object", raw: json.RawMessage(`{}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := writableTickTickPermission(tt.raw); got != tt.want {
				t.Errorf("writableTickTickPermission() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTickTickCreateTaskNormalizesPayloadAndOmitsUnknownDate(t *testing.T) {
	var payload map[string]any
	client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/open/v1/task" {
			t.Fatalf("request = %s %s, want POST /open/v1/task", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode create payload: %v", err)
		}
		return fixtureResponse(http.StatusCreated, `{"id":"task-1","projectId":"work"}`), nil
	})
	router := &TickTickRouter{client: client, defaultProjectID: "default", aliases: map[string]string{"work": "work"}}
	itemContent := "Buy milk exactly as extracted.\nDo not alter punctuation!"

	created, err := router.CreateTask(context.Background(), "work", TickTickTaskInput{
		Title:         "  Buy milk  ",
		Content:       itemContent,
		RecordingHash: testRecordingHash,
		TaskIndex:     2,
		Priority:      3,
		Tags:          []string{"errands", "voice"},
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	marker := "[index01:" + testRecordingHash + ":2]"
	if created.ID != "task-1" || created.ProjectID != "work" || created.Marker != marker {
		t.Fatalf("created = %+v", created)
	}
	if payload["title"] != "Buy milk" || payload["projectId"] != "work" || payload["priority"] != float64(3) {
		t.Errorf("routing/title/priority payload = %#v", payload)
	}
	content, _ := payload["content"].(string)
	if content != marker+"\n\n"+itemContent {
		t.Errorf("content = %q, want marker plus exact item content", content)
	}
	if got := payload["tags"]; !equalStringSlice(got, []string{"errands", "voice"}) {
		t.Errorf("tags = %#v", got)
	}
	for _, field := range []string{"dueDate", "startDate", "timeZone", "isAllDay"} {
		if _, exists := payload[field]; exists {
			t.Errorf("payload contains conservative-omission field %q: %#v", field, payload[field])
		}
	}
}

func TestTickTickCreateTaskIncludesConfiguredDueTimeZone(t *testing.T) {
	var payload map[string]any
	client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode create payload: %v", err)
		}
		return fixtureResponse(http.StatusCreated, `{"id":"task-1","projectId":"default"}`), nil
	})
	router := &TickTickRouter{client: client, defaultProjectID: "default", aliases: map[string]string{}}
	due := time.Date(2026, time.August, 12, 9, 30, 0, 0, time.FixedZone("PDT", -7*60*60))

	_, err := router.CreateTask(context.Background(), "", TickTickTaskInput{
		Title: "Timed task", RecordingHash: testRecordingHash, Due: &due,
		TimeZone: "America/Los_Angeles",
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if payload["dueDate"] != due.Format(time.RFC3339) {
		t.Errorf("dueDate = %#v, want %q", payload["dueDate"], due.Format(time.RFC3339))
	}
	if payload["timeZone"] != "America/Los_Angeles" {
		t.Errorf("timeZone = %#v, want America/Los_Angeles", payload["timeZone"])
	}
	if allDay, exists := payload["isAllDay"]; !exists || allDay != false {
		t.Errorf("isAllDay = %#v, want false", allDay)
	}
}

func TestTickTickCreateTaskResponseClassification(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		transport  error
		wantKind   TickTickErrorKind
		wantTaskID string
	}{
		{name: "ok 200", status: http.StatusOK, body: `{"id":"task-200","projectId":"default"}`, wantTaskID: "task-200"},
		{name: "ok 201", status: http.StatusCreated, body: `{"id":"task-201","projectId":"default"}`, wantTaskID: "task-201"},
		{name: "malformed success json", status: http.StatusOK, body: `{`, wantKind: TickTickErrorAmbiguous},
		{name: "malformed success missing id", status: http.StatusCreated, body: `{}`, wantKind: TickTickErrorAmbiguous},
		{name: "bad request", status: http.StatusBadRequest, body: `{"error":"secret transcript"}`, wantKind: TickTickErrorMalformed},
		{name: "unauthorized", status: http.StatusUnauthorized, wantKind: TickTickErrorAuthentication},
		{name: "forbidden", status: http.StatusForbidden, wantKind: TickTickErrorAuthentication},
		{name: "not found", status: http.StatusNotFound, wantKind: TickTickErrorConfiguration},
		{name: "conflict", status: http.StatusConflict, wantKind: TickTickErrorAmbiguous},
		{name: "rate limit", status: http.StatusTooManyRequests, wantKind: TickTickErrorAmbiguous},
		{name: "server error", status: http.StatusBadGateway, wantKind: TickTickErrorAmbiguous},
		{name: "timeout", transport: context.DeadlineExceeded, wantKind: TickTickErrorAmbiguous},
		{name: "network", transport: errors.New("connection reset"), wantKind: TickTickErrorAmbiguous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
				if tt.transport != nil {
					return nil, tt.transport
				}
				return fixtureResponse(tt.status, tt.body), nil
			})
			router := &TickTickRouter{client: client, defaultProjectID: "default", aliases: map[string]string{}}
			created, err := router.CreateTask(context.Background(), "", TickTickTaskInput{
				Title:         "Private title",
				Content:       "secret item content",
				RecordingHash: testRecordingHash,
				TaskIndex:     0,
			})
			if tt.wantTaskID != "" {
				if err != nil || created.ID != tt.wantTaskID {
					t.Fatalf("CreateTask() = %+v, %v", created, err)
				}
				return
			}
			if tickTickErrorKind(err) != tt.wantKind {
				t.Fatalf("CreateTask() error = %v, kind = %q, want %q", err, tickTickErrorKind(err), tt.wantKind)
			}
			for _, secret := range []string{testTickTickToken, "secret item content", "Private title"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("safe error contains secret %q: %v", secret, err)
				}
			}
		})
	}
}

func TestTickTickCreateNoteRoutesExplicitNoteAndReadsBack(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":3]"
	content := "Keep this note exactly."
	call := 0
	var payload map[string]any
	client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
		call++
		switch call {
		case 1:
			if request.Method != http.MethodPost || request.URL.Path != "/open/v1/task" {
				t.Fatalf("create request = %s %s", request.Method, request.URL.Path)
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode note payload: %v", err)
			}
			return fixtureResponse(http.StatusCreated, `{"id":"note-1","projectId":"notes"}`), nil
		case 2:
			if request.Method != http.MethodGet || request.URL.Path != "/open/v1/project/notes/task/note-1" {
				t.Fatalf("read request = %s %s", request.Method, request.URL.Path)
			}
			return fixtureResponse(http.StatusOK, `{"id":"note-1","projectId":"notes","kind":"NOTE","title":"Field note","content":"`+marker+`\n\n`+content+`"}`), nil
		default:
			t.Fatalf("unexpected request %d", call)
			return nil, nil
		}
	})
	router := &TickTickRouter{client: client, defaultProjectID: "tasks", noteProjectID: "notes", aliases: map[string]string{"notes": "model-project"}}

	created, err := router.CreateNote(context.Background(), TickTickNoteInput{
		Title: "  Field note  ", Content: content, RecordingHash: testRecordingHash, TaskIndex: 3,
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	if created.ID != "note-1" || created.ProjectID != "notes" || created.Marker != marker {
		t.Fatalf("CreateNote() = %+v", created)
	}
	if len(payload) != 4 || payload["kind"] != "NOTE" || payload["projectId"] != "notes" || payload["title"] != "Field note" {
		t.Errorf("note payload = %#v", payload)
	}
	if payload["content"] != marker+"\n\n"+content {
		t.Errorf("note content = %q", payload["content"])
	}
	for _, field := range []string{"priority", "tags", "dueDate", "startDate", "timeZone", "isAllDay"} {
		if _, exists := payload[field]; exists {
			t.Errorf("note payload contains task-only field %q", field)
		}
	}
}

func TestTickTickCreateNoteRequiresVerifiedReadBack(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":1]"
	tests := []struct {
		name     string
		readBody string
		readErr  error
		wantKind TickTickErrorKind
	}{
		{name: "invalid read JSON", readBody: `{`, wantKind: TickTickErrorAmbiguous},
		{name: "wrong item kind", readBody: `{"id":"note-1","projectId":"notes","kind":"TEXT","content":"` + marker + `"}`, wantKind: TickTickErrorAmbiguous},
		{name: "wrong item identifier", readBody: `{"id":"other","projectId":"notes","kind":"NOTE","content":"` + marker + `"}`, wantKind: TickTickErrorAmbiguous},
		{name: "wrong project identifier", readBody: `{"id":"note-1","projectId":"other","kind":"NOTE","content":"` + marker + `"}`, wantKind: TickTickErrorAmbiguous},
		{name: "marker is only a substring", readBody: `{"id":"note-1","projectId":"notes","kind":"NOTE","content":"prefix ` + marker + ` suffix"}`, wantKind: TickTickErrorAmbiguous},
		{name: "read transport is ambiguous", readErr: errors.New("private network detail"), wantKind: TickTickErrorAmbiguous},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := 0
			client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
				call++
				if call == 1 {
					return fixtureResponse(http.StatusCreated, `{"id":"note-1","projectId":"notes"}`), nil
				}
				if tt.readErr != nil {
					return nil, tt.readErr
				}
				return fixtureResponse(http.StatusOK, tt.readBody), nil
			})
			router := &TickTickRouter{client: client, noteProjectID: "notes"}
			_, err := router.CreateNote(context.Background(), TickTickNoteInput{
				Title: "Private note", Content: "private item content", RecordingHash: testRecordingHash, TaskIndex: 1,
			})
			if tickTickErrorKind(err) != tt.wantKind {
				t.Fatalf("CreateNote() error = %v, kind = %q", err, tickTickErrorKind(err))
			}
			for _, secret := range []string{testTickTickToken, "Private note", "private item content", "private network detail"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("safe error contains private value %q: %v", secret, err)
				}
			}
		})
	}
}

func TestTickTickCreateNoteTreatsUnconfirmedCreateAsAmbiguous(t *testing.T) {
	tests := []struct {
		name      string
		transport error
		body      string
	}{
		{name: "transport failure", transport: errors.New("request failed")},
		{name: "invalid success response", body: `{`},
		{name: "missing success identifier", body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
				if tt.transport != nil {
					return nil, tt.transport
				}
				return fixtureResponse(http.StatusCreated, tt.body), nil
			})
			router := &TickTickRouter{client: client, noteProjectID: "notes"}
			_, err := router.CreateNote(context.Background(), TickTickNoteInput{
				Title: "Note", Content: "content", RecordingHash: testRecordingHash, TaskIndex: 0,
			})
			if tickTickErrorKind(err) != TickTickErrorAmbiguous {
				t.Fatalf("CreateNote() error = %v, kind = %q", err, tickTickErrorKind(err))
			}
		})
	}
}

func TestTickTickCreateNoteTreatsReadBackStatusAsAmbiguous(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			call := 0
			client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
				call++
				if call == 1 {
					return fixtureResponse(http.StatusCreated, `{"id":"note-1","projectId":"notes"}`), nil
				}
				return fixtureResponse(status, `{"error":"private item content"}`), nil
			})
			router := &TickTickRouter{client: client, noteProjectID: "notes"}
			_, err := router.CreateNote(context.Background(), TickTickNoteInput{
				Title: "Private note", Content: "private item content", RecordingHash: testRecordingHash, TaskIndex: 0,
			})
			if tickTickErrorKind(err) != TickTickErrorAmbiguous {
				t.Fatalf("CreateNote() error = %v, kind = %q", err, tickTickErrorKind(err))
			}
			if strings.Contains(err.Error(), "private item content") {
				t.Errorf("safe error contains item content: %v", err)
			}
		})
	}
}

func TestTickTickReconcileUsesOneExactMarkerLine(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":4]"
	expectedContent := marker + "\\n\\nexpected content"
	tests := []struct {
		name       string
		tasks      string
		wantStatus TickTickReconciliationStatus
		wantID     string
	}{
		{name: "one exact match", tasks: `[{"id":"match","kind":"TEXT","title":"Expected title","content":"` + expectedContent + `"},{"id":"other","kind":"TEXT","title":"Other","content":"none"}]`, wantStatus: TickTickReconciliationConfirmed, wantID: "match"},
		{name: "multiple exact matches", tasks: `[{"id":"one","kind":"TEXT","title":"Expected title","content":"` + expectedContent + `"},{"id":"two","kind":"text","title":"Expected title","content":"` + expectedContent + `"}]`, wantStatus: TickTickReconciliationReview},
		{name: "zero exact matches", tasks: `[{"id":"substring","kind":"TEXT","title":"Expected title","content":"prefix ` + marker + ` suffix"}]`, wantStatus: TickTickReconciliationUnconfirmed},
		{name: "wrong title is review", tasks: `[{"id":"spoof","kind":"TEXT","title":"Unrelated","content":"` + expectedContent + `"}]`, wantStatus: TickTickReconciliationReview},
		{name: "wrong content is review", tasks: `[{"id":"spoof","kind":"TEXT","title":"Expected title","content":"` + marker + `\n\nunrelated"}]`, wantStatus: TickTickReconciliationReview},
		{name: "note kind is ignored", tasks: `[{"id":"note","kind":"NOTE","title":"Expected title","content":"` + expectedContent + `"}]`, wantStatus: TickTickReconciliationUnconfirmed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet || request.URL.Path != "/open/v1/project/work/data" {
					t.Fatalf("request = %s %s", request.Method, request.URL.Path)
				}
				return fixtureResponse(http.StatusOK, `{"tasks":`+tt.tasks+`}`), nil
			})
			router := &TickTickRouter{client: client, defaultProjectID: "default", aliases: map[string]string{"work": "work"}}
			result, err := router.ReconcileItem(context.Background(), TickTickReconciliationInput{
				Kind: ItemKindTask, ProjectAlias: "work", Marker: marker,
				Title: "Expected title", Content: "expected content",
			})
			if err != nil {
				t.Fatalf("Reconcile() error = %v", err)
			}
			if result.Status != tt.wantStatus || result.TaskID != tt.wantID {
				t.Errorf("Reconcile() = %+v, want status %q ID %q", result, tt.wantStatus, tt.wantID)
			}
		})
	}
}

func TestTickTickReconcileNoteRequiresNoteKindAndExactMarker(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":5]"
	expectedContent := marker + "\\n\\ncontent"
	tests := []struct {
		name       string
		tasks      string
		wantStatus TickTickReconciliationStatus
		wantID     string
	}{
		{name: "one note match ignores text item", tasks: `[{"id":"text","kind":"TEXT","title":"Expected note","content":"` + expectedContent + `"},{"id":"note","kind":"NOTE","title":"Expected note","content":"` + expectedContent + `"}]`, wantStatus: TickTickReconciliationConfirmed, wantID: "note"},
		{name: "multiple note matches require review", tasks: `[{"id":"one","kind":"NOTE","title":"Expected note","content":"` + expectedContent + `"},{"id":"two","kind":"note","title":"Expected note","content":"` + expectedContent + `"}]`, wantStatus: TickTickReconciliationReview},
		{name: "text match is unconfirmed", tasks: `[{"id":"text","kind":"TEXT","title":"Expected note","content":"` + expectedContent + `"}]`, wantStatus: TickTickReconciliationUnconfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path != "/open/v1/project/notes/data" {
					t.Fatalf("request path = %q", request.URL.Path)
				}
				return fixtureResponse(http.StatusOK, `{"tasks":`+tt.tasks+`}`), nil
			})
			router := &TickTickRouter{client: client, noteProjectID: "notes"}
			result, err := router.ReconcileItem(context.Background(), TickTickReconciliationInput{
				Kind: ItemKindNote, Marker: marker, Title: "Expected note", Content: "content",
			})
			if err != nil {
				t.Fatalf("ReconcileItem() error = %v", err)
			}
			if result.Status != tt.wantStatus || result.TaskID != tt.wantID {
				t.Errorf("ReconcileItem() = %+v", result)
			}
		})
	}
}

func TestTickTickReconcileRejectsMissingTaskList(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":6]"
	for _, body := range []string{`{}`, `{"tasks":null}`} {
		client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
			return fixtureResponse(http.StatusOK, body), nil
		})
		router := &TickTickRouter{client: client, defaultProjectID: "default"}
		_, err := router.ReconcileItem(context.Background(), TickTickReconciliationInput{
			Kind: ItemKindTask, Marker: marker, Title: "Expected", Content: "content",
		})
		if tickTickErrorKind(err) != TickTickErrorMalformed {
			t.Fatalf("ReconcileItem(%s) error = %v", body, err)
		}
	}
}

func equalStringSlice(value any, want []string) bool {
	items, ok := value.([]any)
	if !ok || len(items) != len(want) {
		return false
	}
	for i := range want {
		if items[i] != want[i] {
			return false
		}
	}
	return true
}

func tickTickErrorKind(err error) TickTickErrorKind {
	var typed *TickTickError
	if errors.As(err, &typed) {
		return typed.Kind
	}
	return ""
}

func TestTickTickListProjectSummariesRedactsAndSorts(t *testing.T) {
	client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/open/v1/project" {
			t.Fatalf("request = %s %s, want GET /open/v1/project", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+testTickTickToken {
			t.Fatalf("Authorization = %q", got)
		}
		return fixtureResponse(http.StatusOK, `[
			{"id":"z-project","name":"Private Z","content":"secret body","closed":true,"kind":"NOTE","permission":"read"},
			{"id":"a-project","name":"Private A","closed":false,"kind":"TASK","permission":null},
			{"id":"m-project","name":"Private M","closed":false,"kind":"TASK","permission":"write"},
			{"id":"r-project","name":"Private R","closed":false,"kind":"NOTE","permission":"comment"}
		]`), nil
	})

	summaries, err := client.ListProjectSummaries(context.Background())
	if err != nil {
		t.Fatalf("ListProjectSummaries() error = %v", err)
	}
	encoded, err := json.Marshal(summaries)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	const want = `[{"id":"a-project","kind":"TASK","closed":false,"writable":true},{"id":"m-project","kind":"TASK","closed":false,"writable":true},{"id":"r-project","kind":"NOTE","closed":false,"writable":false},{"id":"z-project","kind":"NOTE","closed":true,"writable":false}]`
	if string(encoded) != want {
		t.Fatalf("summaries = %s, want %s", encoded, want)
	}
	for _, prohibited := range []string{"Private A", "Private Z", "secret body", "read", "comment", testTickTickToken} {
		if strings.Contains(string(encoded), prohibited) {
			t.Errorf("summary output contains prohibited value %q: %s", prohibited, encoded)
		}
	}
}

func TestTickTickListProjectSummariesRejectsInvalidIdentifiersAndKinds(t *testing.T) {
	for _, body := range []string{
		`[{"id":"","closed":false,"kind":"TASK","permission":null}]`,
		`[{"id":"   ","closed":false,"kind":"TASK","permission":null}]`,
		`[{"id":"bad id","closed":false,"kind":"TASK","permission":null}]`,
		`[{"id":" project","closed":false,"kind":"TASK","permission":null}]`,
		`[{"id":"project ","closed":false,"kind":"TASK","permission":null}]`,
		`[{"id":"project","closed":false,"kind":"task","permission":null}]`,
		`[{"id":"project","closed":false,"kind":" TASK","permission":null}]`,
		`[{"id":"project","closed":false,"kind":"TASK ","permission":null}]`,
		`[{"id":"project","closed":false,"kind":"TEXT","permission":null}]`,
		`[{"id":"project","closed":false,"kind":"TASK","permission":null},{"id":"project","closed":false,"kind":"NOTE","permission":null}]`,
	} {
		client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
			return fixtureResponse(http.StatusOK, body), nil
		})
		_, err := client.ListProjectSummaries(context.Background())
		if tickTickErrorKind(err) != TickTickErrorMalformed {
			t.Errorf("ListProjectSummaries(%s) error = %v, kind = %q", body, err, tickTickErrorKind(err))
		}
	}
}

func TestTickTickListProjectSummariesClassifiesAuthenticationAndMalformedResponses(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantKind TickTickErrorKind
	}{
		{name: "authentication", status: http.StatusUnauthorized, body: `{"error":"private provider body"}`, wantKind: TickTickErrorAuthentication},
		{name: "malformed JSON", status: http.StatusOK, body: `{`, wantKind: TickTickErrorMalformed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
				return fixtureResponse(tt.status, tt.body), nil
			})
			_, err := client.ListProjectSummaries(context.Background())
			if tickTickErrorKind(err) != tt.wantKind {
				t.Fatalf("ListProjectSummaries() error = %v, kind = %q, want %q", err, tickTickErrorKind(err), tt.wantKind)
			}
			if strings.Contains(err.Error(), "private provider body") || strings.Contains(err.Error(), testTickTickToken) {
				t.Errorf("error contains private value: %v", err)
			}
		})
	}
}

func TestTickTickListProjectSummariesRejectsMissingClosed(t *testing.T) {
	client := fixtureTickTickClient(t, func(*http.Request) (*http.Response, error) {
		return fixtureResponse(http.StatusOK, `[{
			"id":"private-project-id","kind":"TASK","permission":null
		}]`), nil
	})
	_, err := client.ListProjectSummaries(context.Background())
	if tickTickErrorKind(err) != TickTickErrorMalformed {
		t.Fatalf("ListProjectSummaries() error = %v, kind = %q", err, tickTickErrorKind(err))
	}
	if strings.Contains(err.Error(), "private-project-id") {
		t.Fatalf("error exposes project identifier: %v", err)
	}
}

func TestTickTickListProjectSummariesFailureErrorsArePrivate(t *testing.T) {
	const (
		privateURL  = "https://private.example.test/secret"
		privateBody = "private response body for private project name"
		privateName = "private project name"
	)
	tests := []struct {
		name      string
		ctx       func() context.Context
		transport roundTripFunc
		wantKind  TickTickErrorKind
	}{
		{
			name: "context cancellation",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			transport: func(request *http.Request) (*http.Response, error) {
				return nil, request.Context().Err()
			},
			wantKind: TickTickErrorRetryable,
		},
		{
			name: "transport failure",
			ctx:  context.Background,
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New(privateURL + ": connection failed")
			},
			wantKind: TickTickErrorRetryable,
		},
		{
			name: "http 5xx",
			ctx:  context.Background,
			transport: func(*http.Request) (*http.Response, error) {
				return fixtureResponse(http.StatusBadGateway, privateBody), nil
			},
			wantKind: TickTickErrorRetryable,
		},
		{
			name: "redirect",
			ctx:  context.Background,
			transport: func(*http.Request) (*http.Response, error) {
				response := fixtureResponse(http.StatusFound, privateBody)
				response.Header.Set("Location", privateURL)
				return response, nil
			},
			wantKind: TickTickErrorMalformed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, tt.transport)
			_, err := client.ListProjectSummaries(tt.ctx())
			if tickTickErrorKind(err) != tt.wantKind {
				t.Fatalf("ListProjectSummaries() error = %v, kind = %q, want %q", err, tickTickErrorKind(err), tt.wantKind)
			}
			for _, private := range []string{privateURL, privateBody, testTickTickToken, privateName} {
				if strings.Contains(err.Error(), private) {
					t.Errorf("error contains private value %q: %v", private, err)
				}
			}
		})
	}
}
