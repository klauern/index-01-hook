package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestTickTickReconcileMovedItem checks moved items, alias changes, edits, and duplicate markers.
func TestTickTickReconcileMovedItem(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":8]"
	content := marker + "\n\noriginal content"
	cases := []struct {
		name       string
		alias      map[string]string
		project    string
		title      string
		content    string
		duplicate  bool
		wantStatus TickTickReconciliationStatus
	}{
		{name: "moved to another list", alias: map[string]string{"work": "old"}, project: "moved", title: "Original", content: content, wantStatus: TickTickReconciliationConfirmed},
		{name: "alias removed", alias: map[string]string{}, project: "old", title: "Original", content: content, wantStatus: TickTickReconciliationConfirmed},
		{name: "alias remapped", alias: map[string]string{"work": "moved"}, project: "old", title: "Original", content: content, wantStatus: TickTickReconciliationConfirmed},
		{name: "title changed", alias: map[string]string{"work": "old"}, project: "moved", title: "Edited", content: content, wantStatus: TickTickReconciliationReview},
		{name: "content changed", alias: map[string]string{"work": "old"}, project: "moved", title: "Original", content: marker + "\n\nedited", wantStatus: TickTickReconciliationReview},
		{name: "duplicate across lists", alias: map[string]string{"work": "old"}, project: "moved", title: "Original", content: content, duplicate: true, wantStatus: TickTickReconciliationReview},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				switch request.URL.Path {
				case "/open/v1/project":
					return fixtureResponse(http.StatusOK, `[{"id":"old"},{"id":"moved"}]`), nil
				case "/open/v1/project/old/data", "/open/v1/project/moved/data", "/open/v1/project/inbox/data":
					project := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/open/v1/project/"), "/data")
					items := ""
					if project == tc.project || (tc.duplicate && project == "old") {
						items = fmt.Sprintf(`{"id":%q,"kind":"TEXT","title":%q,"content":%q}`, project+"-item", tc.title, tc.content)
					}
					return fixtureResponse(http.StatusOK, `{"tasks":[`+items+`]}`), nil
				default:
					t.Fatalf("unexpected read: %s", request.URL.Path)
					return nil, nil
				}
			})
			router := &TickTickRouter{client: client, defaultProjectID: "old", aliases: tc.alias}
			result, err := router.ReconcileItem(context.Background(), TickTickReconciliationInput{
				Kind: ItemKindTask, ProjectAlias: "work", Marker: marker, Title: "Original", Content: "original content",
			})
			if err != nil || result.Status != tc.wantStatus {
				t.Fatalf("reconcile = %+v, %v; want %s", result, err, tc.wantStatus)
			}
			if tc.wantStatus == TickTickReconciliationConfirmed && (result.ProjectID != tc.project || result.TaskID != tc.project+"-item") {
				t.Fatalf("moved identity = %+v", result)
			}
		})
	}
}

// TestTickTickReconcileBoundsProjectScan rejects incomplete reconciliation beyond the project bound.
func TestTickTickReconcileBoundsProjectScan(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":10]"
	for _, count := range []int{100, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			projects := make([]string, count)
			for i := range projects {
				projects[i] = fmt.Sprintf(`{"id":"p%d"}`, i)
			}
			calls := 0
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Path == "/open/v1/project" {
					return fixtureResponse(http.StatusOK, "["+strings.Join(projects, ",")+"]"), nil
				}
				return fixtureResponse(http.StatusOK, `{"tasks":[]}`), nil
			})
			result, err := (&TickTickRouter{client: client}).ReconcileItem(context.Background(), TickTickReconciliationInput{Kind: ItemKindTask, Marker: marker, Title: "Original", Content: "original content"})
			if err != nil {
				t.Fatal(err)
			}
			if count == 100 {
				if calls != 102 {
					t.Fatalf("HTTP calls = %d; want 102", calls)
				}
			} else if calls != 1 || result.Status != TickTickReconciliationReview {
				t.Fatalf("calls/result = %d/%+v; want list-only review", calls, result)
			}
		})
	}
}

// TestTickTickReconcileDeadlineAndLateFailure bounds reads and retains ambiguity on late failures.
func TestTickTickReconcileDeadlineAndLateFailure(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":11]"
	for _, callerShorter := range []bool{false, true} {
		client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
			deadline, ok := request.Context().Deadline()
			if !ok {
				t.Fatal("request has no deadline")
			}
			remaining := time.Until(deadline)
			if callerShorter && remaining > 100*time.Millisecond {
				t.Fatalf("caller deadline was not retained: %s", remaining)
			}
			if !callerShorter && remaining > tickTickReconcileTimeout {
				t.Fatalf("deadline exceeds scan timeout: %s", remaining)
			}
			if request.URL.Path == "/open/v1/project" {
				return fixtureResponse(http.StatusOK, `[{"id":"a"},{"id":"b"}]`), nil
			}
			if strings.HasSuffix(request.URL.Path, "/a/data") {
				return fixtureResponse(http.StatusOK, `{"tasks":[{"id":"found","kind":"TEXT","title":"Original","content":"`+marker+`\n\noriginal content"}]}`), nil
			}
			return fixtureResponse(http.StatusBadGateway, ""), nil
		})
		ctx := context.Background()
		if callerShorter {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
		}
		result, err := (&TickTickRouter{client: client}).ReconcileItem(ctx, TickTickReconciliationInput{Kind: ItemKindTask, Marker: marker, Title: "Original", Content: "original content"})
		if classifyTickTickError(err) != TickTickErrorRetryable || result.Status == TickTickReconciliationConfirmed {
			t.Fatalf("late failure = %+v, %v", result, err)
		}
	}
}

