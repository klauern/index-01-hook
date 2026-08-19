package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestDeepSeekLiveSyntheticContract(t *testing.T) {
	if os.Getenv("INDEX01_RUN_LIVE_DEEPSEEK_TEST") != "1" {
		t.Skip("set INDEX01_RUN_LIVE_DEEPSEEK_TEST=1 to run the live synthetic contract test")
	}
	token := os.Getenv("INDEX01_DEEPSEEK_TOKEN")
	if token == "" {
		t.Fatal("INDEX01_DEEPSEEK_TOKEN is required")
	}
	client, err := NewDeepSeekClient(token, http.DefaultTransport, time.Now)
	if err != nil {
		t.Fatalf("configure live DeepSeek client: %v", err)
	}
	tests := []struct {
		name       string
		transcript string
		wantKind   ItemKind
		wantAlias  string
	}{
		{name: "work", transcript: "Email the client about the office contract.", wantKind: ItemKindTask, wantAlias: "work"},
		{name: "home", transcript: "Replace the furnace filter at home.", wantKind: ItemKindTask, wantAlias: "home"},
		{name: "uncertain", transcript: "Buy a birthday gift.", wantKind: ItemKindTask},
		{name: "note", transcript: "Capture a note titled Local contract test with content Synthetic input only.", wantKind: ItemKindNote},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			result, err := client.Extract(ctx, test.transcript, []string{"home", "work"})
			if err != nil {
				var providerErr *DeepSeekError
				if errors.As(err, &providerErr) {
					t.Fatalf("live DeepSeek contract failed: kind=%s operation=%s status=%d detail=%s",
						providerErr.Kind, providerErr.Operation, providerErr.StatusCode, providerErr.Detail)
				}
				t.Fatalf("live DeepSeek contract failed: error_type=%T", err)
			}
			if result.Provider != "deepseek" || result.Model != deepSeekModel {
				t.Fatalf("live DeepSeek contract returned unexpected provider metadata")
			}
			if len(result.Items) != 1 || result.Items[0].Kind != test.wantKind {
				t.Fatalf("live DeepSeek routing returned unexpected item shape")
			}
			if result.Items[0].ProjectAlias != test.wantAlias {
				t.Fatalf("live DeepSeek routing alias = %q, want %q", result.Items[0].ProjectAlias, test.wantAlias)
			}
		})
	}
}
