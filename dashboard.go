package main

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

const defaultDashboardListenAddr = "127.0.0.1:0"

//go:embed dashboard_static/*
var dashboardAssets embed.FS

type dashboardConfig struct {
	DBPath     string
	ListenAddr string
	NoOpen     bool
}

func loadDashboardConfig(getenv func(string) string) (dashboardConfig, error) {
	path, err := normalizeDatabasePath(getenv("INDEX01_DB_PATH"))
	if err != nil {
		return dashboardConfig{}, err
	}
	address := getenv("INDEX01_DASHBOARD_LISTEN_ADDR")
	if address == "" {
		address = defaultDashboardListenAddr
	} else if strings.TrimSpace(address) == "" {
		return dashboardConfig{}, fmt.Errorf("INDEX01_DASHBOARD_LISTEN_ADDR must not be blank")
	} else {
		address = strings.TrimSpace(address)
	}
	if err := validateDashboardListenAddr(address); err != nil {
		return dashboardConfig{}, err
	}
	return dashboardConfig{DBPath: path, ListenAddr: address, NoOpen: getenv("INDEX01_DASHBOARD_NO_OPEN") == "1"}, nil
}

func validateDashboardListenAddr(address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		return fmt.Errorf("INDEX01_DASHBOARD_LISTEN_ADDR must not be blank")
	}
	resolved, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return fmt.Errorf("INDEX01_DASHBOARD_LISTEN_ADDR is invalid: %w", err)
	}
	if resolved.IP == nil || !resolved.IP.IsLoopback() {
		return fmt.Errorf("INDEX01_DASHBOARD_LISTEN_ADDR must bind to a loopback address")
	}
	return nil
}

func openDashboardDatabase(ctx context.Context, path string) (*sql.DB, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return nil, fmt.Errorf("inspect database: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("database path must be a regular file")
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath), RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open dashboard database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open dashboard database: %w", err)
	}
	if err := validateApplicationDatabase(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("validate dashboard database: %w", err)
	}
	return db, nil
}

func runDashboard(logger *slog.Logger, getenv func(string) string) error {
	ctx, stop := signalContext()
	defer stop()
	return runDashboardWithContext(ctx, logger, getenv, openDashboardBrowser)
}

func runDashboardWithContext(ctx context.Context, logger *slog.Logger, getenv func(string) string, open func(string) error) error {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	cfg, err := loadDashboardConfig(getenv)
	if err != nil {
		return err
	}
	db, err := openDashboardDatabase(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen for dashboard: %w", err)
	}
	defer listener.Close()
	handler := newDashboardHandler(db, logger)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute}
	address := listener.Addr().String()
	logger.Info("dashboard listening", "address", address)
	if !cfg.NoOpen && open != nil {
		browserURL := "http://" + address + "/"
		go func() {
			if err := open(browserURL); err != nil {
				logger.Warn("could not open dashboard browser", "error", err)
			}
		}()
	}
	return serve(ctx, server, listener, 10*time.Second)
}

// signalContext keeps dashboard startup independent from the worker configuration.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func openDashboardBrowser(address string) error {
	var command string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		command, args = "open", []string{address}
	case "windows":
		command, args = "rundll32", []string{"url.dll,FileProtocolHandler", address}
	default:
		command, args = "xdg-open", []string{address}
	}
	return exec.Command(command, args...).Run()
}

type dashboardHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

