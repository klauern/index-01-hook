package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	itemAcceptanceDeepSeekToken = "acceptance-deepseek-token"
	itemAcceptanceTickTickToken = "acceptance-ticktick-token"
	itemAcceptanceTranscript    = "Private synthetic transcription"
)

type itemAcceptanceTickTickTransport struct {
	t               *testing.T
	noteProjectKind string
	providerBody    string
	failNoteCreates int
	projectLists    int
	taskCreates     int
	noteCreates     int
	noteReads       int
	taskPayloads    []tickTickCreatePayload
	notePayloads    []tickTickNoteCreatePayload
	createdNotes    map[string]tickTickTaskResponse
	createdItems    []tickTickTaskResponse
}

func (transport *itemAcceptanceTickTickTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.t.Helper()
	if got := request.Header.Get("Authorization"); got != "Bearer "+itemAcceptanceTickTickToken {
		transport.t.Fatalf("TickTick Authorization = %q", got)
	}

	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/open/v1/project":
		transport.projectLists++
		noteKind := transport.noteProjectKind
		if noteKind == "" {
			noteKind = "NOTE"
		}
		body := fmt.Sprintf(`[
			{"id":"default","closed":false,"kind":"TASK","permission":null},
			{"id":"work","closed":false,"kind":"TASK","permission":"write"},
			{"id":"notes","closed":false,"kind":%q,"permission":null}
		]`, noteKind)
		return fixtureResponse(http.StatusOK, body), nil

	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/open/v1/project/") && strings.HasSuffix(request.URL.Path, "/data"):
		projectID := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/open/v1/project/"), "/data")
		items := make([]tickTickTaskResponse, 0, len(transport.createdItems))
		for _, item := range transport.createdItems {
			if item.ProjectID == projectID {
				items = append(items, item)
			}
		}
		return itemAcceptanceJSONResponse(transport.t, http.StatusOK, tickTickProjectData{Tasks: items}), nil

	case request.Method == http.MethodPost && request.URL.Path == "/open/v1/task":
		body, err := io.ReadAll(request.Body)
		if err != nil {
			transport.t.Fatalf("read TickTick create request: %v", err)
		}
		var itemKind struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(body, &itemKind); err != nil {
			transport.t.Fatalf("decode TickTick create kind: %v", err)
		}
		if strings.EqualFold(itemKind.Kind, "NOTE") {
			return transport.createNote(body)
		}
		return transport.createTask(body)

	case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/open/v1/project/notes/task/"):
		transport.noteReads++
		itemID := strings.TrimPrefix(request.URL.Path, "/open/v1/project/notes/task/")
		created, exists := transport.createdNotes[itemID]
		if !exists {
			transport.t.Fatalf("read unknown synthetic note %q", itemID)
		}
		return itemAcceptanceJSONResponse(transport.t, http.StatusOK, created), nil

	default:
		transport.t.Fatalf("unexpected TickTick request = %s %s", request.Method, request.URL.Path)
		return nil, nil
	}
}

func (transport *itemAcceptanceTickTickTransport) createTask(body []byte) (*http.Response, error) {
	var payload tickTickCreatePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		transport.t.Fatalf("decode TickTick task request: %v", err)
	}
	transport.taskCreates++
	transport.taskPayloads = append(transport.taskPayloads, payload)
	created := tickTickTaskResponse{
		ID:        fmt.Sprintf("task-%d", transport.taskCreates),
		ProjectID: payload.ProjectID,
		Title:     payload.Title,
		Content:   payload.Content,
		Kind:      "TEXT",
	}
	transport.createdItems = append(transport.createdItems, created)
	return transport.createdItemResponse(created), nil
}

