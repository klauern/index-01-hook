package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUncertainTickTickCreateReconcilesBeforeAnotherPost(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		transport error
	}{
		{name: "malformed success", status: http.StatusCreated, body: `{`},
		{name: "conflict", status: http.StatusConflict},
		{name: "timeout", transport: context.DeadlineExceeded},
		{name: "server error", status: http.StatusBadGateway},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, clock := newQueueStore(t)
			receipt := saveQueueRecording(t, store, "unrelated private transcription")
			freezeQueueItems(t, store, "extractor", []QueuedItem{{
				Kind: ItemKindTask, Title: "Expected task", Content: "frozen task content", Priority: 0,
			}})

			calls := make([]string, 0, 3)
			postCount := 0
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				switch request.Method {
				case http.MethodPost:
					calls = append(calls, "POST")
					postCount++
					var payload tickTickCreatePayload
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatalf("decode task payload: %v", err)
					}
					if !strings.HasSuffix(payload.Content, "\n\nfrozen task content") || strings.Contains(payload.Content, "unrelated private transcription") {
						t.Fatalf("task content = %q", payload.Content)
					}
					if postCount == 1 {
						if test.transport != nil {
							return nil, test.transport
						}
						return fixtureResponse(test.status, test.body), nil
					}
					return fixtureResponse(http.StatusCreated, `{"id":"task-one","projectId":"default"}`), nil
				case http.MethodGet:
					calls = append(calls, "GET")
					return fixtureResponse(http.StatusOK, `{"tasks":[]}`), nil
				default:
					t.Fatalf("unexpected TickTick method %q", request.Method)
					return nil, errors.New("unexpected method")
				}
			})
			router := &TickTickRouter{client: client, defaultProjectID: "default", aliases: map[string]string{}}
			worker := newTestWorker(t, store, &fakeExtractor{}, router)

			runWorkerOnce(t, worker)
			if !reflect.DeepEqual(calls, []string{"POST"}) {
				t.Fatalf("calls after uncertain create = %v", calls)
			}
			clock.Advance(time.Minute)
			runWorkerOnce(t, worker)
			if !reflect.DeepEqual(calls, []string{"POST", "GET"}) {
				t.Fatalf("calls before verified absence = %v", calls)
			}
			runWorkerOnce(t, worker)
			if !reflect.DeepEqual(calls, []string{"POST", "GET", "POST"}) {
				t.Fatalf("calls after verified absence = %v", calls)
			}
			status, err := store.RecordingStatus(context.Background(), receipt.ID)
			if err != nil {
				t.Fatalf("RecordingStatus() error = %v", err)
			}
			if status.State != "complete" || status.Tasks[0].State != "complete" {
				t.Fatalf("final status = %+v", status)
			}
		})
	}
}
