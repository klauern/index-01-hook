package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	index01ApplicationID             = 1227895112
	databaseIdentityMigrationVersion = 6
)

type migrationDefinition struct {
	version  int
	name     string
	checksum string
	script   []byte
}

func migrationDefinitions() ([]migrationDefinition, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	definitions := make([]migrationDefinition, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("migration %q has no numeric prefix", entry.Name())
		}
		version, err := strconv.Atoi(prefix)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("migration %q has an invalid version", entry.Name())
		}
		script, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %d: %w", version, err)
		}
		digest := sha256.Sum256(script)
		definitions = append(definitions, migrationDefinition{
			version: version, name: entry.Name(), checksum: hex.EncodeToString(digest[:]), script: script,
		})
	}
	return definitions, nil
}

func protectSQLiteStorage(databasePath string) error {
	directory := filepath.Dir(databasePath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	// #nosec G302 -- A private directory requires the owner execute bit.
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect database directory: %w", err)
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm"} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect SQLite file: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("SQLite path must be a regular file")
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("protect SQLite file: %w", err)
		}
	}
	return nil
}

func validateApplicationDatabase(ctx context.Context, db *sql.DB) error {
	var applicationID int
	if err := db.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&applicationID); err != nil {
		return fmt.Errorf("read application identity: %w", err)
	}
	if applicationID != index01ApplicationID {
		return fmt.Errorf("database application identity is invalid")
	}
	if err := validateMigrationRecords(ctx, db); err != nil {
		return err
	}
	wantSchema, err := expectedSchemaManifest()
	if err != nil {
		return err
	}
	gotSchema, err := readSchemaManifest(ctx, db)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(gotSchema, wantSchema) {
		return fmt.Errorf("database schema objects are invalid: %s", describeSchemaDifference(gotSchema, wantSchema))
	}
	var violations int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
		return fmt.Errorf("check database foreign keys: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("database foreign keys are invalid")
	}
	if err := validateQueueInvariants(ctx, db); err != nil {
		return err
	}
	return nil
}

func describeSchemaDifference(got, want []string) string {
	gotObjects := make(map[string]string, len(got))
	for _, object := range got {
		fields := strings.SplitN(object, "\x00", 4)
		gotObjects[strings.Join(fields[:min(3, len(fields))], " ")] = object
	}
	for _, object := range want {
		fields := strings.SplitN(object, "\x00", 4)
		key := strings.Join(fields[:min(3, len(fields))], " ")
		actual, exists := gotObjects[key]
		if !exists {
			return "missing " + key
		}
		if actual != object {
			return "changed " + key
		}
		delete(gotObjects, key)
	}
	for key := range gotObjects {
		return "unexpected " + key
	}
	return "object order differs"
}

func validateMigrationRecords(ctx context.Context, db *sql.DB) error {
	definitions, err := migrationDefinitions()
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read migration records: %w", err)
	}
	defer ignoreCloseError(rows)
	index := 0
	for rows.Next() {
		if index >= len(definitions) {
			return fmt.Errorf("database migration records are invalid")
		}
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			return fmt.Errorf("scan migration record: %w", err)
		}
		definition := definitions[index]
		if version != definition.version || name != definition.name || checksum != definition.checksum {
			return fmt.Errorf("database migration records are invalid")
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read migration records: %w", err)
	}
	if index != len(definitions) {
		return fmt.Errorf("database migration records are incomplete")
	}
	return nil
}

func validateQueueInvariants(ctx context.Context, db *sql.DB) error {
	queries := []string{
		`SELECT count(*) FROM extraction_jobs WHERE NOT (
			(state = 'pending' AND workflow_state = 'received') OR
			(state = 'leased' AND workflow_state = 'extracting') OR
			(state = 'retry' AND workflow_state = 'retry_wait') OR
			(state = 'frozen' AND workflow_state = 'extracted') OR
			(state = 'completed' AND workflow_state = 'complete') OR
			(state = 'review' AND workflow_state IN ('blocked_auth', 'needs_review', 'dead_letter'))
		)`,
		`SELECT count(*) FROM delivery_tasks WHERE NOT (
			(state = 'pending' AND workflow_state = 'extracted') OR
			(state = 'leased' AND workflow_state = 'creating') OR
			(state = 'retry' AND workflow_state = 'retry_wait') OR
			(state = 'completed' AND workflow_state = 'complete') OR
			(state = 'review' AND workflow_state IN ('blocked_auth', 'needs_review', 'dead_letter'))
		)`,
	}
	for _, query := range queries {
		var invalid int
		if err := db.QueryRowContext(ctx, query).Scan(&invalid); err != nil {
			return fmt.Errorf("check queue invariants: %w", err)
		}
		if invalid != 0 {
			return fmt.Errorf("database queue invariants are invalid")
		}
	}
	return nil
}

var (
	expectedSchemaOnce sync.Once
	expectedSchema     []string
	expectedSchemaErr  error
)

func expectedSchemaManifest() ([]string, error) {
	expectedSchemaOnce.Do(func() {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			expectedSchemaErr = fmt.Errorf("open expected schema database: %w", err)
			return
		}
		defer ignoreCloseError(db)
		if _, err := db.Exec(`CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
			expectedSchemaErr = fmt.Errorf("create expected migration registry: %w", err)
			return
		}
		definitions, err := migrationDefinitions()
		if err != nil {
			expectedSchemaErr = err
			return
		}
		for _, definition := range definitions {
			if _, err := db.Exec(string(definition.script)); err != nil {
				expectedSchemaErr = fmt.Errorf("apply expected migration %d: %w", definition.version, err)
				return
			}
		}
		expectedSchema, expectedSchemaErr = readSchemaManifest(context.Background(), db)
	})
	return expectedSchema, expectedSchemaErr
}

func readSchemaManifest(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT type, name, tbl_name, sql
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'
		ORDER BY type, name`)
	if err != nil {
		return nil, fmt.Errorf("read database schema: %w", err)
	}
	defer ignoreCloseError(rows)
	manifest := make([]string, 0)
	for rows.Next() {
		var objectType, name, table string
		var statement sql.NullString
		if err := rows.Scan(&objectType, &name, &table, &statement); err != nil {
			return nil, fmt.Errorf("scan database schema: %w", err)
		}
		normalizedStatement := strings.Join(strings.Fields(statement.String), " ")
		manifest = append(manifest, strings.Join([]string{objectType, name, table, normalizedStatement}, "\x00"))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read database schema: %w", err)
	}
	return manifest, nil
}

func validateSQLiteIntegrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("database failed integrity check")
	}
	defer ignoreCloseError(rows)
	resultCount := 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil || result != "ok" {
			return fmt.Errorf("database failed integrity check")
		}
		resultCount++
	}
	if err := rows.Err(); err != nil || resultCount != 1 {
		return fmt.Errorf("database failed integrity check")
	}
	return nil
}
