package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDashboardListenAddressRequiresLoopback(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8080", "[::]:8080", ":8080"} {
		if err := validateDashboardListenAddr(address); err == nil {
			t.Errorf("validateDashboardListenAddr(%q) accepted a non-loopback address", address)
		}
	}
	for _, address := range []string{"127.0.0.1:0", "[::1]:0", "localhost:0"} {
		if err := validateDashboardListenAddr(address); err != nil {
			t.Errorf("validateDashboardListenAddr(%q) error = %v", address, err)
		}
	}
}

func TestDashboardReadOnlyAndRedactedHTML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index01.db")
	receivedAt := time.Date(2026, time.August, 30, 14, 10, 0, 0, time.UTC)
	store, err := openStore(context.Background(), path, func() time.Time { return receivedAt })
	if err != nil {
		t.Fatal(err)
	}
	privateFingerprint := strings.Repeat("a", 64)
	input := RecordingInput{
		RecordedAtMillis: receivedAt.Add(-time.Minute).UnixMilli(), Client: "mobile", Trigger: "button",
		Transcription: "private transcription", AudioFilename: "private.wav",
		AudioByteCount: 12, Fingerprint: privateFingerprint,
	}
	receipt, err := store.SaveRecording(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	receivedAt = receivedAt.Add(2 * time.Minute)
	duplicate, err := store.SaveRecording(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.ID != receipt.ID || !duplicate.Duplicate {
		t.Fatalf("duplicate receipt = %+v, want recording %d marked duplicate", duplicate, receipt.ID)
	}
	claim, err := store.ClaimExtraction(context.Background(), "dashboard-test", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	if err := store.FreezeExtraction(context.Background(), receipt.ID, "dashboard-test", FrozenExtraction{
		Provider: "deepseek", Model: "deepseek-chat", Items: []QueuedItem{{
			Kind: ItemKindTask, Title: "private title", Content: "private notes",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	delivery, err := store.ClaimDelivery(context.Background(), "dashboard-test", time.Minute)
	if err != nil || delivery == nil {
		t.Fatalf("ClaimDelivery() = %+v, %v", delivery, err)
	}
	privateMarker := delivery.Marker
	if err := store.CompleteDelivery(context.Background(), DeliveryCompletion{
		TaskID: delivery.ID, LeaseOwner: "dashboard-test", Classification: OutcomeCreated,
		TickTickTaskID: "ticktick-item-101", TickTickProjectID: "ticktick-project-work",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDashboardDatabase(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE recordings SET client = 'mutated' WHERE id = 1`); err == nil {
		t.Fatal("dashboard database accepted a write")
	}
	recorder := httptest.NewRecorder()
	request := localDashboardRequest(http.MethodGet, "/?client=mobile")
	handler := newDashboardHandler(db, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, privateValue := range []string{"private transcription", "private.wav", privateFingerprint} {
		if strings.Contains(body, privateValue) {
			t.Errorf("dashboard body contains private value %q", privateValue)
		}
	}
	for header, want := range map[string]string{
		"Cache-Control": "no-store", "Cross-Origin-Resource-Policy": "same-origin",
		"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY",
		"Referrer-Policy": "no-referrer",
	} {
		if got := recorder.Header().Get(header); got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
	}
	if !strings.Contains(body, "hx-trigger=\"every 15s\"") || !strings.Contains(body, "hx-target=\"#recordings\"") || !strings.Contains(body, "hx-swap=\"outerMorph\"") {
		t.Error("dashboard body does not contain HTMX 4 polling, targeting, and morph attributes")
	}
	if !strings.Contains(body, `<th class="id-column">ID</th>`) || !strings.Contains(body, `<td class="id-column"><a href="/recordings/`) {
		t.Error("dashboard body does not mark the recording ID column")
	}
	if !strings.Contains(body, "mobile") || !strings.Contains(body, "button") {
		t.Error("dashboard body does not contain safe recording fields")
	}
	for _, evidence := range []string{"Client recorded", "Server received", "Transcription", "received; text removed after processing"} {
		if !strings.Contains(body, evidence) {
			t.Errorf("dashboard body does not contain evidence %q", evidence)
		}
	}
	detailRecorder := httptest.NewRecorder()
	handler.ServeHTTP(detailRecorder, localDashboardRequest(http.MethodGet, "/recordings/"+strconv.FormatInt(receipt.ID, 10)))
	if detailRecorder.Code != http.StatusOK {
		t.Fatalf("dashboard detail status = %d, want 200", detailRecorder.Code)
	}
	detailBody := detailRecorder.Body.String()
	for _, privateValue := range []string{"private transcription", "private title", "private notes", "private.wav", privateFingerprint, privateMarker} {
		if strings.Contains(detailBody, privateValue) {
			t.Errorf("dashboard detail contains private value %q", privateValue)
		}
	}
	for _, safeValue := range []string{
		"Recording receipt evidence", "Client recorded", "First server receipt", "Last server receipt",
		"The server receipt times confirm that Index 01 received this recording.",
		"received; text removed after processing", "TickTick delivery evidence", "TickTick item ID",
		"ticktick-item-101", "ticktick-project-work", "created", "complete", "task",
	} {
		if !strings.Contains(detailBody, safeValue) {
			t.Errorf("dashboard detail does not contain %q", safeValue)
		}
	}
	if !strings.Contains(detailBody, `<th class="id-column">ID</th>`) || !strings.Contains(detailBody, `<td class="id-column">`) {
		t.Error("dashboard detail does not mark the delivery ID column")
	}
	for _, receiptEvidence := range []string{
		"2026-08-30T14:10:00Z", "2026-08-30T14:12:00Z", "<dt>Receives</dt><dd>2</dd>",
	} {
		if !strings.Contains(detailBody, receiptEvidence) {
			t.Errorf("dashboard detail does not contain receipt evidence %q", receiptEvidence)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Error("dashboard changed the database file")
	}
	if receipt.ID <= 0 {
		t.Fatal("test recording was not created")
	}
}

func TestDashboardTranscriptionEvidence(t *testing.T) {
	for _, test := range []struct {
		characters int64
		queued     bool
		want       string
	}{
		{characters: 21, queued: true, want: "received (21 characters)"},
		{queued: true, want: "received; text removed after processing"},
		{want: "not received"},
	} {
		if got := transcriptionEvidence(test.characters, test.queued); got != test.want {
			t.Errorf("transcriptionEvidence(%d, %t) = %q, want %q", test.characters, test.queued, got, test.want)
		}
	}
}

func TestRunDashboardServesAndStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index01.db")
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	openResult := make(chan error, 1)
	opener := func(address string) error {
		response, err := (&http.Client{Timeout: 2 * time.Second}).Get(address)
		if err == nil {
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				err = fmt.Errorf("dashboard status = %d", response.StatusCode)
			}
		}
		openResult <- err
		cancel()
		return err
	}
	getenv := func(key string) string {
		switch key {
		case "INDEX01_DB_PATH":
			return path
		case "INDEX01_DASHBOARD_LISTEN_ADDR":
			return "127.0.0.1:0"
		default:
			return ""
		}
	}
	if err := runDashboardWithContext(ctx, nil, getenv, opener); err != nil {
		t.Fatalf("runDashboardWithContext() error = %v", err)
	}
	if err := <-openResult; err != nil {
		t.Fatal(err)
	}
}

func TestDashboardAllowsGetHeadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index01.db")
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Close()
	db, err := openDashboardDatabase(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := newDashboardHandler(db, nil)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, localDashboardRequest(method, "/status"))
		if rw.Code != http.StatusOK {
			t.Errorf("%s status = %d", method, rw.Code)
		}
		if method == http.MethodHead && rw.Body.Len() != 0 {
			t.Errorf("HEAD body length = %d, want 0", rw.Body.Len())
		}
	}
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, localDashboardRequest(http.MethodPost, "/status"))
	if rw.Code != http.StatusMethodNotAllowed || rw.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST response = %d, Allow=%q", rw.Code, rw.Header().Get("Allow"))
	}
}

func TestDashboardServesVendoredHTMX4(t *testing.T) {
	handler := newDashboardHandler(nil, nil)
	for _, test := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/static/htmx.min.js", contentType: "application/javascript; charset=utf-8", contains: `this.version="4.0.0"`},
		{path: "/static/dashboard.css", contentType: "text/css; charset=utf-8", contains: "color-scheme"},
		{path: "/static/dashboard.css", contentType: "text/css; charset=utf-8", contains: ".id-column { min-width: 5rem; white-space: nowrap; }"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, localDashboardRequest(http.MethodGet, test.path))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d", test.path, recorder.Code)
		}
		if got := recorder.Header().Get("Content-Type"); got != test.contentType {
			t.Errorf("GET %s content type = %q, want %q", test.path, got, test.contentType)
		}
		if !strings.Contains(recorder.Body.String(), test.contains) {
			t.Errorf("GET %s does not contain %q", test.path, test.contains)
		}
	}
}

func TestDashboardRejectsNonLocalHost(t *testing.T) {
	handler := newDashboardHandler(nil, nil)
	request := httptest.NewRequest(http.MethodGet, "http://attacker.example/status", nil)
	request.Host = "attacker.example"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusMisdirectedRequest {
		t.Fatalf("dashboard status = %d, want %d", recorder.Code, http.StatusMisdirectedRequest)
	}
	for _, host := range []string{"localhost:8081", "localhost.:8081", "127.0.0.1:8081", "[::1]:8081"} {
		if !validDashboardHost(host) {
			t.Errorf("validDashboardHost(%q) = false", host)
		}
	}
}

func localDashboardRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Host = "127.0.0.1:8081"
	return request
}
