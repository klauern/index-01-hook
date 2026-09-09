package main

import (
	"strings"
	"testing"
)

func TestLegacyWebhookTokenRequiresExplicitOptIn(t *testing.T) {
	for _, test := range []struct {
		name, setting, token string
		valid                bool
	}{
		{"default rejects legacy", "", strings.Repeat("a", 14), false},
		{"false rejects legacy", "false", strings.Repeat("a", 14), false},
		{"true accepts legacy", "true", strings.Repeat("a", 14), true},
		{"true rejects shorter", "true", strings.Repeat("a", 13), false},
		{"true rejects whitespace", "true", strings.Repeat("a", 14) + " ", false},
		{"true rejects empty", "true", "", false},
		{"invalid setting", "maybe", strings.Repeat("a", 32), false},
		{"default accepts current", "", strings.Repeat("a", 32), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := validConfigEnv()
			env["INDEX01_WEBHOOK_TOKEN"], env["INDEX01_ALLOW_LEGACY_WEBHOOK_TOKEN"] = test.token, test.setting
			cfg, err := LoadConfig(func(key string) string { return env[key] })
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
			if test.valid && cfg.Token != test.token {
				t.Fatal("compatibility changed the sender credential")
			}
		})
	}
	if err := validateWebhookToken(strings.Repeat("a", 14)); err == nil {
		t.Fatal("default validation was weakened")
	}
}