func (transport *itemAcceptanceTickTickTransport) createNote(body []byte) (*http.Response, error) {
	var payload tickTickNoteCreatePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		transport.t.Fatalf("decode TickTick note request: %v", err)
	}
	transport.noteCreates++
	transport.notePayloads = append(transport.notePayloads, payload)
	if transport.failNoteCreates > 0 {
		transport.failNoteCreates--
		return fixtureResponse(http.StatusInternalServerError, `{"error":"private synthetic provider body"}`), nil
	}
	created := tickTickTaskResponse{
		ID:        fmt.Sprintf("note-%d", transport.noteCreates),
		ProjectID: payload.ProjectID,
		Title:     payload.Title,
		Content:   payload.Content,
		Kind:      payload.Kind,
	}
	if transport.createdNotes == nil {
		transport.createdNotes = make(map[string]tickTickTaskResponse)
	}
	transport.createdNotes[created.ID] = created
	transport.createdItems = append(transport.createdItems, created)
	return transport.createdItemResponse(created), nil
}

func (transport *itemAcceptanceTickTickTransport) createdItemResponse(created tickTickTaskResponse) *http.Response {
	if transport.providerBody == "" {
		return itemAcceptanceJSONResponse(transport.t, http.StatusCreated, created)
	}
	value := map[string]any{
		"id":             created.ID,
		"projectId":      created.ProjectID,
		"title":          created.Title,
		"content":        created.Content,
		"kind":           created.Kind,
		"private_detail": transport.providerBody,
	}
	return itemAcceptanceJSONResponse(transport.t, http.StatusCreated, value)
}

func itemAcceptanceJSONResponse(t *testing.T, status int, value any) *http.Response {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode synthetic provider response: %v", err)
	}
	return fixtureResponse(status, string(body))
}

type itemAcceptanceEnvironment struct {
	store         *Store
	dbPath        string
	handler       http.Handler
	logger        *slog.Logger
	logs          bytes.Buffer
	clock         *adjustableClock
	deepSeek      *DeepSeekClient
	router        *TickTickRouter
	deepSeekCalls int
	tickTick      *itemAcceptanceTickTickTransport
}

func newItemAcceptanceEnvironment(
	t *testing.T,
	deepSeekOutput string,
	configureTickTick func(*itemAcceptanceTickTickTransport),
) *itemAcceptanceEnvironment {
	t.Helper()
	clock := &adjustableClock{now: time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)}
	dbPath := filepath.Join(t.TempDir(), "acceptance.db")
	store, err := openStore(context.Background(), dbPath, clock.Time)
	if err != nil {
		t.Fatalf("openStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	environment := &itemAcceptanceEnvironment{store: store, dbPath: dbPath, clock: clock}
	environment.logger = slog.New(slog.NewJSONHandler(&environment.logs, nil))
	environment.handler = NewHandler(store, testToken, 1<<20, environment.logger)
	deepSeek, err := NewDeepSeekClient(itemAcceptanceDeepSeekToken, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		environment.deepSeekCalls++
		if request.Method != http.MethodPost || request.URL.String() != deepSeekResponsesURL {
			t.Fatalf("DeepSeek request = %s %s", request.Method, request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+itemAcceptanceDeepSeekToken {
			t.Fatalf("DeepSeek Authorization = %q", got)
		}
		return deepSeekFixtureOutput("acceptance-response", deepSeekOutput), nil
	}), clock.Time)
	if err != nil {
		t.Fatalf("NewDeepSeekClient() error = %v", err)
	}
	environment.deepSeek = deepSeek

	tickTickTransport := &itemAcceptanceTickTickTransport{t: t}
	if configureTickTick != nil {
		configureTickTick(tickTickTransport)
	}
	tickTick, err := NewTickTickClient(
		"https://api.ticktick.test/open/v1",
		itemAcceptanceTickTickToken,
		&http.Client{Transport: tickTickTransport},
	)
	if err != nil {
		t.Fatalf("NewTickTickClient() error = %v", err)
	}
	router, err := tickTick.ValidateRouting(context.Background(), TickTickRoutingConfig{
		DefaultProjectID: "default",
		NoteProjectID:    "notes",
		Aliases:          map[string]string{"work": "work"},
	})
	if err != nil {
		t.Fatalf("ValidateRouting() error = %v", err)
	}
	environment.router = router
	environment.tickTick = tickTickTransport
	return environment
}

func (environment *itemAcceptanceEnvironment) postWebhook(t *testing.T) (*httptest.ResponseRecorder, Receipt) {
	t.Helper()
	request := multipartRequest(t, "item-acceptance-boundary", []multipartPart{
		{name: "recordedAt", value: "1786554000000"},
		{name: "client", value: "ring"},
		{name: "transcription", value: itemAcceptanceTranscript},
	})
	response := send(t, environment.handler, request)
	var receipt Receipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode webhook receipt: %v; body=%s", err, response.Body.String())
	}
	return response, receipt
}

