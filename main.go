package main

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const tickTickAPIBaseURL = "https://api.ticktick.com/open/v1"

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	if code := runMain(os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func runMain(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	if err := executeWithInput(logger, args, getenv, stdin, stdout); err != nil {
		logger.Error("server stopped", "error", err)
		return 1
	}
	return 0
}

func runWithEnvironment(logger *slog.Logger, getenv func(string) string) error {
	cfg, err := LoadConfig(getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := OpenStore(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer ignoreCloseError(store)

	deepSeek, err := NewDeepSeekClientWithConfig(cfg.DeepSeekToken, http.DefaultTransport, time.Now, DeepSeekClientConfig{Model: cfg.DeepSeekModel, TimeZone: cfg.TimeZone})
	if err != nil {
		return err
	}
	tickTick, err := NewTickTickClient(tickTickAPIBaseURL, cfg.TickTickToken, &http.Client{
		Transport: http.DefaultTransport,
		Timeout:   30 * time.Second,
	})
	if err != nil {
		return err
	}
	router, err := tickTick.ValidateRouting(ctx, TickTickRoutingConfig{
		DefaultProjectID: cfg.TickTickDefaultProjectID,
		NoteProjectID:    cfg.TickTickNoteProjectID,
		Aliases:          cfg.TickTickProjectAliases,
	})
	if err != nil {
		return err
	}
	aliases := make([]string, 0, len(cfg.TickTickProjectAliases))
	for alias := range cfg.TickTickProjectAliases {
		aliases = append(aliases, alias)
	}
	worker, err := NewWorker(store, deepSeek, router, WorkerConfig{
		Owner: cfg.WorkerOwner, TimeZone: cfg.TimeZone,
		LeaseDuration: 2 * time.Minute, PollInterval: time.Second,
		RetryBase: 30 * time.Second, RetryMaximum: 30 * time.Minute,
		ExtractionMaxAttempts: 5, DeliveryMaxAttempts: 5, ReconcileMaxAttempts: 3,
		ProjectAliases: aliases, Jitter: randomJitter, Logger: logger,
	})
	if err != nil {
		return err
	}
	go func() {
		if err := worker.Run(ctx); err != nil {
			logger.Error("worker stopped", "error", err)
		}
	}()

	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler:           NewHandler(store, cfg.Token, cfg.MaxBodyBytes, logger),
		MaxHeaderBytes:    maxWebhookHeaderBytes,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       time.Minute,
	}
	logger.Info("listening", "address", listener.Addr().String(), "database_path", cfg.DBPath, "max_body_bytes", cfg.MaxBodyBytes)
	if err := serve(ctx, server, listener, 10*time.Second); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func randomJitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(limit)+1))
	if err != nil {
		return 0
	}
	return time.Duration(value.Int64())
}
