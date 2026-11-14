package main

import (
	"reflect"
	"testing"
)

// TestLoadConfigErrorPrecedence keeps the first error stable across configuration stages.
func TestLoadConfigErrorPrecedence(t *testing.T) {
	tests := []struct {
		name    string
		invalid map[string]string
		want    string
	}{
		{"legacy before verification", map[string]string{"INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN": "invalid", "INDEX01_TYPESAFE_VERIFY": "true"}, "INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN must be a boolean"},
		{"verification before shadow", map[string]string{"INDEX01_TYPESAFE_VERIFY": "true", "INDEX01_TYPESAFE_SHADOW": "invalid"}, "INDEX01_TYPESAFE_VERIFY is not approved; use INDEX01_TYPESAFE_SHADOW"},
		{"shadow token before model", map[string]string{"INDEX01_TYPESAFE_SHADOW": "true", "INDEX01_TYPESAFE_MODEL": " "}, "INDEX01_TYPESAFE_TOKEN is required when verification is enabled"},
		{"provider before webhook", map[string]string{"INDEX01_TYPESAFE_MODEL": " ", "INDEX01_WEBHOOK_TOKEN": ""}, "INDEX01_TYPESAFE_MODEL is invalid"},
		{"webhook before extractor", map[string]string{"INDEX01_WEBHOOK_TOKEN": "", "INDEX01_DEEPSEEK_TOKEN": ""}, "INDEX01_WEBHOOK_TOKEN is required"},
		{"retention before polling", map[string]string{"INDEX01_EVALUATION_RETENTION_DAYS": "366", "INDEX01_EVALUATION_POLL_INTERVAL": "bad"}, "INDEX01_EVALUATION_RETENTION_DAYS must be an integer from 0 to 365"},
		{"routing before storage", map[string]string{"INDEX01_TICKTICK_PROJECT_ALIASES": "null", "INDEX01_DB_PATH": " "}, "INDEX01_TICKTICK_PROJECT_ALIASES must be a JSON object"},
		{"storage before body", map[string]string{"INDEX01_DB_PATH": " ", "INDEX01_MAX_BODY_BYTES": "-1"}, "INDEX01_DB_PATH must not be blank"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validConfigEnv()
			for key, value := range tt.invalid {
				env[key] = value
			}
			cfg, err := LoadConfig(func(key string) string { return env[key] })
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
			if !reflect.DeepEqual(cfg, Config{}) {
				t.Fatal("failed configuration returned partial state")
			}
		})
	}
}

// TestAliasDescriptionValidationPrecedence keeps identifier rejection before shape errors.
func TestAliasDescriptionValidationPrecedence(t *testing.T) {
	aliases := map[string]string{"work": "private-project"}
	_, err := parseAliasDescriptions(`{"unknown":"private-project\n"}`, aliases)
	if err == nil {
		t.Fatal("private identifier accepted")
	}
	want := "INDEX01_TICKTICK_PROJECT_ALIAS_DESCRIPTIONS must not contain a project identifier"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}
