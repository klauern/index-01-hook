package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type collectionFixtureResponse struct {
	status int
	body   any
}

func collectionFixtureClient(t *testing.T, responses map[string]collectionFixtureResponse) (*TickTickClient, *[]string) {
	t.Helper()
	requests := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("collector attempted %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		response, ok := responses[r.URL.Path]
		if !ok {
			t.Errorf("unexpected collector path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if response.body != nil {
			if err := json.NewEncoder(w).Encode(response.body); err != nil {
				t.Error(err)
			}
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewTickTickClient(server.URL, "fixture-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client, &requests
}

func collectionEvidenceStore(t *testing.T) (*Store, string) {
	t.Helper()
	store, _ := configureEvidenceStore(t, 24*time.Hour)
	saveQueueRecording(t, store, "Move this synthetic task")
	freezeQueueItems(t, store, "worker", []QueuedItem{{Kind: ItemKindTask, Title: "Synthetic task"}})
	completeEvidenceDelivery(t, store)
	marker, err := tickTickMarker(queueTestHash, 0)
	if err != nil {
		t.Fatal(err)
	}
	return store, marker
}

func collectionResponses(tasks ...evaluationRemoteTask) map[string]collectionFixtureResponse {
	if tasks == nil {
		tasks = []evaluationRemoteTask{}
	}
	return map[string]collectionFixtureResponse{
		"/project":                       {body: []any{map[string]any{"id": "project-1", "name": "Home"}, map[string]any{"id": "project-2", "name": "Work"}}},
		"/project/inbox/data":            {body: map[string]any{"tasks": []any{}}},
		"/project/project-1/data":        {body: map[string]any{"tasks": []any{}}},
		"/project/project-2/data":        {body: map[string]any{"tasks": tasks}},
		"/project/project-1/task/task-1": {status: http.StatusNotFound},
	}
}

func TestEvaluationCollectionMovedTask(t *testing.T) {
	store, marker := collectionEvidenceStore(t)
	remote := evaluationRemoteTask{ID: "task-1", ProjectID: "project-2", Title: "Synthetic task", Kind: "TEXT", Content: "Synthetic details\n" + marker, ModifiedAt: "2026-09-08T12:00:00+0000"}
	client, requests := collectionFixtureClient(t, collectionResponses(remote))
	count, err := collectEvaluationObservations(context.Background(), store, client)
	if err != nil || count != 1 {
		t.Fatalf("collection count=%d error=%v", count, err)
	}
	delivery := readEvidence(t, store)[0].Deliveries[0]
	if delivery.ProjectID != "project-1" || delivery.Observation == nil || delivery.Observation.Status != "verified" || delivery.Observation.ProjectID != "project-2" || delivery.Observation.Marker != marker {
		t.Fatalf("move evidence did not retain original and current destinations: %+v", delivery)
	}
	if len(*requests) != 4 {
		t.Fatalf("expected project list and three project reads, got %v", *requests)
	}
}

func TestEvaluationCollectionWithoutDeliveriesMakesNoRequests(t *testing.T) {
	for _, captureInput := range []bool{false, true} {
		store, _ := configureEvidenceStore(t, 24*time.Hour)
		if captureInput {
			saveQueueRecording(t, store, "Synthetic pending input")
		}
		client, requests := collectionFixtureClient(t, nil)
		count, err := collectEvaluationObservations(context.Background(), store, client)
		if err != nil || count != 0 || len(*requests) != 0 {
			t.Fatalf("undelivered evidence caused a provider request: count=%d error=%v requests=%v", count, err, *requests)
		}
	}
}

func TestEvaluationCollectionAmbiguousMarkersAndDuplicates(t *testing.T) {
	for _, scenario := range []string{"missing marker", "marker embedded in text", "duplicate task", "extra marker"} {
		t.Run(scenario, func(t *testing.T) {
			store, marker := collectionEvidenceStore(t)
			task := evaluationRemoteTask{ID: "task-1", ProjectID: "project-2", Title: "Synthetic task", Kind: "TEXT", Content: marker}
			tasks := []evaluationRemoteTask{task}
			switch scenario {
			case "missing marker":
				tasks[0].Content = "Synthetic details without a marker"
			case "marker embedded in text":
				tasks[0].Content = "Copied text " + marker + " with a suffix"
			case "duplicate task":
				tasks = append(tasks, task)
			case "extra marker":
				tasks[0].Description = "[index01:" + strings.Repeat("c", 64) + ":0]"
			}
			client, _ := collectionFixtureClient(t, collectionResponses(tasks...))
			count, err := collectEvaluationObservations(context.Background(), store, client)
			if err != nil || count != 1 {
				t.Fatalf("collection count=%d error=%v", count, err)
			}
			observation := readEvidence(t, store)[0].Deliveries[0].Observation
			if observation == nil || observation.Status != "ambiguous" || observation.ProjectID != "" {
				t.Fatalf("ambiguous identity supplied a route: %+v", observation)
			}
		})
	}
}

func TestEvaluationCollectionFailuresPreserveObservation(t *testing.T) {
	for _, scenario := range []string{"project authentication", "partial project failure", "direct lookup authentication", "malformed project response"} {
		t.Run(scenario, func(t *testing.T) {
			store, marker := collectionEvidenceStore(t)
			prior := EvaluationObservation{TaskID: "task-1", Marker: marker, ProjectID: "project-1", Title: "Synthetic task", Kind: "TEXT", Status: "verified", ObservedAt: time.Now().UTC().Add(-time.Hour)}
			if err := store.SaveEvaluationObservations(context.Background(), []EvaluationObservation{prior}); err != nil {
				t.Fatal(err)
			}
			before := readEvidence(t, store)[0].Deliveries[0].Observation
			responses := collectionResponses()
			switch scenario {
			case "project authentication":
				responses["/project"] = collectionFixtureResponse{status: http.StatusUnauthorized}
			case "partial project failure":
				responses["/project/project-1/data"] = collectionFixtureResponse{body: map[string]any{"tasks": []evaluationRemoteTask{{ID: "task-1", ProjectID: "project-1", Content: marker}}}}
				responses["/project/project-2/data"] = collectionFixtureResponse{status: http.StatusServiceUnavailable}
			case "direct lookup authentication":
				responses["/project/project-1/task/task-1"] = collectionFixtureResponse{status: http.StatusUnauthorized}
			case "malformed project response":
				responses["/project/project-2/data"] = collectionFixtureResponse{body: map[string]any{"tasks": nil}}
			}
			client, _ := collectionFixtureClient(t, responses)
			count, err := collectEvaluationObservations(context.Background(), store, client)
			if err == nil || count != 0 {
				t.Fatalf("failed poll succeeded: count=%d error=%v", count, err)
			}
			if after := readEvidence(t, store)[0].Deliveries[0].Observation; !reflect.DeepEqual(before, after) {
				t.Fatalf("failed poll overwrote prior verified observation: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestEvaluationCollectionMissingAndCompletedTask(t *testing.T) {
	for _, scenario := range []string{"missing", "completed", "wrong task ID", "invalid project ID"} {
		t.Run(scenario, func(t *testing.T) {
			store, marker := collectionEvidenceStore(t)
			responses := collectionResponses()
			if scenario != "missing" {
				task := evaluationRemoteTask{ID: "task-1", ProjectID: "project-1", Title: "Synthetic task", Kind: "TEXT", Description: marker}
				if scenario == "wrong task ID" {
					task.ID = "another-task"
				}
				if scenario == "invalid project ID" {
					task.ProjectID = "bad id"
				}
				responses["/project/project-1/task/task-1"] = collectionFixtureResponse{body: task}
			}
			client, requests := collectionFixtureClient(t, responses)
			_, err := collectEvaluationObservations(context.Background(), store, client)
			if scenario == "invalid project ID" {
				if err == nil {
					t.Fatal("malformed project ID accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			observation := readEvidence(t, store)[0].Deliveries[0].Observation
			want := "missing"
			if scenario == "completed" {
				want = "verified"
			}
			if observation == nil || observation.Status != want {
				t.Fatalf("observation=%+v want status=%s", observation, want)
			}
			if len(*requests) != 5 || (*requests)[4] != "GET /project/project-1/task/task-1" {
				t.Fatalf("known project lookup missing: %v", *requests)
			}
		})
	}
}

func TestEvaluationCollectionKeepsMovedTaskAfterCompletion(t *testing.T) {
	store, marker := collectionEvidenceStore(t)
	task := evaluationRemoteTask{ID: "task-1", ProjectID: "project-2", Title: "Synthetic task", Kind: "TEXT", Content: marker}
	client, _ := collectionFixtureClient(t, collectionResponses(task))
	if _, err := collectEvaluationObservations(context.Background(), store, client); err != nil {
		t.Fatal(err)
	}
	responses := collectionResponses()
	responses["/project/project-2/task/task-1"] = collectionFixtureResponse{body: task}
	client, requests := collectionFixtureClient(t, responses)
	if _, err := collectEvaluationObservations(context.Background(), store, client); err != nil {
		t.Fatal(err)
	}
	observation := readEvidence(t, store)[0].Deliveries[0].Observation
	if observation == nil || observation.Status != "verified" || observation.ProjectID != "project-2" {
		t.Fatalf("completion lost move evidence: %+v", observation)
	}
	if len(*requests) != 5 || (*requests)[4] != "GET /project/project-2/task/task-1" {
		t.Fatalf("did not prefer last observed destination: %v", *requests)
	}
}

func TestEvaluationCollectionRejectsMalformedProjectIdentity(t *testing.T) {
	store, _ := collectionEvidenceStore(t)
	responses := collectionResponses()
	responses["/project"] = collectionFixtureResponse{body: []any{map[string]any{"id": "bad id"}}}
	responses["/project/bad id/data"] = collectionFixtureResponse{body: map[string]any{"tasks": []any{}}}
	client, _ := collectionFixtureClient(t, responses)
	if _, err := collectEvaluationObservations(context.Background(), store, client); err == nil {
		t.Fatal("malformed project-list ID accepted")
	}
}