func newDashboardHandler(db *sql.DB, logger *slog.Logger) http.Handler {
	return &dashboardHandler{db: db, logger: logger}
}
func validDashboardHost(hostport string) bool {
	host := strings.TrimSpace(hostport)
	if host == "" {
		return false
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	host = strings.Trim(strings.TrimSuffix(host, "."), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type dashboardHeadWriter struct {
	http.ResponseWriter
}

func (w dashboardHeadWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func (h *dashboardHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setDashboardHeaders(w)
	if !validDashboardHost(r.Host) {
		h.writeError(w, http.StatusMisdirectedRequest)
		return
	}
	if r.Method == http.MethodHead {
		w = dashboardHeadWriter{ResponseWriter: w}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/static/htmx.min.js" || r.URL.Path == "/static/dashboard.css" {
		h.serveAsset(w, r)
		return
	}
	if r.URL.Path == "/" || r.URL.Path == "/dashboard" {
		h.serveHome(w, r)
		return
	}
	if r.URL.Path == "/status" {
		h.serveStatus(w, r)
		return
	}
	if r.URL.Path == "/recordings" {
		h.serveRecordings(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/recordings/") {
		h.serveRecording(w, r, strings.TrimPrefix(r.URL.Path, "/recordings/"))
		return
	}
	h.writeError(w, http.StatusNotFound)
}

func setDashboardHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("Vary", "HX-Request")
}

func (h *dashboardHandler) serveAsset(w http.ResponseWriter, r *http.Request) {
	assetName := "dashboard_static/htmx.min.js"
	contentType := "application/javascript; charset=utf-8"
	if r.URL.Path == "/static/dashboard.css" {
		assetName = "dashboard_static/dashboard.css"
		contentType = "text/css; charset=utf-8"
	}
	asset, err := dashboardAssets.ReadFile(assetName)
	if err != nil {
		h.writeError(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(asset)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(asset)
}

type dashboardStatus struct {
	WorkerState        string
	Heartbeat          string
	RecordingCount     int64
	PendingExtractions int64
	PendingDeliveries  int64
}

type dashboardRecording struct {
	ID                    int64
	RecordedAt            string
	FirstReceivedAt       string
	Client                string
	Trigger               string
	AudioBytes            int64
	TranscriptionEvidence string
	ReceiveCount          int64
	ExtractionState       string
	DeliveryCount         int64
}

type dashboardRecordingsPage struct {
	Rows     []dashboardRecording
	Page     int
	PageSize int
	Total    int64
	Client   string
	Trigger  string
	State    string
	PrevURL  string
	NextURL  string
}

type dashboardDelivery struct {
	ID                 int64
	TaskIndex          int
	Kind               string
	State              string
	AttemptCount       int
	LastClassification string
	CreationEvidence   string
	TickTickTaskID     string
	TickTickProjectID  string
	CompletedAt        string
	UpdatedAt          string
}

type dashboardDetail struct {
	ID                    int64
	RecordedAt            string
	FirstReceivedAt       string
	LastReceivedAt        string
	Client                string
	Trigger               string
	AudioBytes            int64
	TranscriptionEvidence string
	ReceiveCount          int64
	ExtractionState       string
	Provider              string
	Model                 string
	DeliveryCount         int64
	CompletedDeliveries   int64
	Deliveries            []dashboardDelivery
}

var dashboardTemplates = template.Must(template.New("dashboard").Funcs(template.FuncMap{"pageCount": func(total int64, size int) int64 {
	if total == 0 {
		return 1
	}
	return (total + int64(size) - 1) / int64(size)
}}).Parse(`
{{define "home"}}<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Index 01 Hook dashboard</title>
	<link rel="stylesheet" href="/static/dashboard.css">
	<script src="/static/htmx.min.js" defer></script>
</head>
<body><main>
	<h1>Index 01 Hook dashboard</h1>
	<p>This local dashboard shows operational metadata only. It excludes private content and full fingerprints.</p>
	{{template "content" .}}
</main></body>
</html>{{end}}

{{define "content"}}
	{{template "status" .Status}}
	<form method="get" action="/recordings" hx-get="/recordings" hx-target="#recordings" hx-swap="outerMorph" hx-push-url="true">
		<label>Client <input name="client" value="{{.Recordings.Client}}"></label>
		<label>Trigger <input name="trigger" value="{{.Recordings.Trigger}}"></label>
		<label>State <input name="state" value="{{.Recordings.State}}"></label>
		<button type="submit">Filter</button>
	</form>
	{{template "recordings" .Recordings}}
{{end}}

{{define "status"}}
	<section id="status" hx-get="/status" hx-trigger="every 15s" hx-swap="outerMorph">
		<h2>Operational status</h2>
		<dl>
			<div><dt>Worker state</dt><dd>{{.WorkerState}}</dd></div>
			<div><dt>Last heartbeat</dt><dd>{{.Heartbeat}}</dd></div>
			<div><dt>Recordings</dt><dd>{{.RecordingCount}}</dd></div>
			<div><dt>Pending extractions</dt><dd>{{.PendingExtractions}}</dd></div>
			<div><dt>Pending deliveries</dt><dd>{{.PendingDeliveries}}</dd></div>
		</dl>
	</section>
{{end}}

{{define "recordings"}}
	<section id="recordings">
		<h2>Recordings</h2>
		<p>Page {{.Page}} of {{pageCount .Total .PageSize}}. The filter has {{.Total}} rows.</p>
		<table>
			<thead><tr><th>ID</th><th>Client recorded</th><th>Server received</th><th>Client</th><th>Trigger</th><th>Audio bytes</th><th>Transcription</th><th>Receives</th><th>Extraction</th><th>Deliveries</th></tr></thead>
			<tbody>
			{{range .Rows}}
				<tr><td><a href="/recordings/{{.ID}}">{{.ID}}</a></td><td>{{.RecordedAt}}</td><td>{{.FirstReceivedAt}}</td><td>{{.Client}}</td><td>{{.Trigger}}</td><td>{{.AudioBytes}}</td><td>{{.TranscriptionEvidence}}</td><td>{{.ReceiveCount}}</td><td>{{.ExtractionState}}</td><td>{{.DeliveryCount}}</td></tr>
			{{else}}
				<tr><td colspan="10">No recordings found.</td></tr>
			{{end}}
			</tbody>
		</table>
		<nav>
			{{if .PrevURL}}<a href="{{.PrevURL}}" hx-get="{{.PrevURL}}" hx-target="#recordings" hx-swap="outerMorph" hx-push-url="true">Previous</a>{{end}}
			{{if .NextURL}}<a href="{{.NextURL}}" hx-get="{{.NextURL}}" hx-target="#recordings" hx-swap="outerMorph" hx-push-url="true">Next</a>{{end}}
		</nav>
	</section>
{{end}}

{{define "detail"}}<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Recording {{.ID}}</title>
	<link rel="stylesheet" href="/static/dashboard.css">
	<script src="/static/htmx.min.js" defer></script>
</head>
<body><main>
	<p><a href="/">Dashboard</a></p>
	<h1>Recording {{.ID}}</h1>
	<p>This view contains operational metadata only.</p>
	<section>
		<h2>Recording receipt evidence</h2>
		<p>The server receipt times confirm that Index 01 received this recording.</p>
		<dl>
			<div><dt>Client recorded</dt><dd>{{.RecordedAt}}</dd></div>
			<div><dt>First server receipt</dt><dd>{{.FirstReceivedAt}}</dd></div>
			<div><dt>Last server receipt</dt><dd>{{.LastReceivedAt}}</dd></div>
			<div><dt>Client</dt><dd>{{.Client}}</dd></div>
			<div><dt>Trigger</dt><dd>{{.Trigger}}</dd></div>
			<div><dt>Audio bytes</dt><dd>{{.AudioBytes}}</dd></div>
			<div><dt>Transcription</dt><dd>{{.TranscriptionEvidence}}</dd></div>
			<div><dt>Receives</dt><dd>{{.ReceiveCount}}</dd></div>
			<div><dt>Extraction state</dt><dd>{{.ExtractionState}}</dd></div>
			<div><dt>Provider</dt><dd>{{.Provider}}</dd></div>
			<div><dt>Model</dt><dd>{{.Model}}</dd></div>
			<div><dt>Delivery count</dt><dd>{{.DeliveryCount}}</dd></div>
			<div><dt>Completed deliveries</dt><dd>{{.CompletedDeliveries}}</dd></div>
		</dl>
	</section>
	<section>
		<h2>TickTick delivery evidence</h2>
		<table>
			<thead><tr><th>ID</th><th>Index</th><th>Kind</th><th>State</th><th>Attempts</th><th>Classification</th><th>Creation evidence</th><th>TickTick item ID</th><th>TickTick project ID</th><th>Completed</th><th>Updated</th></tr></thead>
			<tbody>
			{{range .Deliveries}}
				<tr><td>{{.ID}}</td><td>{{.TaskIndex}}</td><td>{{.Kind}}</td><td>{{.State}}</td><td>{{.AttemptCount}}</td><td>{{.LastClassification}}</td><td>{{.CreationEvidence}}</td><td>{{.TickTickTaskID}}</td><td>{{.TickTickProjectID}}</td><td>{{.CompletedAt}}</td><td>{{.UpdatedAt}}</td></tr>
			{{else}}
				<tr><td colspan="11">No delivery items exist.</td></tr>
			{{end}}
			</tbody>
		</table>
	</section>
</main></body>
</html>{{end}}
`))

func (h *dashboardHandler) serveHome(w http.ResponseWriter, r *http.Request) {
	status, err := h.readStatus(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError)
		return
	}
	recordings, err := h.readRecordings(r.Context(), r.URL.Query())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	data := struct {
		Status     dashboardStatus
		Recordings dashboardRecordingsPage
	}{status, recordings}
	name := "home"
	if r.Header.Get("HX-Request") == "true" {
		name = "content"
	}
	h.executeTemplate(w, name, data)
}

func (h *dashboardHandler) serveStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.readStatus(r.Context())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		h.executeTemplate(w, "status", status)
	}
}

func (h *dashboardHandler) serveRecordings(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		h.serveHome(w, r)
		return
	}
	page, err := h.readRecordings(r.Context(), r.URL.Query())
	if err != nil {
		h.writeError(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		h.executeTemplate(w, "recordings", page)
	}
}

func (h *dashboardHandler) serveRecording(w http.ResponseWriter, r *http.Request, rawID string) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		h.writeError(w, http.StatusNotFound)
		return
	}
	detail, err := h.readRecording(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		h.writeError(w, http.StatusNotFound)
		return
	}
	if err != nil {
		h.writeError(w, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		h.executeTemplate(w, "detail", detail)
	}
}

func (h *dashboardHandler) readStatus(ctx context.Context) (dashboardStatus, error) {
	var status dashboardStatus
	var heartbeat sql.NullString
	if err := h.db.QueryRowContext(ctx, `SELECT state, heartbeat_at FROM worker_health WHERE singleton = 1`).Scan(&status.WorkerState, &heartbeat); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return status, err
	}
	if status.WorkerState == "" {
		status.WorkerState = "not recorded"
	}
	if heartbeat.Valid {
		status.Heartbeat = heartbeat.String
	} else {
		status.Heartbeat = "not recorded"
	}
	err := h.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM recordings), (SELECT count(*) FROM extraction_jobs WHERE state NOT IN ('completed', 'review')), (SELECT count(*) FROM delivery_tasks WHERE state NOT IN ('completed', 'review'))`).Scan(&status.RecordingCount, &status.PendingExtractions, &status.PendingDeliveries)
	return status, err
}

func (h *dashboardHandler) readRecordings(ctx context.Context, values url.Values) (dashboardRecordingsPage, error) {
	page := boundedInt(values.Get("page"), 1, 1, 1000000)
	size := boundedInt(values.Get("page_size"), 25, 1, 100)
	client, trigger := boundedFilter(values.Get("client")), boundedFilter(values.Get("trigger"))
	stateValue := values.Get("state")
	if stateValue == "" {
		stateValue = values.Get("status")
	}
	state := boundedFilter(stateValue)
	where := []string{"1 = 1"}
	args := make([]any, 0, 3)
	if client != "" {
		where = append(where, "r.client = ?")
		args = append(args, client)
	}
	if trigger != "" {
		where = append(where, "r.trigger = ?")
		args = append(args, trigger)
	}
	if state != "" {
		where = append(where, "COALESCE(j.workflow_state, 'not-started') = ?")
		args = append(args, state)
	}
	predicate := strings.Join(where, " AND ")
	var total int64
	if err := h.db.QueryRowContext(ctx, "SELECT count(*) FROM recordings r LEFT JOIN extraction_jobs j ON j.recording_id = r.id WHERE "+predicate, args...).Scan(&total); err != nil {
		return dashboardRecordingsPage{}, err
	}
	query := `SELECT r.id, r.recorded_at_ms, r.first_received_at, r.client, r.trigger, r.audio_byte_count,
		length(r.transcription), j.recording_id IS NOT NULL, r.receive_count,
		COALESCE(j.workflow_state, 'not-started'), (SELECT count(*) FROM delivery_tasks d WHERE d.recording_id = r.id)
		FROM recordings r LEFT JOIN extraction_jobs j ON j.recording_id = r.id WHERE ` + predicate + ` ORDER BY r.id DESC LIMIT ? OFFSET ?`
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := h.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return dashboardRecordingsPage{}, err
	}
	defer rows.Close()
	result := dashboardRecordingsPage{Rows: make([]dashboardRecording, 0, size), Page: page, PageSize: size, Total: total, Client: client, Trigger: trigger, State: state}
	for rows.Next() {
		var row dashboardRecording
		var millis int64
		var transcriptionCharacters int64
		var transcriptionQueued bool
		if err := rows.Scan(&row.ID, &millis, &row.FirstReceivedAt, &row.Client, &row.Trigger, &row.AudioBytes, &transcriptionCharacters, &transcriptionQueued, &row.ReceiveCount, &row.ExtractionState, &row.DeliveryCount); err != nil {
			return dashboardRecordingsPage{}, err
		}
		row.RecordedAt = time.UnixMilli(millis).UTC().Format(time.RFC3339)
		row.TranscriptionEvidence = transcriptionEvidence(transcriptionCharacters, transcriptionQueued)
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return dashboardRecordingsPage{}, err
	}
	if page > 1 {
		result.PrevURL = recordingsURL(page-1, size, client, trigger, state)
	}
	if int64(page*size) < total {
		result.NextURL = recordingsURL(page+1, size, client, trigger, state)
	}
	return result, nil
}

func (h *dashboardHandler) readRecording(ctx context.Context, id int64) (dashboardDetail, error) {
	var detail dashboardDetail
	var millis int64
	var transcriptionCharacters int64
	var transcriptionQueued bool
	err := h.db.QueryRowContext(ctx, `SELECT r.id, r.recorded_at_ms, r.first_received_at, r.last_received_at,
		length(r.transcription), j.recording_id IS NOT NULL, r.client, r.trigger, r.audio_byte_count, r.receive_count,
		COALESCE(j.workflow_state, 'not-started'), COALESCE(e.provider, ''), COALESCE(e.model, ''),
		(SELECT count(*) FROM delivery_tasks d WHERE d.recording_id = r.id),
		(SELECT count(*) FROM delivery_tasks d WHERE d.recording_id = r.id AND d.workflow_state = 'complete')
		FROM recordings r
		LEFT JOIN extraction_jobs j ON j.recording_id = r.id
		LEFT JOIN extractions e ON e.recording_id = r.id
		WHERE r.id = ?`, id).Scan(&detail.ID, &millis, &detail.FirstReceivedAt, &detail.LastReceivedAt, &transcriptionCharacters, &transcriptionQueued, &detail.Client, &detail.Trigger, &detail.AudioBytes, &detail.ReceiveCount, &detail.ExtractionState, &detail.Provider, &detail.Model, &detail.DeliveryCount, &detail.CompletedDeliveries)
	if err != nil {
		return detail, err
	}
	detail.RecordedAt = time.UnixMilli(millis).UTC().Format(time.RFC3339)
	detail.TranscriptionEvidence = transcriptionEvidence(transcriptionCharacters, transcriptionQueued)
	rows, err := h.db.QueryContext(ctx, `SELECT id, task_index, item_kind, workflow_state, attempt_count,
		COALESCE(last_classification, ''), COALESCE(ticktick_task_id, ''),
		COALESCE(ticktick_project_id, ''), COALESCE(completed_at, ''), updated_at
		FROM delivery_tasks WHERE recording_id = ? ORDER BY task_index, id`, id)
	if err != nil {
		return detail, err
	}
	defer rows.Close()
	detail.Deliveries = make([]dashboardDelivery, 0, detail.DeliveryCount)
	for rows.Next() {
		var delivery dashboardDelivery
		if err := rows.Scan(&delivery.ID, &delivery.TaskIndex, &delivery.Kind, &delivery.State, &delivery.AttemptCount, &delivery.LastClassification, &delivery.TickTickTaskID, &delivery.TickTickProjectID, &delivery.CompletedAt, &delivery.UpdatedAt); err != nil {
			return detail, err
		}
		delivery.CreationEvidence = tickTickCreationEvidence(delivery.TickTickTaskID, delivery.LastClassification)
		detail.Deliveries = append(detail.Deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return detail, err
	}
	return detail, nil
}

func transcriptionEvidence(characters int64, queued bool) string {
	if characters > 0 {
		return fmt.Sprintf("received (%d characters)", characters)
	}
	if queued {
		return "received; text removed after processing"
	}
	return "not received"
}

func tickTickCreationEvidence(taskID, classification string) string {
	if taskID == "" {
		return "not created"
	}
	if classification == "reconciled" {
		return "reconciled"
	}
	return "created"
}

func boundedFilter(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 200 {
		return value[:200]
	}
	return value
}

func boundedInt(value string, fallback, minimum, maximum int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return fallback
	}
	return parsed
}

func recordingsURL(page, size int, client, trigger, state string) string {
	values := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(size)}}
	if client != "" {
		values.Set("client", client)
	}
	if trigger != "" {
		values.Set("trigger", trigger)
	}
	if state != "" {
		values.Set("state", state)
	}
	return "/recordings?" + values.Encode()
}

func (h *dashboardHandler) executeTemplate(w http.ResponseWriter, name string, data any) {
	if err := dashboardTemplates.ExecuteTemplate(w, name, data); err != nil && h.logger != nil {
		h.logger.Error("render dashboard", "error", err)
	}
}

func (h *dashboardHandler) writeError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	if status != http.StatusMethodNotAllowed {
		_, _ = io.WriteString(w, "dashboard request failed\n")
	}
}
