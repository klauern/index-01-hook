package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"modernc.org/sqlite"
)

type operatorResult struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

type versionResult struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

type fileOperationResult struct {
	State string `json:"state"`
}

type purgeResult struct {
	State             string `json:"state"`
	RecordingsDeleted int64  `json:"recordings_deleted"`
	RetentionDays     int    `json:"retention_days"`
}

type renamedFile struct {
	current   string
	preserved string
}

func execute(logger *slog.Logger, args []string, getenv func(string) string, output io.Writer) error {
	return executeWithInput(logger, args, getenv, nil, output)
}

func executeWithInput(logger *slog.Logger, args []string, getenv func(string) string, input io.Reader, output io.Writer) error {
	if len(args) == 0 || len(args) == 1 && args[0] == "serve" {
		return runWithEnvironment(logger, getenv)
	}
	if len(args) == 1 && args[0] == "version" {
		return json.NewEncoder(output).Encode(versionResult{Version: version, Commit: commit, BuildDate: buildDate})
	}
	if len(args) == 1 && args[0] == "healthcheck" {
		return runHealthcheck(context.Background(), http.DefaultTransport, output)
	}
	if len(args) == 1 && args[0] == "maintenance" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		return nil
	}
	if len(args) == 1 && args[0] == "ticktick-projects" {
		return runTickTickProjects(context.Background(), getenv("INDEX01_TICKTICK_TOKEN"), http.DefaultTransport, output)
	}
	if len(args) == 1 && args[0] == "purge-expired" {
		if getenv("INDEX01_PURGE_CONFIRM") != "purge-expired-recordings" {
			return fmt.Errorf("INDEX01_PURGE_CONFIRM must equal purge-expired-recordings")
		}
		databasePath, err := normalizeDatabasePath(getenv("INDEX01_DB_PATH"))
		if err != nil {
			return err
		}
		store, err := OpenStore(context.Background(), databasePath)
		if err != nil {
			return err
		}
		defer ignoreCloseError(store)
		count, err := store.PurgeExpiredRecordings(context.Background(), terminalRecordRetention)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(purgeResult{
			State: "purged", RecordingsDeleted: count,
			RetentionDays: int(terminalRecordRetention / (24 * time.Hour)),
		})
	}
	if len(args) == 2 && args[0] == "restore" {
		databasePath, err := normalizeDatabasePath(getenv("INDEX01_DB_PATH"))
		if err != nil {
			return err
		}
		var restoreErr error
		if args[1] == "-" {
			if input == nil {
				return fmt.Errorf("restore input is required")
			}
			restoreErr = restoreDatabaseFromReader(databasePath, input, time.Now())
		} else {
			restoreErr = restoreDatabase(databasePath, args[1], time.Now())
		}
		if restoreErr != nil {
			return restoreErr
		}
		return json.NewEncoder(output).Encode(fileOperationResult{State: "restored"})
	}
	if len(args) != 2 {
		return fmt.Errorf("usage: index-01-hook [serve|version|healthcheck|maintenance|purge-expired|ticktick-projects|status ID|retry-recording ID|retry-delivery ID|backup PATH|restore PATH]")
	}
	if args[0] != "status" && args[0] != "retry-recording" && args[0] != "retry-delivery" && args[0] != "backup" {
		return fmt.Errorf("unknown operator command %q", args[0])
	}
	var id int64
	var err error
	if args[0] != "backup" {
		id, err = strconv.ParseInt(args[1], 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("operator ID must be a positive integer")
		}
	}
	databasePath, err := normalizeDatabasePath(getenv("INDEX01_DB_PATH"))
	if err != nil {
		return err
	}
	if args[0] == "backup" {
		if args[1] != "-" {
			return fmt.Errorf("backup destination must be - for encrypted export")
		}
		return backupDatabase(context.Background(), databasePath, args[1], output)
	}
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		return err
	}
	defer ignoreCloseError(store)
	encoder := json.NewEncoder(output)
	switch args[0] {
	case "status":
		status, err := store.RecordingStatus(context.Background(), id)
		if err != nil {
			return err
		}
		return encoder.Encode(status)
	case "retry-recording":
		if err := store.RetryRecordingByID(context.Background(), id); err != nil {
			return err
		}
		return encoder.Encode(operatorResult{ID: id, State: "received"})
	case "retry-delivery":
		if err := store.RetryDeliveryByID(context.Background(), id); err != nil {
			return err
		}
		return encoder.Encode(operatorResult{ID: id, State: "extracted"})
	}
	return fmt.Errorf("unknown operator command %q", args[0])
}

