package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOperatorStatusAndExactIDRetryAreRedacted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.db")
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	receipt, err := store.SaveRecording(context.Background(), RecordingInput{
		RecordedAtMillis: 1760000000000, Client: "test",
		Transcription: "private transcript", Fingerprint: queueTestHash,
	})
	if err != nil {
		t.Fatalf("SaveRecording() error = %v", err)
	}
	claim, err := store.ClaimExtraction(context.Background(), "operator-test", time.Minute)
	if err != nil || claim == nil {
		t.Fatalf("ClaimExtraction() = %+v, %v", claim, err)
	}
	if err := store.BlockExtraction(context.Background(), receipt.ID, "operator-test", OutcomeReview, "needs_review"); err != nil {
		t.Fatalf("BlockExtraction() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	getenv := func(key string) string {
		if key == "INDEX01_DB_PATH" {
			return path
		}
		return ""
	}
	var output bytes.Buffer
	if err := execute(logger, []string{"status", "1"}, getenv, &output); err != nil {
		t.Fatalf("execute(status) error = %v", err)
	}
	for _, privateValue := range []string{"private transcript", "token-value", "raw provider body"} {
		if strings.Contains(output.String(), privateValue) {
			t.Errorf("status contains private value %q: %s", privateValue, output.String())
		}
	}
	var status RecordingQueueStatus
	if err := json.Unmarshal(output.Bytes(), &status); err != nil || status.State != "needs_review" {
		t.Fatalf("status output = %s, error = %v", output.String(), err)
	}
	output.Reset()
	if err := execute(logger, []string{"retry-recording", "1"}, getenv, &output); err != nil {
		t.Fatalf("execute(retry-recording) error = %v", err)
	}
	if output.String() != "{\"id\":1,\"state\":\"received\"}\n" {
		t.Fatalf("retry output = %q", output.String())
	}
}

func TestOperatorRejectsInvalidCommands(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, args := range [][]string{{"status"}, {"status", "zero"}, {"unknown", "1"}, {"backup", "backup.db"}} {
		if err := execute(logger, args, func(string) string { return "" }, io.Discard); err == nil {
			t.Errorf("execute(%v) returned no error", args)
		}
	}
}

