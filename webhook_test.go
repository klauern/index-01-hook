package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testToken = "synthetic-webhook-token-0123456789abcdef"

type multipartPart struct {
	name        string
	filename    string
	contentType string
	value       string
}

func newTestApp(t *testing.T, maxBodyBytes int64) (*Store, http.Handler, string) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newTestAppWithLogger(t, maxBodyBytes, logger)
}

func newTestAppWithLogger(t *testing.T, maxBodyBytes int64, logger *slog.Logger) (*Store, http.Handler, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "index01.db")
	store, err := OpenStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, NewHandler(store, testToken, maxBodyBytes, logger), dbPath
}

func assertWebhookRejectionLog(t *testing.T, logs *bytes.Buffer, reasonCode string, status int, sensitive ...string) {
	t.Helper()
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &event); err != nil {
		t.Fatalf("decode rejection log: %v; log=%q", err, logs.String())
	}
	if got := event["msg"]; got != "rejected webhook" {
		t.Errorf("msg = %v, want rejected webhook", got)
	}
	if got := event["reason_code"]; got != reasonCode {
		t.Errorf("reason_code = %v, want %q", got, reasonCode)
	}
	if got := event["status_code"]; got != float64(status) {
		t.Errorf("status_code = %v, want %d", got, status)
	}
	for _, value := range append(sensitive, testToken) {
		if value != "" && strings.Contains(logs.String(), value) {
			t.Errorf("rejection log contains sensitive value %q", value)
		}
	}
}

