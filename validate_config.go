package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// validateProviders checks configuration and reads routing metadata before database access.
// Client construction makes no model calls or TickTick writes.
func validateProviders(ctx context.Context, cfg Config, transport http.RoundTripper) (*DeepSeekClient, *TickTickClient, *TickTickRouter, error) {
	deepSeek, err := NewDeepSeekClientWithConfig(cfg.DeepSeekToken, transport, time.Now, DeepSeekClientConfig{
		Model: cfg.DeepSeekModel, TimeZone: cfg.TimeZone, CaptureEvidence: cfg.EvaluationRetention > 0,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("model client configuration is invalid")
	}
	tickTick, err := NewTickTickClient(tickTickAPIBaseURL, cfg.TickTickToken, &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("TickTick client configuration is invalid")
	}
	router, err := tickTick.ValidateRouting(ctx, TickTickRoutingConfig{
		DefaultProjectID: cfg.TickTickDefaultProjectID,
		NoteProjectID:    cfg.TickTickNoteProjectID,
		Aliases:          cfg.TickTickProjectAliases,
	})
	if err != nil {
		// Routing errors can contain private alias names. Expose only the error kind.
		var providerError *TickTickError
		if errors.As(err, &providerError) {
			return nil, nil, nil, fmt.Errorf("TickTick routing validation failed (%s)", providerError.Kind)
		}
		return nil, nil, nil, fmt.Errorf("TickTick routing validation failed")
	}
	return deepSeek, tickTick, router, nil
}

func runValidateConfig(ctx context.Context, getenv func(string) string, transport http.RoundTripper, output io.Writer) error {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return fmt.Errorf("local configuration validation failed")
	}
	if _, _, _, err := validateProviders(ctx, cfg, transport); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Status string `json:"status"`
	}{Status: "ok"})
}