func TestOperatorBackupAndRestorePreserveCurrentDatabase(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "database.db")
	backupPath := filepath.Join(directory, "backup.db")
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	first := saveQueueRecording(t, store, "first transcript")
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	getenv := func(key string) string {
		if key == "INDEX01_DB_PATH" {
			return databasePath
		}
		return ""
	}
	if err := createSQLiteBackup(context.Background(), databasePath, backupPath); err != nil {
		t.Fatalf("createSQLiteBackup() error = %v", err)
	}
	backupInfo, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if backupInfo.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %04o, want 0600", backupInfo.Mode().Perm())
	}
	store, err = OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	_, err = store.SaveRecording(context.Background(), RecordingInput{
		RecordedAtMillis: 1760000000001, Client: "test", Transcription: "second transcript",
		Fingerprint: strings.Repeat("d", 64),
	})
	if err != nil {
		t.Fatalf("save second recording: %v", err)
	}
	_ = store.Close()
	if err := execute(logger, []string{"restore", backupPath}, getenv, io.Discard); err != nil {
		t.Fatalf("execute(restore) error = %v", err)
	}
	store, err = OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open restored database: %v", err)
	}
	defer ignoreCloseError(store)
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM recordings`).Scan(&count); err != nil {
		t.Fatalf("count restored recordings: %v", err)
	}
	if count != 1 || first.ID != 1 {
		t.Fatalf("restored recording count = %d, want 1", count)
	}
	var queueState, workflowState string
	if err := store.db.QueryRow(`SELECT state, workflow_state FROM extraction_jobs WHERE recording_id = ?`, first.ID).Scan(&queueState, &workflowState); err != nil {
		t.Fatalf("read restored queue state: %v", err)
	}
	if queueState != "pending" || workflowState != "received" {
		t.Fatalf("restored queue state = %q, %q", queueState, workflowState)
	}
	matches, err := filepath.Glob(databasePath + ".pre-restore-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("preserved database files = %v, error = %v", matches, err)
	}
}

func TestOperatorRestoreFromInputCreatesNewDatabaseAndRemovesStaging(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.db")
	backupPath := filepath.Join(directory, "backup.db")
	targetPath := filepath.Join(directory, "new-volume", "data", "index01.db")
	createDatabaseWithRecording(t, sourcePath, "streamed restore transcript", strings.Repeat("f", 64))
	if err := createSQLiteBackup(context.Background(), sourcePath, backupPath); err != nil {
		t.Fatalf("createSQLiteBackup() error = %v", err)
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	var output bytes.Buffer
	if err := executeWithInput(discardLogger(), []string{"restore", "-"}, databaseEnvironment(targetPath), bytes.NewReader(backup), &output); err != nil {
		t.Fatalf("executeWithInput(restore -) error = %v", err)
	}
	if output.String() != "{\"state\":\"restored\"}\n" {
		t.Fatalf("restore output = %q", output.String())
	}
	store, err := OpenStore(context.Background(), targetPath)
	if err != nil {
		t.Fatalf("OpenStore(restored database) error = %v", err)
	}
	defer ignoreCloseError(store)
	status, err := store.RecordingStatus(context.Background(), 1)
	if err != nil {
		t.Fatalf("RecordingStatus() error = %v", err)
	}
	if status.State != "received" || status.RecordingID != 1 {
		t.Fatalf("restored status = %+v", status)
	}
	assertPathMode(t, filepath.Dir(targetPath), 0o700)
	assertPathMode(t, targetPath, 0o600)
	staging, err := filepath.Glob(filepath.Join(filepath.Dir(targetPath), ".index-01-hook-restore-*"))
	if err != nil {
		t.Fatalf("find restore staging paths: %v", err)
	}
	if len(staging) != 0 {
		t.Fatalf("restore staging paths remain: %v", staging)
	}
}

func TestOperatorRestoreFromInvalidInputPreservesCurrentDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "data", "index01.db")
	createDatabaseWithRecording(t, databasePath, "preserve live database", strings.Repeat("1", 64))
	err := executeWithInput(discardLogger(), []string{"restore", "-"}, databaseEnvironment(databasePath), strings.NewReader("not a database"), io.Discard)
	if err == nil {
		t.Fatal("executeWithInput(restore -) accepted invalid input")
	}
	assertDatabaseRecordingCount(t, databasePath, 1)
}

func TestOperatorBackupStdoutIsVerifiedSQLiteSnapshot(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "database.db")
	temporaryRoot := filepath.Join(directory, "temporary")
	if err := os.Mkdir(temporaryRoot, 0o700); err != nil {
		t.Fatalf("create temporary root: %v", err)
	}
	t.Setenv("TMPDIR", temporaryRoot)
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer ignoreCloseError(store)
	if _, err := store.db.Exec(`PRAGMA wal_autocheckpoint = 0`); err != nil {
		t.Fatalf("disable automatic WAL checkpoint: %v", err)
	}
	receipt := saveQueueRecording(t, store, "backup queue transcript")
	if _, err := os.Stat(databasePath + "-wal"); err != nil {
		t.Fatalf("WAL state is not present: %v", err)
	}
	databaseBefore, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("read source database: %v", err)
	}
	walBefore, err := os.ReadFile(databasePath + "-wal")
	if err != nil {
		t.Fatalf("read source WAL: %v", err)
	}

	var output bytes.Buffer
	if err := execute(discardLogger(), []string{"backup", "-"}, databaseEnvironment(databasePath), &output); err != nil {
		t.Fatalf("execute(backup -) error = %v", err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("backup header = %q", output.Bytes()[:min(output.Len(), 16)])
	}
	backupPath := filepath.Join(directory, "stdout-backup.db")
	if err := os.WriteFile(backupPath, output.Bytes(), 0o600); err != nil {
		t.Fatalf("write streamed backup: %v", err)
	}
	if err := validateSQLiteBackup(backupPath); err != nil {
		t.Fatalf("validate streamed backup: %v", err)
	}
	backupDB, err := sql.Open("sqlite", sqliteFileDSN(backupPath, "ro"))
	if err != nil {
		t.Fatalf("open streamed backup: %v", err)
	}
	defer ignoreCloseError(backupDB)
	var pageCount, pageSize, recordingID int64
	if err := backupDB.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		t.Fatalf("read backup page count: %v", err)
	}
	if err := backupDB.QueryRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatalf("read backup page size: %v", err)
	}
	if got, want := int64(output.Len()), pageCount*pageSize; got != want {
		t.Fatalf("streamed byte count = %d, want exact SQLite size %d", got, want)
	}
	var transcript, queueState, workflowState string
	if err := backupDB.QueryRow(`
		SELECT r.id, r.transcription, j.state, j.workflow_state
		FROM recordings r JOIN extraction_jobs j ON j.recording_id = r.id
		WHERE r.id = ?`, receipt.ID).Scan(&recordingID, &transcript, &queueState, &workflowState); err != nil {
		t.Fatalf("read queue state from backup: %v", err)
	}
	if recordingID != receipt.ID || transcript != "backup queue transcript" || queueState != "pending" || workflowState != "received" {
		t.Fatalf("backup queue state = %d, %q, %q, %q", recordingID, transcript, queueState, workflowState)
	}
	assertFileBytesEqual(t, databasePath, databaseBefore)
	assertFileBytesEqual(t, databasePath+"-wal", walBefore)
	assertDirectoryEmpty(t, temporaryRoot)
	assertNoBackupTemporaryDirectories(t, directory)
}

func TestOperatorBackupStdoutRejectsUnrelatedSourceWithoutModification(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "legacy.db")
	db, err := sql.Open("sqlite", sqliteFileDSN(databasePath, "rwc"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE sentinel (value TEXT NOT NULL); INSERT INTO sentinel VALUES ('preserve')`); err != nil {
		t.Fatalf("create legacy database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	sourceBefore, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("read legacy database: %v", err)
	}
	var output bytes.Buffer
	if err := execute(discardLogger(), []string{"backup", "-"}, databaseEnvironment(databasePath), &output); err == nil {
		t.Fatal("execute(backup -) succeeded for unrelated database")
	}
	if output.Len() != 0 {
		t.Fatalf("execute(backup -) wrote %d bytes", output.Len())
	}
	assertFileBytesEqual(t, databasePath, sourceBefore)
	db, err = sql.Open("sqlite", sqliteFileDSN(databasePath, "ro"))
	if err != nil {
		t.Fatalf("reopen source read-only: %v", err)
	}
	defer ignoreCloseError(db)
	var migrationTableCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&migrationTableCount); err != nil {
		t.Fatalf("inspect source schema: %v", err)
	}
	if migrationTableCount != 0 {
		t.Fatalf("backup created schema_migrations in the source")
	}
}

