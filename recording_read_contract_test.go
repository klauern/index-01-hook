package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestDashboardReadContractFiltersPaginationAndDetail fixes the current SQLite read semantics before adapter extraction.
func TestDashboardReadContractFiltersPaginationAndDetail(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reads.db")
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	store, err := openStore(ctx, path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ids := make([]int64, 0, 6)
	for i, input := range []struct{ client, trigger, transcript string }{
		{"mobile", "button", "Ω synthetic-private-transcript"},
		{"desktop", "button", "synthetic-private-other-client"},
		{"mobile", "auto", "synthetic-private-other-trigger"},
		{"mobile", "button", ""},
		{"mobile", "button", "synthetic-private-four"},
		{"mobile", "button", "synthetic-private-five"},
	} {
		receipt, err := store.SaveRecording(ctx, RecordingInput{RecordedAtMillis: now.UnixMilli(), Client: input.client,
			Trigger: input.trigger, Transcription: input.transcript, AudioFilename: "synthetic-private.wav",
			AudioByteCount: 17, Fingerprint: fmt.Sprintf("%064x", i+1)})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, receipt.ID)
	}
	claim, err := store.ClaimExtraction(ctx, "read-contract", time.Minute)
	if err != nil || claim == nil || claim.RecordingID != ids[0] {
		t.Fatalf("claim: %+v, %v", claim, err)
	}
	if err := store.FreezeExtraction(ctx, ids[0], "read-contract", claim.AttemptNumber, FrozenExtraction{
		Provider: "deepseek", Model: "synthetic-model", Items: []QueuedItem{
			{Kind: ItemKindNote, Title: "synthetic-private-note", Content: "synthetic-private-content"},
			{Kind: ItemKindTask, Title: "synthetic-private-task"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	delivery, err := store.ClaimDelivery(ctx, "read-contract", time.Minute)
	if err != nil || delivery == nil {
		t.Fatalf("delivery: %+v, %v", delivery, err)
	}
	if err := store.CompleteDelivery(ctx, DeliveryCompletion{TaskID: delivery.ID, LeaseOwner: "read-contract",
		Classification: OutcomeCreated, TickTickTaskID: "synthetic-remote", TickTickProjectID: "synthetic-project"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordingStatus(ctx, ids[3]); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("audio-only operator lookup: %v", err)
	}
	status, err := store.RecordingStatus(ctx, ids[0])
	if err != nil || len(status.Tasks) != 2 || status.Tasks[0].TaskIndex != 0 || status.Tasks[1].TaskIndex != 1 {
		t.Fatalf("operator status: %+v, %v", status, err)
	}
	if _, err := store.RecordingStatus(ctx, ids[5]+1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing operator lookup: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := openDashboardDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := newDashboardHandler(db, nil).(*dashboardHandler)
	var changesBefore, changesAfter int
	if err := db.QueryRow("SELECT total_changes()").Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, query string
		want        []int64
		total       int64
		page, size  int
	}{
		{"first", "client=mobile&trigger=button&state=received&page_size=1", []int64{ids[5]}, 2, 1, 1},
		{"second", "client=mobile&trigger=button&state=received&page_size=1&page=2", []int64{ids[4]}, 2, 2, 1},
		{"beyond", "client=mobile&trigger=button&state=received&page_size=1&page=3", []int64{}, 2, 3, 1},
		{"alias", "client=mobile&trigger=button&status=received", []int64{ids[5], ids[4]}, 2, 1, 25},
		{"precedence", "client=mobile&trigger=button&state=not-started&status=received", []int64{ids[3]}, 1, 1, 25},
		{"raw-precedence", "client=mobile&trigger=button&state=+&status=received", []int64{ids[5], ids[4], ids[3], ids[0]}, 4, 1, 25},
		{"defaults", "client=+mobile+&trigger=button&state=received&page=1000001&page_size=101", []int64{ids[5], ids[4]}, 2, 1, 25},
		{"negative-defaults", "client=mobile&trigger=button&state=received&page=0&page_size=-1", []int64{ids[5], ids[4]}, 2, 1, 25},
		{"lease-state-is-not-workflow-state", "state=pending", []int64{}, 0, 1, 25},
		{"parameterized", "client=mobile%27+OR+1%3D1--", []int64{}, 0, 1, 25},
	} {
		t.Run(test.name, func(t *testing.T) {
			values, err := url.ParseQuery(test.query)
			if err != nil {
				t.Fatal(err)
			}
			page, err := h.readRecordings(ctx, values)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]int64, 0, len(page.Rows))
			for _, row := range page.Rows {
				got = append(got, row.ID)
			}
			if !reflect.DeepEqual(got, test.want) || page.Total != test.total || page.Page != test.page || page.PageSize != test.size {
				t.Fatalf("page: %+v; IDs %v, want %v", page, got, test.want)
			}
			if (page.PrevURL != "") != (test.page > 1) || (page.NextURL != "") != (int64(test.page*test.size) < test.total) {
				t.Fatalf("navigation: %+v", page)
			}
			if page.NextURL != "" {
				next, err := url.Parse(page.NextURL)
				if err != nil {
					t.Fatal(err)
				}
				q := next.Query()
				if q.Get("page") != "2" || q.Get("page_size") != "1" || q.Get("client") != "mobile" || q.Get("trigger") != "button" || q.Get("state") != "received" {
					t.Fatalf("next URL: %s", page.NextURL)
				}
			}
		})
	}
	detail, err := h.readRecording(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	if detail.DeliveryCount != 2 || detail.CompletedDeliveries != 1 || len(detail.Deliveries) != 2 ||
		detail.Deliveries[0].TaskIndex != 0 || detail.Deliveries[1].TaskIndex != 1 ||
		detail.Deliveries[0].Kind != "note" || detail.Deliveries[0].TickTickTaskID != "synthetic-remote" ||
		detail.Deliveries[0].CreationEvidence != "created" {
		t.Fatalf("detail: %+v", detail)
	}
	if detail.TranscriptionEvidence != "received (30 characters)" {
		t.Fatalf("Unicode character evidence with an incomplete sibling: %s", detail.TranscriptionEvidence)
	}
	audio, err := h.readRecording(ctx, ids[3])
	if err != nil || audio.ExtractionState != "not-started" || audio.TranscriptionEvidence != "not received" || len(audio.Deliveries) != 0 {
		t.Fatalf("audio-only dashboard: %+v, %v", audio, err)
	}
	if _, err := h.readRecording(ctx, ids[5]+1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing detail: %v", err)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-private") || strings.Contains(string(encoded), delivery.Marker) {
		t.Fatal("private detail leaked")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, rawID := range []string{"0", "bad", fmt.Sprint(ids[5] + 1)} {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, localDashboardRequest(method, "/recordings/"+rawID))
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s: %d", method, rawID, response.Code)
			}
			if method == http.MethodHead && response.Body.Len() != 0 {
				t.Fatal("HEAD returned body")
			}
		}
	}
	if err := db.QueryRow("SELECT total_changes()").Scan(&changesAfter); err != nil {
		t.Fatal(err)
	}
	if changesAfter != changesBefore {
		t.Fatalf("read wrote: %d -> %d", changesBefore, changesAfter)
	}
	if _, err := db.Exec("UPDATE recordings SET client='mutation'"); err == nil {
		t.Fatal("read-only database accepted write")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("read changed database bytes")
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, localDashboardRequest(http.MethodGet, fmt.Sprintf("/recordings/%d", ids[0])))
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("storage error response: %d %s", response.Code, response.Body)
	}
}

// TestDashboardReadContractRejectsUnsafeDatabaseInputs checks that dashboard opening cannot create or migrate storage.
func TestDashboardReadContractRejectsUnsafeDatabaseInputs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	missing := filepath.Join(root, "missing.db")
	if db, err := openDashboardDatabase(ctx, missing); err == nil {
		db.Close()
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing database was created: %v", err)
	}
	path := filepath.Join(root, "foreign.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE foreign_data (value TEXT); INSERT INTO foreign_data VALUES ('synthetic')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.db")
	valid := filepath.Join(root, "valid.db")
	store, err := openStore(ctx, valid, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{path, link, root} {
		if db, err := openDashboardDatabase(ctx, input); err == nil {
			db.Close()
			t.Fatalf("unsafe input accepted: %s", input)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("rejected database changed")
	}
}