func (environment *itemAcceptanceEnvironment) worker(t *testing.T) *Worker {
	t.Helper()
	worker, err := NewWorker(environment.store, environment.deepSeek, environment.router, WorkerConfig{
		Owner: "item-acceptance-worker", LeaseDuration: time.Minute, PollInterval: time.Millisecond,
		RetryBase: time.Minute, RetryMaximum: 8 * time.Minute,
		ExtractionMaxAttempts: 3, DeliveryMaxAttempts: 3, ReconcileMaxAttempts: 2,
		ProjectAliases: []string{"work"}, Jitter: func(time.Duration) time.Duration { return 0 },
		Logger: environment.logger,
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	return worker
}

func runItemAcceptanceUntilIdle(t *testing.T, worker *Worker) {
	t.Helper()
	for cycle := 0; cycle < 20; cycle++ {
		if !runWorkerOnce(t, worker) {
			return
		}
	}
	t.Fatal("worker did not become idle within 20 cycles")
}

func assertItemAcceptanceStatusRedacted(t *testing.T, status RecordingQueueStatus, privateValues ...string) {
	t.Helper()
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal recording status: %v", err)
	}
	for _, privateValue := range append(privateValues, itemAcceptanceDeepSeekToken, itemAcceptanceTickTickToken) {
		if strings.Contains(string(encoded), privateValue) {
			t.Errorf("recording status contains private value %q: %s", privateValue, encoded)
		}
	}
}

type itemAcceptanceExpectedTask struct {
	index     int
	title     string
	content   string
	projectID string
}

type itemAcceptanceExpectedNote struct {
	index   int
	title   string
	content string
}

func assertItemAcceptancePayloads(
	t *testing.T,
	recordingHash string,
	transport *itemAcceptanceTickTickTransport,
	wantTasks []itemAcceptanceExpectedTask,
	wantNotes []itemAcceptanceExpectedNote,
) {
	t.Helper()
	if len(transport.taskPayloads) != len(wantTasks) || len(transport.notePayloads) != len(wantNotes) {
		t.Fatalf("payload counts = tasks:%d notes:%d, want tasks:%d notes:%d",
			len(transport.taskPayloads), len(transport.notePayloads), len(wantTasks), len(wantNotes))
	}
	for payloadIndex, want := range wantTasks {
		marker, err := tickTickMarker(recordingHash, want.index)
		if err != nil {
			t.Fatalf("build task marker for item %d: %v", want.index, err)
		}
		payload := transport.taskPayloads[payloadIndex]
		if payload.Title != want.title || payload.ProjectID != want.projectID ||
			payload.Content != marker+"\n\n"+want.content {
			t.Errorf("task payload %d = title:%q project:%q content:%q, want title:%q project:%q content:%q",
				payloadIndex, payload.Title, payload.ProjectID, payload.Content,
				want.title, want.projectID, marker+"\n\n"+want.content)
		}
	}
	for payloadIndex, want := range wantNotes {
		marker, err := tickTickMarker(recordingHash, want.index)
		if err != nil {
			t.Fatalf("build note marker for item %d: %v", want.index, err)
		}
		payload := transport.notePayloads[payloadIndex]
		if payload.Title != want.title || payload.ProjectID != "notes" || payload.Kind != "NOTE" ||
			payload.Content != marker+"\n\n"+want.content {
			t.Errorf("note payload %d = title:%q project:%q kind:%q content:%q, want title:%q project:notes kind:NOTE content:%q",
				payloadIndex, payload.Title, payload.ProjectID, payload.Kind, payload.Content,
				want.title, marker+"\n\n"+want.content)
		}
	}
}

func TestItemAcceptanceDeliversSyntheticItemKinds(t *testing.T) {
	tests := []struct {
		name          string
		output        string
		wantKinds     []ItemKind
		wantTasks     []itemAcceptanceExpectedTask
		wantNotes     []itemAcceptanceExpectedNote
		privateValues []string
	}{
		{name: "zero items", output: `{"items":[]}`},
		{
			name: "one task", output: `{"items":[` + validDeepSeekTask("Private task title") + `]}`,
			wantKinds:     []ItemKind{ItemKindTask},
			wantTasks:     []itemAcceptanceExpectedTask{{index: 0, title: "Private task title", projectID: "default"}},
			privateValues: []string{"Private task title"},
		},
		{
			name: "one note", output: `{"items":[` + validDeepSeekNote("Private note title", "Private note content") + `]}`,
			wantKinds:     []ItemKind{ItemKindNote},
			wantNotes:     []itemAcceptanceExpectedNote{{index: 0, title: "Private note title", content: "Private note content"}},
			privateValues: []string{"Private note title", "Private note content"},
		},
		{
			name: "mixed items",
			output: `{"items":[{"kind":"task","title":"Private work task","content":"","due_at":null,"all_day":false,"priority":0,"tags":[],"project_alias":"work"},` +
				validDeepSeekNote("Private mixed note", "Private mixed content") + `]}`,
			wantKinds:     []ItemKind{ItemKindTask, ItemKindNote},
			wantTasks:     []itemAcceptanceExpectedTask{{index: 0, title: "Private work task", projectID: "work"}},
			wantNotes:     []itemAcceptanceExpectedNote{{index: 1, title: "Private mixed note", content: "Private mixed content"}},
			privateValues: []string{"Private work task", "Private mixed note", "Private mixed content"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			environment := newItemAcceptanceEnvironment(t, test.output, nil)
			response, receipt := environment.postWebhook(t)
			if response.Code != http.StatusAccepted || receipt.Duplicate || !receipt.Queued {
				t.Fatalf("webhook response = %d %+v, want new queued receipt", response.Code, receipt)
			}

			runItemAcceptanceUntilIdle(t, environment.worker(t))
			status, err := environment.store.RecordingStatus(context.Background(), receipt.ID)
			if err != nil {
				t.Fatalf("RecordingStatus() error = %v", err)
			}
			if status.State != "complete" || len(status.Tasks) != len(test.wantKinds) {
				t.Fatalf("recording status = %+v", status)
			}
			for index, wantKind := range test.wantKinds {
				if status.Tasks[index].Kind != wantKind || status.Tasks[index].State != "complete" {
					t.Errorf("item %d status = %+v, want complete %q", index, status.Tasks[index], wantKind)
				}
			}
			if environment.deepSeekCalls != 1 || environment.tickTick.projectLists != 1 ||
				environment.tickTick.taskCreates != len(test.wantTasks) ||
				environment.tickTick.noteCreates != len(test.wantNotes) ||
				environment.tickTick.noteReads != len(test.wantNotes) {
				t.Fatalf("provider calls = DeepSeek:%d projects:%d tasks:%d notes:%d note reads:%d",
					environment.deepSeekCalls, environment.tickTick.projectLists,
					environment.tickTick.taskCreates, environment.tickTick.noteCreates,
					environment.tickTick.noteReads)
			}
			assertItemAcceptancePayloads(t, status.RecordingHash, environment.tickTick, test.wantTasks, test.wantNotes)
			assertItemAcceptanceStatusRedacted(t, status, append(test.privateValues, itemAcceptanceTranscript)...)
		})
	}
}

func TestItemAcceptanceExactReplayMakesNoDuplicateProviderCalls(t *testing.T) {
	output := `{"items":[` + validDeepSeekTask("Replay task") + `,` + validDeepSeekNote("Replay note", "Replay content") + `]}`
	environment := newItemAcceptanceEnvironment(t, output, nil)
	firstResponse, firstReceipt := environment.postWebhook(t)
	if firstResponse.Code != http.StatusAccepted {
		t.Fatalf("first webhook status = %d", firstResponse.Code)
	}
	runItemAcceptanceUntilIdle(t, environment.worker(t))

	secondResponse, secondReceipt := environment.postWebhook(t)
	if secondResponse.Code != http.StatusOK || !secondReceipt.Duplicate || secondReceipt.ID != firstReceipt.ID {
		t.Fatalf("replay response = %d %+v, first receipt = %+v", secondResponse.Code, secondReceipt, firstReceipt)
	}
	if worked := runWorkerOnce(t, environment.worker(t)); worked {
		t.Fatal("exact replay created new queue work")
	}
	if environment.deepSeekCalls != 1 || environment.tickTick.taskCreates != 1 ||
		environment.tickTick.noteCreates != 1 || environment.tickTick.noteReads != 1 {
		t.Fatalf("provider calls after replay = DeepSeek:%d tasks:%d notes:%d note reads:%d",
			environment.deepSeekCalls, environment.tickTick.taskCreates,
			environment.tickTick.noteCreates, environment.tickTick.noteReads)
	}
	status, err := environment.store.RecordingStatus(context.Background(), firstReceipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus(replay) error = %v", err)
	}
	assertItemAcceptancePayloads(t, status.RecordingHash, environment.tickTick,
		[]itemAcceptanceExpectedTask{{index: 0, title: "Replay task", projectID: "default"}},
		[]itemAcceptanceExpectedNote{{index: 1, title: "Replay note", content: "Replay content"}},
	)
	var receiveCount int
	if err := environment.store.db.QueryRow(`SELECT receive_count FROM recordings WHERE id = ?`, firstReceipt.ID).Scan(&receiveCount); err != nil {
		t.Fatalf("query receive count: %v", err)
	}
	if receiveCount != 2 {
		t.Fatalf("receive_count = %d, want 2", receiveCount)
	}
}

func TestItemAcceptanceRedactsCapturedLogsAndStatus(t *testing.T) {
	const (
		providerBody = "private-provider-body-sentinel"
		modelOutput  = "private-model-output-sentinel"
	)
	environment := newItemAcceptanceEnvironment(t,
		`{"items":[`+validDeepSeekTask(modelOutput)+`]}`,
		func(transport *itemAcceptanceTickTickTransport) {
			transport.providerBody = providerBody
		},
	)

	response, receipt := environment.postWebhook(t)
	if response.Code != http.StatusAccepted || receipt.ID == 0 || receipt.Duplicate || !receipt.Queued {
		t.Fatalf("webhook response = %d %+v, want new queued receipt", response.Code, receipt)
	}
	runItemAcceptanceUntilIdle(t, environment.worker(t))

	status, err := environment.store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.State != "complete" || len(status.Tasks) != 1 || status.Tasks[0].State != "complete" {
		t.Fatalf("recording status = %+v, want one complete task", status)
	}
	if environment.deepSeekCalls != 1 || environment.tickTick.taskCreates != 1 {
		t.Fatalf("provider calls = DeepSeek:%d TickTick:%d, want 1 and 1",
			environment.deepSeekCalls, environment.tickTick.taskCreates)
	}
	assertItemAcceptancePayloads(t, status.RecordingHash, environment.tickTick,
		[]itemAcceptanceExpectedTask{{index: 0, title: modelOutput, projectID: "default"}}, nil)

	encodedStatus, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal recording status: %v", err)
	}
	logs := environment.logs.String()
	if !strings.Contains(logs, "recorded webhook") {
		t.Fatalf("captured logs do not contain the safe receipt event: %s", logs)
	}
	observability := logs + "\n" + string(encodedStatus)
	privateValues := map[string]string{
		"webhook token":                 testToken,
		"webhook Authorization header":  "Bearer " + testToken,
		"DeepSeek token":                itemAcceptanceDeepSeekToken,
		"DeepSeek Authorization header": "Bearer " + itemAcceptanceDeepSeekToken,
		"TickTick token":                itemAcceptanceTickTickToken,
		"TickTick Authorization header": "Bearer " + itemAcceptanceTickTickToken,
		"transcript":                    itemAcceptanceTranscript,
		"provider body":                 providerBody,
		"model output":                  modelOutput,
	}
	for name, privateValue := range privateValues {
		if strings.Contains(observability, privateValue) {
			t.Errorf("captured logs or status contain %s %q: %s", name, privateValue, observability)
		}
	}
}

