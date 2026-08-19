package main

import (
	"strings"
	"testing"
)

func TestLoadConfigUsesDefaults(t *testing.T) {
	env := validConfigEnv()

	cfg, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.Token != "synthetic-webhook-token-0123456789abcdef" {
		t.Errorf("Token = %q, want synthetic-webhook-token-0123456789abcdef", cfg.Token)
	}
	if cfg.DBPath != "./index01.db" {
		t.Errorf("DBPath = %q, want ./index01.db", cfg.DBPath)
	}
	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q, want :8080", cfg.ListenAddr)
	}
	if cfg.MaxBodyBytes != 64<<20 {
		t.Errorf("MaxBodyBytes = %d, want %d", cfg.MaxBodyBytes, int64(64<<20))
	}
	if cfg.DeepSeekModel != defaultDeepSeekModel {
		t.Errorf("DeepSeekModel = %q, want %q", cfg.DeepSeekModel, defaultDeepSeekModel)
	}
	if cfg.TimeZone != defaultDeepSeekTimeZone {
		t.Errorf("TimeZone = %q, want %q", cfg.TimeZone, defaultDeepSeekTimeZone)
	}
	if cfg.WorkerOwner != "index-01-hook" || len(cfg.TickTickProjectAliases) != 0 {
		t.Errorf("worker defaults = owner:%q aliases:%v", cfg.WorkerOwner, cfg.TickTickProjectAliases)
	}
	if cfg.TickTickNoteProjectID != "project-notes" {
		t.Errorf("TickTickNoteProjectID = %q, want project-notes", cfg.TickTickNoteProjectID)
	}
}

func TestLoadConfigReadsOverrides(t *testing.T) {
	env := validConfigEnv()
	for key, value := range map[string]string{
		"INDEX01_DB_PATH":                  "/tmp/index.db",
		"INDEX01_LISTEN_ADDR":              "127.0.0.1:9090",
		"INDEX01_MAX_BODY_BYTES":           "12345",
		"INDEX01_DEEPSEEK_MODEL":           "  deepseek-custom-v1  ",
		"INDEX01_TIME_ZONE":                "  America/Los_Angeles  ",
		"INDEX01_WORKER_OWNER":             "pod-one",
		"INDEX01_TICKTICK_PROJECT_ALIASES": `{"Work":"project-work"}`,
		"INDEX01_TICKTICK_NOTE_PROJECT_ID": "project-notes-override",
	} {
		env[key] = value
	}

	cfg, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.DBPath != "/tmp/index.db" || cfg.ListenAddr != "127.0.0.1:9090" || cfg.MaxBodyBytes != 12345 {
		t.Fatalf("LoadConfig() = %+v, overrides were not retained", cfg)
	}
	if cfg.WorkerOwner != "pod-one" || cfg.TickTickProjectAliases["work"] != "project-work" {
		t.Fatalf("LoadConfig() = %+v, worker overrides were not retained", cfg)
	}
	if cfg.DeepSeekModel != "deepseek-custom-v1" || cfg.TimeZone != "America/Los_Angeles" {
		t.Fatalf("LoadConfig() = %+v, trimmed model and time-zone overrides were not retained", cfg)
	}
	if cfg.TickTickNoteProjectID != "project-notes-override" {
		t.Fatalf("TickTickNoteProjectID = %q", cfg.TickTickNoteProjectID)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "missing webhook token", env: map[string]string{"INDEX01_WEBHOOK_TOKEN": ""}, want: "INDEX01_WEBHOOK_TOKEN"},
		{name: "weak webhook token", env: map[string]string{"INDEX01_WEBHOOK_TOKEN": "short-token"}, want: "INDEX01_WEBHOOK_TOKEN"},
		{name: "whitespace in webhook token", env: map[string]string{"INDEX01_WEBHOOK_TOKEN": "synthetic-webhook-token-0123456789 abcdef"}, want: "INDEX01_WEBHOOK_TOKEN"},
		{name: "missing DeepSeek token", env: map[string]string{"INDEX01_DEEPSEEK_TOKEN": ""}, want: "INDEX01_DEEPSEEK_TOKEN"},
		{name: "missing TickTick token", env: map[string]string{"INDEX01_TICKTICK_TOKEN": ""}, want: "INDEX01_TICKTICK_TOKEN"},
		{name: "missing TickTick project", env: map[string]string{"INDEX01_TICKTICK_DEFAULT_PROJECT_ID": ""}, want: "INDEX01_TICKTICK_DEFAULT_PROJECT_ID"},
		{name: "missing TickTick note project", env: map[string]string{"INDEX01_TICKTICK_NOTE_PROJECT_ID": ""}, want: "INDEX01_TICKTICK_NOTE_PROJECT_ID"},
		{name: "blank model", env: map[string]string{"INDEX01_DEEPSEEK_MODEL": " 	 "}, want: "INDEX01_DEEPSEEK_MODEL"},
		{name: "unsafe model", env: map[string]string{"INDEX01_DEEPSEEK_MODEL": "deepseek model"}, want: "INDEX01_DEEPSEEK_MODEL"},
		{name: "blank time zone", env: map[string]string{"INDEX01_TIME_ZONE": " 	 "}, want: "INDEX01_TIME_ZONE"},
		{name: "local time zone", env: map[string]string{"INDEX01_TIME_ZONE": "Local"}, want: "INDEX01_TIME_ZONE"},
		{name: "unknown time zone", env: map[string]string{"INDEX01_TIME_ZONE": "Mars/Olympus"}, want: "INDEX01_TIME_ZONE"},
		{name: "empty database path", env: map[string]string{"INDEX01_DB_PATH": " "}, want: "INDEX01_DB_PATH"},
		{name: "empty listen address", env: map[string]string{"INDEX01_LISTEN_ADDR": " "}, want: "INDEX01_LISTEN_ADDR"},
		{name: "non-numeric body limit", env: map[string]string{"INDEX01_MAX_BODY_BYTES": "large"}, want: "INDEX01_MAX_BODY_BYTES"},
		{name: "non-positive body limit", env: map[string]string{"INDEX01_MAX_BODY_BYTES": "0"}, want: "INDEX01_MAX_BODY_BYTES"},
		{name: "invalid aliases", env: map[string]string{"INDEX01_TICKTICK_PROJECT_ALIASES": `[]`}, want: "INDEX01_TICKTICK_PROJECT_ALIASES"},
		{name: "null aliases", env: map[string]string{"INDEX01_TICKTICK_PROJECT_ALIASES": `null`}, want: "INDEX01_TICKTICK_PROJECT_ALIASES"},
		{name: "blank alias", env: map[string]string{"INDEX01_TICKTICK_PROJECT_ALIASES": `{" ":"project"}`}, want: "INDEX01_TICKTICK_PROJECT_ALIASES"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validConfigEnv()
			for key, value := range tt.env {
				env[key] = value
			}
			_, err := LoadConfig(func(key string) string { return env[key] })
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("LoadConfig() error = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func validConfigEnv() map[string]string {
	return map[string]string{
		"INDEX01_WEBHOOK_TOKEN":               "synthetic-webhook-token-0123456789abcdef",
		"INDEX01_DEEPSEEK_TOKEN":              "deepseek-secret",
		"INDEX01_TICKTICK_TOKEN":              "ticktick-secret",
		"INDEX01_TICKTICK_DEFAULT_PROJECT_ID": "project-default",
		"INDEX01_TICKTICK_NOTE_PROJECT_ID":    "project-notes",
	}
}
