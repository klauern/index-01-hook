package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db  *sql.DB
	now func() time.Time
}

type RecordingInput struct {
	RecordedAtMillis int64
	Client           string
	Trigger          string
	Transcription    string
	AudioFilename    string
	AudioByteCount   int64
	Fingerprint      string
}

type Receipt struct {
	ID        int64 `json:"id"`
	Duplicate bool  `json:"duplicate"`
	Queued    bool  `json:"queued"`
}

func OpenStore(ctx context.Context, path string) (*Store, error) {
	return openStore(ctx, path, time.Now)
}

func openStore(ctx context.Context, path string, now func() time.Time) (*Store, error) {
	if now == nil {
		return nil, fmt.Errorf("clock is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := protectSQLiteStorage(absPath); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// One connection keeps connection-scoped SQLite pragmas consistent. WAL
	// still allows external readers while this receiver serializes its writes.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db, now: now}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := protectSQLiteStorage(absPath); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure database: %w", err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("create migration registry: %w", err)
	}
	if err := s.applyMigrations(ctx); err != nil {
		return err
	}
	return validateApplicationDatabase(ctx, s.db)
}

func (s *Store) applyMigrations(ctx context.Context) error {
	definitions, err := migrationDefinitions()
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		var applied int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, definition.version).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %d: %w", definition.version, err)
		}
		if applied > 1 {
			return fmt.Errorf("migration %d has duplicate records", definition.version)
		}
		if applied == 1 {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", definition.version, err)
		}
		if _, err := tx.ExecContext(ctx, string(definition.script)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", definition.version, err)
		}
		insertStatement := `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`
		insertArguments := []any{definition.version, definition.name, s.now().UTC().Format(time.RFC3339Nano)}
		if definition.version >= databaseIdentityMigrationVersion {
			insertStatement = `INSERT INTO schema_migrations(version, name, applied_at, checksum) VALUES (?, ?, ?, ?)`
			insertArguments = append(insertArguments, definition.checksum)
		}
		if _, err := tx.ExecContext(ctx, insertStatement, insertArguments...); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", definition.version, err)
		}
		if definition.version == databaseIdentityMigrationVersion {
			for _, recorded := range definitions {
				if recorded.version >= databaseIdentityMigrationVersion {
					break
				}
				result, err := tx.ExecContext(ctx, `
					UPDATE schema_migrations SET checksum = ?
					WHERE version = ? AND name = ? AND checksum = ''`,
					recorded.checksum, recorded.version, recorded.name)
				if err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("record migration %d checksum: %w", recorded.version, err)
				}
				updated, err := result.RowsAffected()
				if err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("read migration %d checksum update: %w", recorded.version, err)
				}
				if updated != 1 {
					_ = tx.Rollback()
					return fmt.Errorf("migration %d record is invalid", recorded.version)
				}
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", definition.version, err)
		}
	}
	return nil
}

func (s *Store) SaveRecording(ctx context.Context, input RecordingInput) (Receipt, error) {
	nowTime := s.now().UTC()
	now := nowTime.Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Receipt{}, fmt.Errorf("begin recording transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var id int64
	var receiveCount int
	err = tx.QueryRowContext(ctx, `
		INSERT INTO recordings (
			recorded_at_ms, client, trigger, transcription, audio_filename,
			audio_byte_count, payload_fingerprint, receive_count,
			first_received_at, last_received_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(payload_fingerprint) DO UPDATE SET
			receive_count = recordings.receive_count + 1,
			last_received_at = excluded.last_received_at
		RETURNING id, receive_count`,
		input.RecordedAtMillis, input.Client, input.Trigger, input.Transcription,
		input.AudioFilename, input.AudioByteCount, input.Fingerprint, now, now,
	).Scan(&id, &receiveCount)
	if err != nil {
		return Receipt{}, fmt.Errorf("save recording: %w", err)
	}

	if strings.TrimSpace(input.Transcription) != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO extraction_jobs (
				recording_id, state, attempt_count, next_attempt_at_ms,
				created_at, updated_at, workflow_state
			) VALUES (?, 'pending', 0, ?, ?, ?, 'received')
			ON CONFLICT(recording_id) DO NOTHING`, id, nowTime.UnixMilli(), now, now); err != nil {
			return Receipt{}, fmt.Errorf("queue deepseek dispatch: %w", err)
		}
	}
	var queued bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM extraction_jobs
			WHERE recording_id = ? AND state NOT IN ('completed', 'review')
		)`, id).Scan(&queued); err != nil {
		return Receipt{}, fmt.Errorf("check deepseek dispatch: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Receipt{}, fmt.Errorf("commit recording: %w", err)
	}
	return Receipt{ID: id, Duplicate: receiveCount > 1, Queued: queued}, nil
}

func (s *Store) Health(ctx context.Context) error {
	if err := validateApplicationDatabase(ctx, s.db); err != nil {
		return fmt.Errorf("validate database health: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