// runHealthcheck verifies the private HTTP health endpoint without exposing
// response content. transport is injectable for synthetic tests.
func runHealthcheck(ctx context.Context, transport http.RoundTripper, output io.Writer) error {
	if transport == nil {
		transport = http.DefaultTransport
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:8080/healthz", nil)
	if err != nil {
		return fmt.Errorf("create healthcheck request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("healthcheck request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned status %d", response.StatusCode)
	}
	_, err = io.WriteString(output, "{\"status\":\"ok\"}\n")
	if err != nil {
		return fmt.Errorf("write healthcheck result: %w", err)
	}
	return nil
}

// runTickTickProjects lists only safe TickTick project summaries.
// transport is injectable for synthetic tests; production uses the fixed HTTPS API.
func runTickTickProjects(ctx context.Context, token string, transport http.RoundTripper, output io.Writer) error {
	if transport == nil {
		transport = http.DefaultTransport
	}
	client, err := NewTickTickClient(tickTickAPIBaseURL, token, &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	})
	if err != nil {
		return err
	}
	summaries, err := client.ListProjectSummaries(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(summaries)
}

func (s *Store) Backup(ctx context.Context, destination string) error {
	var sourcePath string
	rows, err := s.db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return fmt.Errorf("query database path: %w", err)
	}
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan database path: %w", err)
		}
		if name == "main" {
			sourcePath = path
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close database path query: %w", err)
	}
	return createSQLiteBackup(ctx, sourcePath, destination)
}

func backupDatabase(ctx context.Context, sourcePath, destination string, output io.Writer) error {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return fmt.Errorf("backup path is required")
	}
	if destination != "-" {
		if err := createSQLiteBackup(ctx, sourcePath, destination); err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(fileOperationResult{State: "created"})
	}
	absSource, err := filepath.Abs(strings.TrimSpace(sourcePath))
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	temporaryDirectory, err := os.MkdirTemp(filepath.Dir(absSource), ".index-01-hook-backup-")
	if err != nil {
		return fmt.Errorf("create backup temporary directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryDirectory) }()
	// #nosec G302 -- A private directory requires the owner execute bit.
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		return fmt.Errorf("protect backup temporary directory: %w", err)
	}
	temporaryPath := filepath.Join(temporaryDirectory, "backup.db")
	if err := createSQLiteBackup(ctx, sourcePath, temporaryPath); err != nil {
		return err
	}
	// #nosec G304 -- MkdirTemp creates temporaryPath inside the database directory.
	backup, err := os.Open(temporaryPath)
	if err != nil {
		return fmt.Errorf("open verified SQLite backup: %w", err)
	}
	defer ignoreCloseError(backup)
	if _, err := io.Copy(output, backup); err != nil {
		return fmt.Errorf("stream SQLite backup: %w", err)
	}
	return nil
}

type sqliteOnlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func createSQLiteBackup(ctx context.Context, sourcePath, destination string) error {
	sourcePath = strings.TrimSpace(sourcePath)
	destination = strings.TrimSpace(destination)
	if sourcePath == "" {
		return fmt.Errorf("database path is required")
	}
	if destination == "" {
		return fmt.Errorf("backup path is required")
	}
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup path: %w", err)
	}
	if absSource == absDestination {
		return fmt.Errorf("backup path must differ from database path")
	}
	// #nosec G304 -- The operator supplies the exact absolute destination. O_EXCL prevents replacement.
	destinationFile, err := os.OpenFile(absDestination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("claim backup path exclusively: %w", err)
	}
	if err := destinationFile.Close(); err != nil {
		_ = os.Remove(absDestination)
		return fmt.Errorf("close claimed backup path: %w", err)
	}
	removeDestination := true
	defer func() {
		if removeDestination {
			_ = os.Remove(absDestination)
		}
	}()
	sourceDSN := sqliteFileDSN(absSource, "ro")
	destinationDSN := sqliteFileDSN(absDestination, "rw")
	db, err := sql.Open("sqlite", sourceDSN)
	if err != nil {
		return fmt.Errorf("open source database read-only: %w", err)
	}
	defer ignoreCloseError(db)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	connection, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect to source database read-only: %w", err)
	}
	defer ignoreCloseError(connection)
	if err := connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(sqliteOnlineBackuper)
		if !ok {
			return fmt.Errorf("SQLite driver does not support online backup")
		}
		backup, err := backuper.NewBackup(destinationDSN)
		if err != nil {
			return fmt.Errorf("start SQLite online backup: %w", err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for more := true; more; {
			more, err = backup.Step(-1)
			if err != nil {
				return fmt.Errorf("copy SQLite online backup: %w", err)
			}
		}
		finished = true
		if err := backup.Finish(); err != nil {
			return fmt.Errorf("finish SQLite online backup: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := os.Chmod(absDestination, 0o600); err != nil {
		return fmt.Errorf("protect SQLite backup: %w", err)
	}
	if err := validateSQLiteBackup(absDestination); err != nil {
		return fmt.Errorf("validate SQLite backup: %w", err)
	}
	removeDestination = false
	return nil
}

func sqliteFileDSN(path, mode string) string {
	query := url.Values{}
	query.Set("mode", mode)
	query.Add("_pragma", "busy_timeout(5000)")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: query.Encode()}).String()
}