type contextBlockingBody struct {
	ctx context.Context
}

// Read waits for cancellation to model a stalled reconciliation response.
func (b contextBlockingBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

// Close satisfies the synthetic response body contract.
func (contextBlockingBody) Close() error { return nil }

// TestTickTickReconcileDecodeDeadlineIsRetryable preserves retries when response decoding exceeds the deadline.
func TestTickTickReconcileDecodeDeadlineIsRetryable(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":13]"
	for _, stage := range []string{"project list", "project data"} {
		t.Run(stage, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				if stage == "project list" && request.URL.Path == "/open/v1/project" ||
					stage == "project data" && request.URL.Path == "/open/v1/project/old/data" {
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: contextBlockingBody{ctx: request.Context()}}, nil
				}
				if request.URL.Path == "/open/v1/project" {
					return fixtureResponse(http.StatusOK, `[{"id":"old"}]`), nil
				}
				t.Fatalf("unexpected read: %s", request.URL.Path)
				return nil, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			result, err := (&TickTickRouter{client: client}).ReconcileItem(ctx, TickTickReconciliationInput{
				Kind: ItemKindTask, Marker: marker, Title: "Original", Content: "original content",
			})
			if classifyTickTickError(err) != TickTickErrorRetryable || result.Status == TickTickReconciliationConfirmed {
				t.Fatalf("expired scan = %+v, %v; want retryable error and no confirmation", result, err)
			}
		})
	}
}

// TestTickTickReconcileWhitespaceProjectRequiresReview rejects noncanonical remote project identities.
func TestTickTickReconcileWhitespaceProjectRequiresReview(t *testing.T) {
	for _, projectID := range []string{" old", "old "} {
		t.Run(fmt.Sprintf("%q", projectID), func(t *testing.T) {
			calls := 0
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				calls++
				if request.URL.Path != "/open/v1/project" {
					t.Fatalf("unexpected data request: %s", request.URL.Path)
				}
				return fixtureResponse(http.StatusOK, fmt.Sprintf(`[{"id":%q}]`, projectID)), nil
			})
			result, err := (&TickTickRouter{client: client}).ReconcileItem(context.Background(), TickTickReconciliationInput{Kind: ItemKindTask, Marker: "[index01:" + testRecordingHash + ":12]", Title: "Original", Content: "original content"})
			if err != nil || result.Status != TickTickReconciliationReview {
				t.Fatalf("result = %+v, %v; want review", result, err)
			}
			if calls != 1 {
				t.Fatalf("HTTP calls = %d; want one project-list request", calls)
			}
		})
	}
}

// TestTickTickReconcileIncompleteScanRequiresReview prevents confirmation before every candidate project is checked.
func TestTickTickReconcileIncompleteScanRequiresReview(t *testing.T) {
	marker := "[index01:" + testRecordingHash + ":9]"
	cases := []struct {
		name   string
		list   string
		status int
		data   string
	}{
		{name: "list failure", status: http.StatusBadGateway},
		{name: "malformed list", list: `{}`, status: http.StatusOK},
		{name: "invalid project id", list: `[{"id":"../bad"}]`, status: http.StatusOK},
		{name: "too many projects", list: func() string {
			parts := make([]string, maxTickTickReconcileProjects+1)
			for i := range parts {
				parts[i] = fmt.Sprintf(`{"id":"p%d"}`, i)
			}
			return "[" + strings.Join(parts, ",") + "]"
		}(), status: http.StatusOK},
		{name: "project read failure", list: `[{"id":"old"}]`, status: http.StatusOK, data: "failure"},
		{name: "transient project read failure", list: `[{"id":"old"}]`, status: http.StatusOK, data: "transient"},
		{name: "missing task list", list: `[{"id":"old"}]`, status: http.StatusOK, data: "missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fixtureTickTickClient(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/open/v1/project" {
					return fixtureResponse(tc.status, tc.list), nil
				}
				if tc.data == "failure" {
					return fixtureResponse(http.StatusForbidden, "private provider text"), nil
				}
				if tc.data == "transient" {
					return fixtureResponse(http.StatusBadGateway, "private provider text"), nil
				}
				if tc.data == "missing" {
					return fixtureResponse(http.StatusOK, `{}`), nil
				}
				t.Fatalf("unexpected read: %s", request.URL.Path)
				return nil, nil
			})
			result, err := (&TickTickRouter{client: client, defaultProjectID: "old"}).ReconcileItem(context.Background(), TickTickReconciliationInput{
				Kind: ItemKindTask, Marker: marker, Title: "Original", Content: "original content",
			})
			if tc.name == "list failure" || tc.data == "transient" {
				if classifyTickTickError(err) != TickTickErrorRetryable {
					t.Fatalf("transient scan = %+v, %v; want retryable error", result, err)
				}
			} else if err != nil || result.Status != TickTickReconciliationReview {
				t.Fatalf("incomplete scan = %+v, %v; want review", result, err)
			}
		})
	}
}