func TestOperatorBackupStdoutUsesDatabaseDirectoryWhenSystemTempIsUnusable(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	saveQueueRecording(t, store, "database directory temporary backup")
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	unusableTemp := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(unusableTemp, []byte("file"), 0o600); err != nil {
		t.Fatalf("create unusable system temporary path: %v", err)
	}
	t.Setenv("TMPDIR", unusableTemp)
	sourceBefore, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("read source database: %v", err)
	}

	var output bytes.Buffer
	if err := execute(discardLogger(), []string{"backup", "-"}, databaseEnvironment(databasePath), &output); err != nil {
		t.Fatalf("execute(backup -) error = %v", err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatalf("backup header = %q", output.Bytes()[:min(output.Len(), 16)])
	}
	assertFileBytesEqual(t, databasePath, sourceBefore)
	assertNoBackupTemporaryDirectories(t, directory)
}

func TestOperatorBackupPathRejectsDanglingSymlink(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	saveQueueRecording(t, store, "dangling symlink backup")
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	missingTarget := filepath.Join(directory, "missing-target.db")
	backupPath := filepath.Join(directory, "backup.db")
	if err := os.Symlink(missingTarget, backupPath); err != nil {
		t.Fatalf("create dangling backup symlink: %v", err)
	}
	if err := createSQLiteBackup(context.Background(), databasePath, backupPath); err == nil {
		t.Fatal("createSQLiteBackup() accepted dangling symlink")
	}
	info, err := os.Lstat(backupPath)
	if err != nil {
		t.Fatalf("inspect dangling backup symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("backup path mode = %v, want symlink", info.Mode())
	}
	if _, err := os.Stat(missingTarget); !os.IsNotExist(err) {
		t.Fatalf("dangling symlink target exists or returned unexpected error: %v", err)
	}
}

func TestOperatorBackupPathConcurrentCreationDoesNotClobber(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "source.db")
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	saveQueueRecording(t, store, "concurrent backup")
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	backupPath := filepath.Join(directory, "backup.db")
	start := make(chan struct{})
	results := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for range 2 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			results <- createSQLiteBackup(context.Background(), databasePath, backupPath)
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	successCount := 0
	failureCount := 0
	for err := range results {
		if err == nil {
			successCount++
		} else {
			failureCount++
		}
	}
	if successCount != 1 || failureCount != 1 {
		t.Fatalf("concurrent backup results = %d successes, %d failures", successCount, failureCount)
	}
	if err := validateSQLiteBackup(backupPath); err != nil {
		t.Fatalf("validate concurrent backup winner: %v", err)
	}
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat concurrent backup winner: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("concurrent backup mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestOperatorBackupStdoutFailureIsBinarySafeAndCleansTemporaryFiles(t *testing.T) {
	for _, test := range []struct {
		name          string
		prepareSource func(*testing.T, string)
	}{
		{name: "missing source"},
		{name: "invalid source", prepareSource: func(t *testing.T, path string) {
			t.Helper()
			if err := os.WriteFile(path, []byte("not a SQLite database"), 0o600); err != nil {
				t.Fatalf("write invalid source: %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			temporaryRoot := filepath.Join(directory, "temporary")
			if err := os.Mkdir(temporaryRoot, 0o700); err != nil {
				t.Fatalf("create temporary root: %v", err)
			}
			t.Setenv("TMPDIR", temporaryRoot)
			databasePath := filepath.Join(directory, "source.db")
			if test.prepareSource != nil {
				test.prepareSource(t, databasePath)
			}
			var output bytes.Buffer
			if err := execute(discardLogger(), []string{"backup", "-"}, databaseEnvironment(databasePath), &output); err == nil {
				t.Fatalf("execute(backup -) returned no error")
			}
			if output.Len() != 0 {
				t.Fatalf("failed backup wrote %d stdout bytes", output.Len())
			}
			assertDirectoryEmpty(t, temporaryRoot)
			assertNoBackupTemporaryDirectories(t, directory)
		})
	}
}

func TestOperatorBackupStdoutCleansTemporaryFilesAfterWriteFailure(t *testing.T) {
	directory := t.TempDir()
	temporaryRoot := filepath.Join(directory, "temporary")
	if err := os.Mkdir(temporaryRoot, 0o700); err != nil {
		t.Fatalf("create temporary root: %v", err)
	}
	t.Setenv("TMPDIR", temporaryRoot)
	databasePath := filepath.Join(directory, "source.db")
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	saveQueueRecording(t, store, "write failure transcript")
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	writer := &failingWriter{remaining: 32}
	if err := execute(discardLogger(), []string{"backup", "-"}, databaseEnvironment(databasePath), writer); err == nil || !strings.Contains(err.Error(), "stream SQLite backup") {
		t.Fatalf("execute(backup -) error = %v", err)
	}
	assertDirectoryEmpty(t, temporaryRoot)
	assertNoBackupTemporaryDirectories(t, directory)
}

func TestRunMainKeepsBackupDiagnosticsOffStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"backup", "-"}, databaseEnvironment(filepath.Join(t.TempDir(), "missing.db")), strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runMain() code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("runMain() wrote %d stdout bytes", stdout.Len())
	}
	if !strings.Contains(stderr.String(), "server stopped") || !strings.Contains(stderr.String(), "error") {
		t.Fatalf("runMain() stderr = %q", stderr.String())
	}
}