func TestItemAcceptanceRestartDoesNotRepeatCompletedSibling(t *testing.T) {
	output := `{"items":[` + validDeepSeekTask("Completed sibling") + `,` + validDeepSeekNote("Restarted note", "Restarted content") + `]}`
	environment := newItemAcceptanceEnvironment(t, output, func(transport *itemAcceptanceTickTickTransport) {
		transport.failNoteCreates = 1
	})
	_, receipt := environment.postWebhook(t)
	firstWorker := environment.worker(t)
	runWorkerOnce(t, firstWorker)
	runWorkerOnce(t, firstWorker)
	runWorkerOnce(t, firstWorker)

	status, err := environment.store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus(partial) error = %v", err)
	}
	if status.State != "extracted" || status.Tasks[0].State != "complete" || status.Tasks[1].State != "retry_wait" {
		t.Fatalf("partial status = %+v", status)
	}
	if environment.tickTick.taskCreates != 1 || environment.tickTick.noteCreates != 1 {
		t.Fatalf("partial create calls = tasks:%d notes:%d", environment.tickTick.taskCreates, environment.tickTick.noteCreates)
	}
	assertItemAcceptancePayloads(t, status.RecordingHash, environment.tickTick,
		[]itemAcceptanceExpectedTask{{index: 0, title: "Completed sibling", projectID: "default"}},
		[]itemAcceptanceExpectedNote{{index: 1, title: "Restarted note", content: "Restarted content"}},
	)

	if err := environment.store.Close(); err != nil {
		t.Fatalf("close Store before restart: %v", err)
	}
	restartedStore, err := openStore(context.Background(), environment.dbPath, environment.clock.Time)
	if err != nil {
		t.Fatalf("reopen Store after restart: %v", err)
	}
	t.Cleanup(func() { _ = restartedStore.Close() })

	restartedDeepSeekCalls := 0
	restartedDeepSeek, err := NewDeepSeekClient(itemAcceptanceDeepSeekToken, roundTripFunc(func(*http.Request) (*http.Response, error) {
		restartedDeepSeekCalls++
		t.Fatal("restarted worker repeated extraction")
		return nil, nil
	}), environment.clock.Time)
	if err != nil {
		t.Fatalf("reconstruct DeepSeek client: %v", err)
	}
	restartedTickTick := &itemAcceptanceTickTickTransport{t: t}
	restartedClient, err := NewTickTickClient(
		"https://api.ticktick.test/open/v1",
		itemAcceptanceTickTickToken,
		&http.Client{Transport: restartedTickTick},
	)
	if err != nil {
		t.Fatalf("reconstruct TickTick client: %v", err)
	}
	restartedRouter, err := restartedClient.ValidateRouting(context.Background(), TickTickRoutingConfig{
		DefaultProjectID: "default",
		NoteProjectID:    "notes",
		Aliases:          map[string]string{"work": "work"},
	})
	if err != nil {
		t.Fatalf("reconstruct TickTick router: %v", err)
	}
	restartedWorker := newTestWorker(t, restartedStore, restartedDeepSeek, restartedRouter)

	environment.clock.Advance(time.Minute)
	runItemAcceptanceUntilIdle(t, restartedWorker)
	status, err = restartedStore.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus(restarted) error = %v", err)
	}
	if status.State != "complete" || status.Tasks[0].State != "complete" || status.Tasks[1].State != "complete" {
		t.Fatalf("restarted status = %+v", status)
	}
	if environment.deepSeekCalls != 1 || restartedDeepSeekCalls != 0 ||
		environment.tickTick.taskCreates != 1 || environment.tickTick.noteCreates != 1 ||
		restartedTickTick.projectLists != 1 || restartedTickTick.taskCreates != 0 ||
		restartedTickTick.noteCreates != 1 || restartedTickTick.noteReads != 1 {
		t.Fatalf("restart provider calls = initial DeepSeek:%d tasks:%d notes:%d; restarted DeepSeek:%d projects:%d tasks:%d notes:%d note reads:%d",
			environment.deepSeekCalls, environment.tickTick.taskCreates, environment.tickTick.noteCreates,
			restartedDeepSeekCalls, restartedTickTick.projectLists, restartedTickTick.taskCreates,
			restartedTickTick.noteCreates, restartedTickTick.noteReads)
	}
	assertItemAcceptancePayloads(t, status.RecordingHash, restartedTickTick, nil, []itemAcceptanceExpectedNote{{
		index: 1, title: "Restarted note", content: "Restarted content",
	}})
	assertItemAcceptanceStatusRedacted(t, status,
		itemAcceptanceTranscript, "Completed sibling", "Restarted note", "Restarted content", "private synthetic provider body")
}

