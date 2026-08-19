package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRestoreRejectsInvalidApplicationDatabases(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "unrelated SQLite database",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatalf("open unrelated database: %v", err)
				}
				defer ignoreCloseError(db)
				if _, err := db.Exec(`CREATE TABLE unrelated (value TEXT)`); err != nil {
					t.Fatalf("create unrelated database: %v", err)
				}
			},
		},
		{
			name: "spoofed migration checksum",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				mutateSQLiteDatabase(t, path, `UPDATE schema_migrations SET checksum = ? WHERE version = 1`, strings.Repeat("0", 64))
			},
		},
		{
			name: "erased migration checksum",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				mutateSQLiteDatabase(t, path, `UPDATE schema_migrations SET checksum = '' WHERE version = 1`)
			},
		},
		{
			name: "missing workflow trigger",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				mutateSQLiteDatabase(t, path, `DROP TRIGGER extraction_workflow_transition`)
			},
		},
		{
			name: "foreign key corruption",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				mutateSQLiteDatabase(t, path, `PRAGMA foreign_keys = OFF`)
				mutateSQLiteDatabase(t, path, `DELETE FROM recordings`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			livePath := filepath.Join(directory, "live", "index01.db")
			createDatabaseWithRecording(t, livePath, "live transcript", strings.Repeat("a", 64))

			restorePath := filepath.Join(directory, "restore.db")
			if test.name == "unrelated SQLite database" {
				test.mutate(t, restorePath)
			} else {
				createDatabaseWithRecording(t, restorePath, "restore transcript", strings.Repeat("b", 64))
				test.mutate(t, restorePath)
			}

			err := restoreDatabase(livePath, restorePath, time.Date(2026, time.August, 14, 1, 2, 3, 0, time.UTC))
			if err == nil {
				t.Fatal("restoreDatabase() succeeded for invalid database")
			}
			assertDatabaseRecordingCount(t, livePath, 1)
		})
	}
}

func TestRestoreRevalidatesInstalledCopyAndRollsBack(t *testing.T) {
	directory := t.TempDir()
	livePath := filepath.Join(directory, "live.db")
	restorePath := filepath.Join(directory, "restore.db")
	createDatabaseWithRecording(t, livePath, "live transcript", strings.Repeat("c", 64))
	createDatabaseWithRecording(t, restorePath, "restore transcript", strings.Repeat("d", 64))

	validationCount := 0
	validator := func(path string) error {
		validationCount++
		if validationCount == 2 {
			return errors.New("synthetic installed-copy failure")
		}
		return validateSQLiteBackup(path)
	}
	err := restoreDatabaseWithValidator(livePath, restorePath, time.Now(), validator)
	if err == nil || !strings.Contains(err.Error(), "validate installed restore database") {
		t.Fatalf("restoreDatabaseWithValidator() error = %v", err)
	}
	if validationCount != 2 {
		t.Fatalf("validation count = %d, want 2", validationCount)
	}
	assertDatabaseRecordingCount(t, livePath, 1)
}

func TestOpenStoreProtectsSQLiteDirectoryAndFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("create data directory: %v", err)
	}
	databasePath := filepath.Join(directory, "index01.db")
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer ignoreCloseError(store)
	if _, err := store.SaveRecording(context.Background(), RecordingInput{
		RecordedAtMillis: 1760000000000,
		Client:           "test",
		Transcription:    "mode test",
		Fingerprint:      strings.Repeat("e", 64),
	}); err != nil {
		t.Fatalf("SaveRecording() error = %v", err)
	}

	assertPathMode(t, directory, 0o700)
	assertPathMode(t, databasePath, 0o600)
	assertPathMode(t, databasePath+"-wal", 0o600)
	assertPathMode(t, databasePath+"-shm", 0o600)

	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("relax data directory mode: %v", err)
	}
	if err := os.Chmod(databasePath, 0o644); err != nil {
		t.Fatalf("relax database mode: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	store, err = OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("reopen protected store: %v", err)
	}
	defer ignoreCloseError(store)
	assertPathMode(t, directory, 0o700)
	assertPathMode(t, databasePath, 0o600)
}

func TestHealthRejectsChangedApplicationSchema(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "health.db")
	store, err := OpenStore(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer ignoreCloseError(store)
	if _, err := store.db.Exec(`DROP TRIGGER delivery_workflow_transition`); err != nil {
		t.Fatalf("drop workflow trigger: %v", err)
	}
	if err := store.Health(context.Background()); err == nil || !strings.Contains(err.Error(), "schema objects") {
		t.Fatalf("Health() error = %v", err)
	}
}

func createDatabaseWithRecording(t *testing.T, path, transcript, fingerprint string) {
	t.Helper()
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore(%q) error = %v", path, err)
	}
	if _, err := store.SaveRecording(context.Background(), RecordingInput{
		RecordedAtMillis: 1760000000000,
		Client:           "test",
		Transcription:    transcript,
		Fingerprint:      fingerprint,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("SaveRecording(%q) error = %v", path, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(%q) error = %v", path, err)
	}
}

func mutateSQLiteDatabase(t *testing.T, path, statement string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open database for mutation: %v", err)
	}
	defer ignoreCloseError(db)
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("mutate database: %v", err)
	}
}

func assertDatabaseRecordingCount(t *testing.T, path string, want int) {
	t.Helper()
	store, err := OpenStore(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenStore(%q) after rejected restore: %v", path, err)
	}
	defer ignoreCloseError(store)
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM recordings`).Scan(&count); err != nil {
		t.Fatalf("count recordings: %v", err)
	}
	if count != want {
		t.Fatalf("recording count = %d, want %d", count, want)
	}
}

func assertPathMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("mode for %q = %04o, want %04o", path, info.Mode().Perm(), want)
	}
}