func multipartRequest(t *testing.T, boundary string, parts []multipartPart) *http.Request {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if boundary != "" {
		if err := w.SetBoundary(boundary); err != nil {
			t.Fatalf("SetBoundary() error = %v", err)
		}
	}
	for _, part := range parts {
		var dst io.Writer
		var err error
		if part.filename == "" {
			dst, err = w.CreateFormField(part.name)
		} else {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, part.name, part.filename))
			header.Set("Content-Type", part.contentType)
			dst, err = w.CreatePart(header)
		}
		if err != nil {
			t.Fatalf("CreatePart(%q) error = %v", part.name, err)
		}
		if _, err := io.WriteString(dst, part.value); err != nil {
			t.Fatalf("write part %q: %v", part.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func send(t *testing.T, handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestWebhookStoresCombinedPayloadAndQueuesDeepSeek(t *testing.T) {
	store, handler, dbPath := newTestApp(t, 1<<20)
	req := multipartRequest(t, "combined-boundary", []multipartPart{
		{name: "audio", filename: "clip.m4a", contentType: "audio/mp4", value: "audio bytes"},
		{name: "transcription", value: "Buy milk tomorrow"},
		{name: "client", value: "ring"},
		{name: "recordedAt", value: "1760000000123"},
	})
	req.Header.Set("X-Index-Trigger", "single_tap")

	rr := send(t, handler, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
	}
	var receipt Receipt
	if err := json.Unmarshal(rr.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if receipt.ID == 0 || receipt.Duplicate || !receipt.Queued {
		t.Fatalf("receipt = %+v, want new queued recording", receipt)
	}

	var recordedAt int64
	var client, trigger, transcription, filename string
	var audioBytes, receiveCount int64
	err := store.db.QueryRow(`
		SELECT recorded_at_ms, client, trigger, transcription, audio_filename, audio_byte_count, receive_count
		FROM recordings WHERE id = ?`, receipt.ID,
	).Scan(&recordedAt, &client, &trigger, &transcription, &filename, &audioBytes, &receiveCount)
	if err != nil {
		t.Fatalf("query recording: %v", err)
	}
	if recordedAt != 1760000000123 || client != "ring" || trigger != "single_tap" || transcription != "Buy milk tomorrow" {
		t.Errorf("stored semantic fields = (%d, %q, %q, %q)", recordedAt, client, trigger, transcription)
	}
	if filename != "clip.m4a" || audioBytes != int64(len("audio bytes")) || receiveCount != 1 {
		t.Errorf("stored audio metadata/count = (%q, %d, %d)", filename, audioBytes, receiveCount)
	}

	var status string
	var attemptCount int
	if err := store.db.QueryRow(`SELECT state, attempt_count FROM extraction_jobs WHERE recording_id = ?`, receipt.ID).Scan(&status, &attemptCount); err != nil {
		t.Fatalf("query extraction job: %v", err)
	}
	if status != "pending" || attemptCount != 0 {
		t.Errorf("extraction job = (%q, %d), want pending 0", status, attemptCount)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dbPath), "clip.m4a")); !os.IsNotExist(err) {
		t.Errorf("audio file should not exist, Stat() error = %v", err)
	}
	rows, err := store.db.Query(`PRAGMA table_info(recordings)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer ignoreCloseError(rows)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == "audio" || strings.Contains(name, "audio_blob") || strings.Contains(name, "audio_data") {
			t.Errorf("recordings schema unexpectedly stores audio content in column %q", name)
		}
	}
}

func TestWebhookReturns500WhenPersistenceFails(t *testing.T) {
	store, handler, dbPath := newTestApp(t, 1<<20)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() before request: %v", err)
	}

	response := send(t, handler, multipartRequest(t, "persistence-failure-boundary", []multipartPart{
		{name: "recordedAt", value: "1760000000500"},
		{name: "client", value: "ring"},
		{name: "transcription", value: "Do not acknowledge this request"},
	}))
	if response.Code == http.StatusAccepted {
		t.Fatalf("status = 202, want persistence failure")
	}
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", response.Code, response.Body.String())
	}
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil {
		t.Fatalf("decode failure response: %v", err)
	}
	if failure.Error != "failed to persist webhook" {
		t.Fatalf("failure response = %+v", failure)
	}
	var receipt Receipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatalf("decode response as receipt: %v", err)
	}
	if receipt.ID != 0 || receipt.Duplicate || receipt.Queued {
		t.Fatalf("failure returned a successful receipt: %+v", receipt)
	}

	reopened, err := OpenStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenStore() after failed request: %v", err)
	}
	defer ignoreCloseError(reopened)
	var recordingCount int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM recordings`).Scan(&recordingCount); err != nil {
		t.Fatalf("count recordings after failed request: %v", err)
	}
	if recordingCount != 0 {
		t.Fatalf("recording count = %d, want 0", recordingCount)
	}
}

func TestWebhookAcceptsTranscriptionOnlyAndAudioOnly(t *testing.T) {
	store, handler, _ := newTestApp(t, 1<<20)
	tests := []struct {
		name       string
		parts      []multipartPart
		wantQueued bool
	}{
		{
			name: "transcription only",
			parts: []multipartPart{
				{name: "recordedAt", value: "1760000001000"},
				{name: "client", value: "ring"},
				{name: "transcription", value: "Call Alex"},
			},
			wantQueued: true,
		},
		{
			name: "audio only",
			parts: []multipartPart{
				{name: "recordedAt", value: "1760000002000"},
				{name: "client", value: "ring"},
				{name: "audio", filename: "audio.wav", contentType: "audio/wav", value: "wave"},
			},
			wantQueued: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := send(t, handler, multipartRequest(t, "", tt.parts))
			if rr.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
			}
			var receipt Receipt
			if err := json.Unmarshal(rr.Body.Bytes(), &receipt); err != nil {
				t.Fatalf("decode receipt: %v", err)
			}
			if receipt.Queued != tt.wantQueued {
				t.Errorf("Queued = %v, want %v", receipt.Queued, tt.wantQueued)
			}
			var count int
			if err := store.db.QueryRow(`SELECT count(*) FROM extraction_jobs WHERE recording_id = ?`, receipt.ID).Scan(&count); err != nil {
				t.Fatalf("count extraction jobs: %v", err)
			}
			if count != boolInt(tt.wantQueued) {
				t.Errorf("extraction job count = %d, want %d", count, boolInt(tt.wantQueued))
			}
		})
	}
}