func TestItemAcceptanceRejectsInvalidKindBeforeFreeze(t *testing.T) {
	output := `{"items":[{"kind":"event","title":"Private invalid event","content":"Private invalid content"}]}`
	environment := newItemAcceptanceEnvironment(t, output, nil)
	_, receipt := environment.postWebhook(t)
	runItemAcceptanceUntilIdle(t, environment.worker(t))

	status, err := environment.store.RecordingStatus(context.Background(), receipt.ID)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.State != "dead_letter" || status.LastClassification != string(OutcomeMalformed) || len(status.Tasks) != 0 {
		t.Fatalf("invalid kind status = %+v", status)
	}
	var extractions, deliveries int
	if err := environment.store.db.QueryRow(`SELECT count(*) FROM extractions WHERE recording_id = ?`, receipt.ID).Scan(&extractions); err != nil {
		t.Fatalf("count extractions: %v", err)
	}
	if err := environment.store.db.QueryRow(`SELECT count(*) FROM delivery_tasks WHERE recording_id = ?`, receipt.ID).Scan(&deliveries); err != nil {
		t.Fatalf("count delivery items: %v", err)
	}
	if extractions != 0 || deliveries != 0 || environment.deepSeekCalls != 1 ||
		environment.tickTick.taskCreates != 0 || environment.tickTick.noteCreates != 0 {
		t.Fatalf("invalid kind persistence/calls = extractions:%d deliveries:%d DeepSeek:%d tasks:%d notes:%d",
			extractions, deliveries, environment.deepSeekCalls,
			environment.tickTick.taskCreates, environment.tickTick.noteCreates)
	}
	assertItemAcceptanceStatusRedacted(t, status,
		itemAcceptanceTranscript, "Private invalid event", "Private invalid content")
}