func restoreDatabase(databasePath, backupPath string, now time.Time) error {
	return restoreDatabaseWithValidator(databasePath, backupPath, now, validateSQLiteBackup)
}

func restoreDatabaseFromReader(databasePath string, input io.Reader, now time.Time) error {
	absDatabase, err := filepath.Abs(databasePath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	if err := protectSQLiteStorage(absDatabase); err != nil {
		return err
	}
	temporaryDirectory, err := os.MkdirTemp(filepath.Dir(absDatabase), ".index-01-hook-restore-")
	if err != nil {
		return fmt.Errorf("create restore staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryDirectory) }()
	// #nosec G302 -- A private directory requires the owner execute bit.
	if err := os.Chmod(temporaryDirectory, 0o700); err != nil {
		return fmt.Errorf("protect restore staging directory: %w", err)
	}
	temporaryPath := filepath.Join(temporaryDirectory, "restore.db")
	// #nosec G304 -- MkdirTemp creates temporaryPath inside the database directory.
	staged, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create restore staging file: %w", err)
	}
	if _, err := io.Copy(staged, input); err != nil {
		_ = staged.Close()
		return fmt.Errorf("stage restore database: %w", err)
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		return fmt.Errorf("sync restore staging file: %w", err)
	}
	if err := staged.Close(); err != nil {
		return fmt.Errorf("close restore staging file: %w", err)
	}
	if err := restoreDatabase(absDatabase, temporaryPath, now); err != nil {
		return err
	}
	return nil
}

func restoreDatabaseWithValidator(databasePath, backupPath string, now time.Time, validate func(string) error) error {
	absDatabase, err := filepath.Abs(databasePath)
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}
	absBackup, err := filepath.Abs(strings.TrimSpace(backupPath))
	if err != nil || strings.TrimSpace(backupPath) == "" {
		return fmt.Errorf("resolve restore path")
	}
	if absDatabase == absBackup {
		return fmt.Errorf("restore path must differ from database path")
	}
	if err := validate(absBackup); err != nil {
		return err
	}
	suffix := ".pre-restore-" + now.UTC().Format("20060102T150405Z")
	renamed := make([]renamedFile, 0, 3)
	for _, path := range []string{absDatabase, absDatabase + "-wal", absDatabase + "-shm"} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			rollbackRenames(renamed)
			return fmt.Errorf("inspect current database file: %w", err)
		}
		preserved := path + suffix
		if _, err := os.Stat(preserved); err == nil {
			rollbackRenames(renamed)
			return fmt.Errorf("preserved database path already exists")
		} else if !os.IsNotExist(err) {
			rollbackRenames(renamed)
			return fmt.Errorf("inspect preserved database path: %w", err)
		}
		if err := os.Rename(path, preserved); err != nil {
			rollbackRenames(renamed)
			return fmt.Errorf("preserve current database file: %w", err)
		}
		renamed = append(renamed, renamedFile{current: path, preserved: preserved})
	}
	if err := copyFile(absBackup, absDatabase); err != nil {
		rollbackRenames(renamed)
		return err
	}
	if err := validate(absDatabase); err != nil {
		_ = os.Remove(absDatabase)
		rollbackRenames(renamed)
		return fmt.Errorf("validate installed restore database: %w", err)
	}
	if err := protectSQLiteStorage(absDatabase); err != nil {
		_ = os.Remove(absDatabase)
		rollbackRenames(renamed)
		return err
	}
	return nil
}

func validateSQLiteBackup(path string) error {
	dsn := sqliteFileDSN(path, "ro")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open restore database: %w", err)
	}
	defer ignoreCloseError(db)
	if err := validateSQLiteIntegrity(context.Background(), db); err != nil {
		return fmt.Errorf("restore database failed integrity check: %w", err)
	}
	if err := validateApplicationDatabase(context.Background(), db); err != nil {
		return fmt.Errorf("restore database identity is invalid: %w", err)
	}
	return nil
}

func rollbackRenames(renamed []renamedFile) {
	for index := len(renamed) - 1; index >= 0; index-- {
		_ = os.Rename(renamed[index].preserved, renamed[index].current)
	}
}

func copyFile(sourcePath, destinationPath string) error {
	// #nosec G304 -- Restore validates the exact operator-supplied source before this copy.
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open restore database: %w", err)
	}
	defer ignoreCloseError(source)
	// #nosec G304 -- destinationPath is the configured database path. O_EXCL prevents replacement.
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create restored database: %w", err)
	}
	removeDestination := true
	defer func() {
		_ = destination.Close()
		if removeDestination {
			_ = os.Remove(destinationPath)
		}
	}()
	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("copy restore database: %w", err)
	}
	if err := destination.Sync(); err != nil {
		return fmt.Errorf("sync restored database: %w", err)
	}
	if err := destination.Close(); err != nil {
		return fmt.Errorf("close restored database: %w", err)
	}
	removeDestination = false
	return nil
}