func TestWebhookDeduplicatesSemanticPayloadAcrossBoundariesAndPartOrder(t *testing.T) {
	store, handler, _ := newTestApp(t, 1<<20)
	first := []multipartPart{
		{name: "recordedAt", value: "1760000003000"},
		{name: "client", value: "ring"},
		{name: "transcription", value: "Schedule dentist"},
		{name: "audio", filename: "note.m4a", contentType: "audio/mp4", value: "same audio"},
	}
	second := []multipartPart{first[3], first[2], first[1], first[0]}

	firstResponse := send(t, handler, multipartRequest(t, "first-boundary", first))
	secondResponse := send(t, handler, multipartRequest(t, "second-boundary", second))
	if firstResponse.Code != http.StatusAccepted || secondResponse.Code != http.StatusOK {
		t.Fatalf("statuses = (%d, %d), want (202, 200); bodies=(%s, %s)", firstResponse.Code, secondResponse.Code, firstResponse.Body.String(), secondResponse.Body.String())
	}
	var firstReceipt, secondReceipt Receipt
	_ = json.Unmarshal(firstResponse.Body.Bytes(), &firstReceipt)
	_ = json.Unmarshal(secondResponse.Body.Bytes(), &secondReceipt)
	if firstReceipt.ID != secondReceipt.ID || !secondReceipt.Duplicate || !secondReceipt.Queued {
		t.Errorf("receipts = (%+v, %+v), want same ID and duplicate queued second response", firstReceipt, secondReceipt)
	}
	var recordings, jobs, receiveCount int
	_ = store.db.QueryRow(`SELECT count(*) FROM recordings`).Scan(&recordings)
	_ = store.db.QueryRow(`SELECT count(*) FROM extraction_jobs`).Scan(&jobs)
	_ = store.db.QueryRow(`SELECT receive_count FROM recordings WHERE id = ?`, firstReceipt.ID).Scan(&receiveCount)
	if recordings != 1 || jobs != 1 || receiveCount != 2 {
		t.Errorf("counts = recordings:%d jobs:%d receipts:%d, want 1, 1, 2", recordings, jobs, receiveCount)
	}
}

