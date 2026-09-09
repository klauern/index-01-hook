package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validationEnvironment(path string) map[string]string {
	env := validConfigEnv()
	env["INDEX01_DB_PATH"] = path
	env["INDEX01_TICKTICK_DEFAULT_PROJECT_ID"] = "fixture-project"
	env["INDEX01_TICKTICK_NOTE_PROJECT_ID"] = "fixture-notes"
	return env
}

func TestValidateConfigReadsOnlyAndDoesNotOpenDatabase(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "absent", "queue.db")
			env := validationEnvironment(path)
			var output bytes.Buffer
			calls := 0
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				if request.Method != http.MethodGet || request.URL.Host != "api.ticktick.com" || request.URL.Path != "/open/v1/project" {
					t.Fatalf("unexpected provider request: %s %s", request.Method, request.URL)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`[{"id":"fixture-project","name":"Private fixture name","permission":null,"kind":"TASK"},{"id":"fixture-notes","name":"Private notes","permission":null,"kind":"NOTE"}]`))}, nil
			})
			err := runValidateConfig(context.Background(), func(key string) string { return env[key] }, transport, &output)
			if (err == nil) != (status == http.StatusOK) {
				t.Fatalf("unexpected validation result: %v", err)
			}
			if status == http.StatusOK && output.String() != "{\"status\":\"ok\"}\n" {
				t.Fatalf("unexpected output: %q", output.String())
			}
			if status != http.StatusOK && (output.Len() != 0 || strings.Contains(err.Error(), "Private fixture")) {
				t.Fatal("failure exposed private output")
			}
			if calls != 1 {
				t.Fatalf("expected one routing read, got %d", calls)
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatalf("validation created database directory: %v", err)
			}
		})
	}
}

func TestRunRejectsRoutingBeforeDatabaseMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE startup_sentinel (value TEXT); INSERT INTO startup_sentinel VALUES ('preserve'); PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range validationEnvironment(path) {
		t.Setenv(key, value)
	}
	oldTransport := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("unexpected write: %s", request.Method)
		}
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("private provider response"))}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	if err := runWithEnvironment(slog.New(slog.NewTextHandler(io.Discard, nil)), os.Getenv); err == nil || err.Error() != "TickTick routing validation failed (authentication)" {
		t.Fatalf("expected sanitized routing rejection, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed startup modified database")
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("failed startup created database sidecar %s", suffix)
		}
	}
}

func TestValidateConfigRejectsLocalConfigBeforeProviderRead(t *testing.T) {
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid local configuration caused a provider request")
		return nil, nil
	})
	if err := runValidateConfig(context.Background(), func(string) string { return "" }, transport, io.Discard); err == nil {
		t.Fatal("expected local configuration error")
	}
}
