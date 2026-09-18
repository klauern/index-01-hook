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

func validateProvidersWithTypeSafe(ctx context.Context, cfg Config, transport http.RoundTripper) (*DeepSeekClient, *TypeSafeClient, *TickTickClient, *TickTickRouter, error) {
	deepSeek, err := NewDeepSeekClientWithConfig(cfg.DeepSeekToken, transport, time.Now, DeepSeekClientConfig{
		Model: cfg.DeepSeekModel, TimeZone: cfg.TimeZone, CaptureEvidence: cfg.EvaluationRetention > 0, SemanticVerification: cfg.TypeSafeVerify,
	})
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("model client configuration is invalid")
	}
	var typeSafe *TypeSafeClient
	if cfg.TypeSafeVerify {
		typeSafe, err = NewTypeSafeClient(cfg.TypeSafeToken, transport, TypeSafeClientConfig{Model: cfg.TypeSafeModel, Endpoint: cfg.TypeSafeEndpoint})
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("TypeSafe client configuration is invalid")
		}
	}
	tickTick, err := NewTickTickClient(tickTickAPIBaseURL, cfg.TickTickToken, &http.Client{Transport: transport, Timeout: 30 * time.Second})
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("TickTick client configuration is invalid")
	}
	router, err := tickTick.ValidateRouting(ctx, TickTickRoutingConfig{DefaultProjectID: cfg.TickTickDefaultProjectID, NoteProjectID: cfg.TickTickNoteProjectID, Aliases: cfg.TickTickProjectAliases})
	if err != nil {
		var providerError *TickTickError
		if errors.As(err, &providerError) {
			return nil, nil, nil, nil, fmt.Errorf("TickTick routing validation failed (%s)", providerError.Kind)
		}
		return nil, nil, nil, nil, fmt.Errorf("TickTick routing validation failed")
	}
	return deepSeek, typeSafe, tickTick, router, nil
}

// validateProviders checks configuration and reads routing metadata before database access.
func validateProviders(ctx context.Context, cfg Config, transport http.RoundTripper) (*DeepSeekClient, *TickTickClient, *TickTickRouter, error) {
	deepSeek, _, tickTick, router, err := validateProvidersWithTypeSafe(ctx, cfg, transport)
	return deepSeek, tickTick, router, err
}

func runValidateConfig(ctx context.Context, getenv func(string) string, transport http.RoundTripper, output io.Writer) error {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return fmt.Errorf("local configuration validation failed")
	}
	if _, _, _, _, err := validateProvidersWithTypeSafe(ctx, cfg, transport); err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Status string `json:"status"`
	}{Status: "ok"})
}
