package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestUncertainTickTickCreateRequiresReviewWithoutAnotherPost preserves ambiguity when no marker is confirmed.
func TestUncertainTickTickCreateRequiresReviewWithoutAnotherPost(t *testing.T) {
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
					if request.URL.Path == "/open/v1/project" {
						return fixtureResponse(http.StatusOK, `[{"id":"default"}]`), nil
					}
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
			if !reflect.DeepEqual(calls, []string{"POST", "GET", "GET", "GET"}) {
				t.Fatalf("calls after reconciliation = %v", calls)
			}
			runWorkerOnce(t, worker)
			if postCount != 1 {
				t.Fatalf("automatic duplicate create: %v", calls)
			}
			status, err := store.RecordingStatus(context.Background(), receipt.ID)
			if err != nil {
				t.Fatalf("RecordingStatus() error = %v", err)
			}
			if status.State != "extracted" || status.Tasks[0].State != "needs_review" {
				t.Fatalf("final status = %+v", status)
			}
		})
	}
}

// TestReconciliationAuthenticationStaysBlocked preserves credential recovery without another remote create.
func TestReconciliationAuthenticationStaysBlocked(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		for _, stage := range []string{"catalog", "project"} {
			t.Run(fmt.Sprintf("%s-%d", stage, code), func(t *testing.T) {
				store, clock := newQueueStore(t)
				receipt := saveQueueRecording(t, store, "Synthetic authentication recovery")
				freezeQueueTasks(t, store, "extractor", []QueuedTask{{Title: "Only", Priority: 0}})
				posts := 0
				client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
					if request.Method == http.MethodPost {
						posts++
						return fixtureResponse(http.StatusBadGateway, `{}`), nil
					}
					if request.URL.Path == "/open/v1/project" && stage == "project" {
						return fixtureResponse(http.StatusOK, `[{"id":"default"}]`), nil
					}
					return fixtureResponse(code, `{}`), nil
				})
				router := &TickTickRouter{client: client, defaultProjectID: "default", aliases: map[string]string{}}
				worker := newTestWorker(t, store, &fakeExtractor{}, router)
				runWorkerOnce(t, worker)
				clock.Advance(time.Minute)
				runWorkerOnce(t, worker)
				status, err := store.RecordingStatus(context.Background(), receipt.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(status.Tasks) != 1 || status.Tasks[0].State != "blocked_auth" || posts != 1 {
					t.Fatalf("status=%+v posts=%d", status, posts)
				}
				clock.Advance(time.Hour)
				runWorkerOnce(t, worker)
				if posts != 1 {
					t.Fatal("authentication failure issued another create")
				}
			})
		}
	}
}