func TestWebhookRejectsInvalidRequests(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, handler, _ := newTestAppWithLogger(t, 512, logger)
	validParts := []multipartPart{{name: "recordedAt", value: "1760000004000"}, {name: "client", value: "ring"}}

	tests := []struct {
		name       string
		request    func(t *testing.T) *http.Request
		wantStatus int
		wantReason string
		sensitive  []string
	}{
		{
			name: "missing bearer token",
			request: func(t *testing.T) *http.Request {
				r := multipartRequest(t, "", validParts)
				r.Header.Del("Authorization")
				return r
			},
			wantStatus: http.StatusUnauthorized,
			wantReason: "missing_authorization",
		},
		{
			name: "wrong bearer token",
			request: func(t *testing.T) *http.Request {
				r := multipartRequest(t, "", validParts)
				r.Header.Set("Authorization", "Bearer wrong")
				return r
			},
			wantStatus: http.StatusUnauthorized,
			wantReason: "invalid_authorization",
			sensitive:  []string{"wrong"},
		},
		{
			name: "duplicate bearer token",
			request: func(t *testing.T) *http.Request {
				r := multipartRequest(t, "", validParts)
				r.Header.Add("Authorization", "Bearer second-secret")
				return r
			},
			wantStatus: http.StatusUnauthorized,
			wantReason: "duplicate_authorization",
			sensitive:  []string{"second-secret"},
		},
		{
			name: "unsupported content type",
			request: func(t *testing.T) *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{}`))
				r.Header.Set("Authorization", "Bearer "+testToken)
				r.Header.Set("Content-Type", "application/json")
				return r
			},
			wantStatus: http.StatusUnsupportedMediaType,
			wantReason: "unsupported_media_type",
		},
		{
			name: "malformed multipart body",
			request: func(t *testing.T) *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader("--broken\r\nContent-Disposition: form-data"))
				r.Header.Set("Authorization", "Bearer "+testToken)
				r.Header.Set("Content-Type", "multipart/form-data; boundary=broken")
				return r
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "missing_recorded_at",
			sensitive:  []string{"broken"},
		},
		{
			name: "malformed recordedAt",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", []multipartPart{{name: "recordedAt", value: "yesterday"}, {name: "client", value: "ring"}})
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "invalid_recorded_at",
			sensitive:  []string{"yesterday"},
		},
		{
			name: "missing client",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", []multipartPart{{name: "recordedAt", value: "1760000004000"}})
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "missing_client",
		},
		{
			name: "duplicate scalar field",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", []multipartPart{{name: "recordedAt", value: "1760000004000"}, {name: "client", value: "ring"}, {name: "client", value: "phone"}})
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "duplicate_field",
			sensitive:  []string{"phone"},
		},
		{
			name: "unexpected multipart field",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", append(validParts, multipartPart{name: "unexpected", value: "value"}))
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "unexpected_field",
			sensitive:  []string{"value"},
		},
		{
			name: "body over configured limit",
			request: func(t *testing.T) *http.Request {
				r := multipartRequest(t, "", []multipartPart{{name: "recordedAt", value: "1760000004000"}, {name: "client", value: "ring"}, {name: "transcription", value: strings.Repeat("x", 1024)}})
				// Exercise the streaming limiter instead of the Content-Length fast path.
				r.ContentLength = -1
				return r
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantReason: "body_too_large",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()
			rr := send(t, handler, tt.request(t))
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if !strings.HasPrefix(rr.Header().Get("Content-Type"), "application/json") {
				t.Errorf("Content-Type = %q, want JSON", rr.Header().Get("Content-Type"))
			}
			if rr.Header().Get("Cache-Control") != "no-store" || rr.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("security headers = Cache-Control:%q X-Content-Type-Options:%q",
					rr.Header().Get("Cache-Control"), rr.Header().Get("X-Content-Type-Options"))
			}
			assertWebhookRejectionLog(t, &logs, tt.wantReason, tt.wantStatus, tt.sensitive...)
		})
	}
}

func TestWebhookEnforcesFieldHeaderFilenameAndPartLimits(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	_, handler, _ := newTestAppWithLogger(t, 1<<20, logger)
	baseParts := []multipartPart{
		{name: "recordedAt", value: "1760000004000"},
		{name: "client", value: "ring"},
	}
	tests := []struct {
		name       string
		request    func(*testing.T) *http.Request
		wantStatus int
		wantReason string
	}{
		{
			name: "recordedAt field",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", []multipartPart{
					{name: "recordedAt", value: strings.Repeat("1", maxWebhookRecordedAtBytes+1)},
					{name: "client", value: "ring"},
				})
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantReason: "field_too_large",
		},
		{
			name: "client field",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", []multipartPart{
					{name: "recordedAt", value: "1760000004000"},
					{name: "client", value: strings.Repeat("c", maxWebhookClientBytes+1)},
				})
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantReason: "field_too_large",
		},
		{
			name: "transcription field",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", append(baseParts,
					multipartPart{name: "transcription", value: strings.Repeat("t", deepSeekMaxInputBytes+1)}))
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantReason: "field_too_large",
		},
		{
			name: "trigger header",
			request: func(t *testing.T) *http.Request {
				request := multipartRequest(t, "", baseParts)
				request.Header.Set("X-Index-Trigger", strings.Repeat("x", maxWebhookTriggerBytes+1))
				return request
			},
			wantStatus: http.StatusRequestHeaderFieldsTooLarge,
			wantReason: "request_headers_invalid",
		},
		{
			name: "duplicate content type header",
			request: func(t *testing.T) *http.Request {
				request := multipartRequest(t, "", baseParts)
				request.Header.Add("Content-Type", "multipart/form-data; boundary=duplicate")
				return request
			},
			wantStatus: http.StatusRequestHeaderFieldsTooLarge,
			wantReason: "request_headers_invalid",
		},
		{
			name: "duplicate trigger header",
			request: func(t *testing.T) *http.Request {
				request := multipartRequest(t, "", baseParts)
				request.Header.Add("X-Index-Trigger", "single_tap")
				request.Header.Add("X-Index-Trigger", "double_tap")
				return request
			},
			wantStatus: http.StatusRequestHeaderFieldsTooLarge,
			wantReason: "request_headers_invalid",
		},
		{
			name: "filename",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", append(baseParts, multipartPart{
					name: "audio", filename: strings.Repeat("f", maxWebhookFilenameBytes+1), contentType: "audio/wav", value: "audio",
				}))
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "audio_filename",
		},
		{
			name: "part count",
			request: func(t *testing.T) *http.Request {
				return multipartRequest(t, "", []multipartPart{
					{name: "recordedAt", value: "1760000004000"},
					{name: "client", value: "ring"},
					{name: "transcription", value: "content"},
					{name: "audio", filename: "audio.wav", contentType: "audio/wav", value: "audio"},
					{name: "unexpected", value: "fifth"},
				})
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "part_count",
		},
		{
			name: "part headers",
			request: func(*testing.T) *http.Request {
				boundary := "header-limit"
				body := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"recordedAt\"\r\nX-Fill: " +
					strings.Repeat("h", maxWebhookPartHeaderBytes) + "\r\n\r\n1760000004000\r\n--" + boundary + "--\r\n"
				request := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer "+testToken)
				request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
				return request
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "part_headers",
		},
		{
			name: "part header count",
			request: func(*testing.T) *http.Request {
				boundary := "part-header-count"
				var body strings.Builder
				body.WriteString("--" + boundary + "\r\nContent-Disposition: form-data; name=\"recordedAt\"\r\n")
				for index := 0; index < maxWebhookPartHeaders; index++ {
					fmt.Fprintf(&body, "X-Test-%d: value\r\n", index)
				}
				body.WriteString("\r\n1760000004000\r\n--" + boundary + "--\r\n")
				request := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body.String()))
				request.Header.Set("Authorization", "Bearer "+testToken)
				request.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
				return request
			},
			wantStatus: http.StatusBadRequest,
			wantReason: "part_headers",
		},
		{
			name: "request header count",
			request: func(t *testing.T) *http.Request {
				request := multipartRequest(t, "", baseParts)
				for index := 0; index < maxWebhookHeaders; index++ {
					request.Header.Set(fmt.Sprintf("X-Test-%d", index), "value")
				}
				return request
			},
			wantStatus: http.StatusRequestHeaderFieldsTooLarge,
			wantReason: "request_headers_invalid",
		},
		{
			name: "request header bytes",
			request: func(t *testing.T) *http.Request {
				request := multipartRequest(t, "", baseParts)
				request.Header.Set("X-Fill", strings.Repeat("h", maxWebhookHeaderBytes))
				return request
			},
			wantStatus: http.StatusRequestHeaderFieldsTooLarge,
			wantReason: "request_headers_invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logs.Reset()
			response := send(t, handler, test.request(t))
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			assertWebhookRejectionLog(t, &logs, test.wantReason, test.wantStatus)
		})
	}
}

func TestWebhookAcceptsConcurrentNearLimitTranscriptions(t *testing.T) {
	_, handler, _ := newTestApp(t, 1<<20)
	const requestCount = 12
	requests := make([]*http.Request, requestCount)
	for index := range requests {
		requests[index] = multipartRequest(t, "", []multipartPart{
			{name: "recordedAt", value: fmt.Sprintf("176000001%04d", index)},
			{name: "client", value: strings.Repeat("c", maxWebhookClientBytes)},
			{name: "transcription", value: strings.Repeat("t", deepSeekMaxInputBytes)},
		})
	}

	statuses := make(chan int, requestCount)
	var wait sync.WaitGroup
	for _, request := range requests {
		wait.Add(1)
		go func(request *http.Request) {
			defer wait.Done()
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			statuses <- response.Code
		}(request)
	}
	wait.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusAccepted {
			t.Fatalf("concurrent status = %d, want 202", status)
		}
	}
}

func TestJSONResponsesDisableStorageAndSniffing(t *testing.T) {
	_, handler, _ := newTestApp(t, 1<<20)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
		multipartRequest(t, "", []multipartPart{
			{name: "recordedAt", value: "1760000099000"},
			{name: "client", value: "ring"},
		}),
	}
	for _, request := range requests {
		response := send(t, handler, request)
		if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("security headers = Cache-Control:%q X-Content-Type-Options:%q",
				response.Header().Get("Cache-Control"), response.Header().Get("X-Content-Type-Options"))
		}
	}
}

func TestStorePersistsAcrossReopenAndMigrationsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "durable.db")
	store, err := OpenStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("first OpenStore(): %v", err)
	}
	receipt, err := store.SaveRecording(ctx, RecordingInput{
		RecordedAtMillis: 1760000005000,
		Client:           "ring",
		Transcription:    "Persist this",
		Fingerprint:      "stable-test-fingerprint",
	})
	if err != nil {
		t.Fatalf("SaveRecording(): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Close(): %v", err)
	}

	reopened, err := OpenStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("second OpenStore(): %v", err)
	}
	defer ignoreCloseError(reopened)
	var count, version int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM recordings WHERE id = ?`, receipt.ID).Scan(&count); err != nil {
		t.Fatalf("query durable recording: %v", err)
	}
	if err := reopened.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("query migration version: %v", err)
	}
	if count != 1 || version < 1 {
		t.Errorf("durable count/version = (%d, %d), want (1, >=1)", count, version)
	}
}

func TestStoreEnablesRequiredSQLitePragmas(t *testing.T) {
	store, _, _ := newTestApp(t, 1<<20)
	var foreignKeys, busyTimeout int
	var journalMode string
	if err := store.db.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if foreignKeys != 1 || !strings.EqualFold(journalMode, "wal") || busyTimeout != 5000 {
		t.Errorf("SQLite pragmas = foreign_keys:%d journal_mode:%q busy_timeout:%d", foreignKeys, journalMode, busyTimeout)
	}
}

func TestHealthzReflectsSQLiteAvailability(t *testing.T) {
	store, handler, _ := newTestApp(t, 1<<20)
	rr := send(t, handler, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	rr = send(t, handler, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy status = %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