func TestItemAcceptanceRejectsInvalidNoteProjectConfiguration(t *testing.T) {
	transport := &itemAcceptanceTickTickTransport{t: t, noteProjectKind: "TASK"}
	client, err := NewTickTickClient(
		"https://api.ticktick.test/open/v1",
		itemAcceptanceTickTickToken,
		&http.Client{Transport: transport},
	)
	if err != nil {
		t.Fatalf("NewTickTickClient() error = %v", err)
	}
	router, err := client.ValidateRouting(context.Background(), TickTickRoutingConfig{
		DefaultProjectID: "default",
		NoteProjectID:    "notes",
		Aliases:          map[string]string{"work": "work"},
	})
	if router != nil || tickTickErrorKind(err) != TickTickErrorConfiguration {
		t.Fatalf("ValidateRouting(invalid note project) = %+v, %v", router, err)
	}
	if transport.projectLists != 1 || transport.taskCreates != 0 || transport.noteCreates != 0 {
		t.Fatalf("invalid configuration provider calls = projects:%d tasks:%d notes:%d",
			transport.projectLists, transport.taskCreates, transport.noteCreates)
	}
	if strings.Contains(err.Error(), itemAcceptanceTickTickToken) {
		t.Fatalf("configuration error contains token: %v", err)
	}
}