type failingWriter struct {
	remaining int
}

func (w *failingWriter) Write(data []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errors.New("synthetic write failure")
	}
	if len(data) > w.remaining {
		written := w.remaining
		w.remaining = 0
		return written, errors.New("synthetic write failure")
	}
	w.remaining -= len(data)
	return len(data), nil
}

func databaseEnvironment(path string) func(string) string {
	return func(key string) string {
		if key == "INDEX01_DB_PATH" {
			return path
		}
		return ""
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func assertFileBytesEqual(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("file %s changed during backup", path)
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read temporary root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary root contains %v", entries)
	}
}

func assertNoBackupTemporaryDirectories(t *testing.T, path string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(path, ".index-01-hook-backup-*"))
	if err != nil {
		t.Fatalf("find backup temporary directories: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("backup temporary directories remain: %v", matches)
	}
}

func TestOperatorTickTickProjectsNeedsOnlyToken(t *testing.T) {
	calledUnexpectedEnvironment := false
	getenv := func(key string) string {
		if key != "INDEX01_TICKTICK_TOKEN" {
			calledUnexpectedEnvironment = true
			t.Fatalf("ticktick-projects read unexpected environment variable %q", key)
		}
		return ""
	}
	var output bytes.Buffer
	err := execute(discardLogger(), []string{"ticktick-projects"}, getenv, &output)
	if err == nil || !strings.Contains(err.Error(), "token is required") {
		t.Fatalf("execute(ticktick-projects) error = %v, want token error", err)
	}
	if calledUnexpectedEnvironment {
		t.Fatal("ticktick-projects read service configuration")
	}
	if output.Len() != 0 {
		t.Fatalf("ticktick-projects wrote output on configuration error: %q", output.String())
	}
}

func TestOperatorTickTickProjectsUsageIsDocumented(t *testing.T) {
	err := execute(discardLogger(), []string{"unknown"}, func(string) string { return "" }, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "ticktick-projects") {
		t.Fatalf("usage error = %v, want ticktick-projects", err)
	}
}

func TestRunTickTickProjectsUsesFixedAPIAndSafeOutput(t *testing.T) {
	var output bytes.Buffer
	err := runTickTickProjects(context.Background(), testTickTickToken, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || request.URL.Host != "api.ticktick.com" || request.URL.Path != "/open/v1/project" {
			t.Fatalf("request URL = %s, want fixed TickTick HTTPS API project URL", request.URL)
		}
		return fixtureResponse(http.StatusOK, `[{"id":"project-1","name":"private name","closed":false,"kind":"TASK","permission":null}]`), nil
	}), &output)
	if err != nil {
		t.Fatalf("runTickTickProjects() error = %v", err)
	}
	if got, want := output.String(), "[{\"id\":\"project-1\",\"kind\":\"TASK\",\"closed\":false,\"writable\":true}]\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	for _, prohibited := range []string{"private name", testTickTickToken, "permission"} {
		if strings.Contains(output.String(), prohibited) {
			t.Errorf("output contains prohibited value %q: %s", prohibited, output.String())
		}
	}
}

func TestHealthcheckReturnsSafeJSONAndUsesPrivateHealthURL(t *testing.T) {
	var output bytes.Buffer
	err := runHealthcheck(context.Background(), roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.String() != "http://127.0.0.1:8080/healthz" {
			t.Fatalf("healthcheck request = %s %s", request.Method, request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("private response body")),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	}), &output)
	if err != nil {
		t.Fatalf("runHealthcheck() error = %v", err)
	}
	if got, want := output.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("healthcheck output = %q, want %q", got, want)
	}
	if strings.Contains(output.String(), "private response body") {
		t.Fatal("healthcheck output contains the response body")
	}
}

func TestHealthcheckRejectsNonOKAndRedirectResponses(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var output bytes.Buffer
			err := runHealthcheck(context.Background(), roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: status,
					Body:       io.NopCloser(strings.NewReader("private response body")),
					Header:     make(http.Header),
					Request:    request,
				}, nil
			}), &output)
			if err == nil || !strings.Contains(err.Error(), "status "+strconv.Itoa(status)) {
				t.Fatalf("runHealthcheck() error = %v, want status %d", err, status)
			}
			if output.Len() != 0 {
				t.Fatalf("healthcheck output on failure = %q", output.String())
			}
			if strings.Contains(err.Error(), "private response body") {
				t.Fatal("healthcheck error contains the response body")
			}
		})
	}
}
