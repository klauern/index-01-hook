package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type ExtractionProvider interface {
	Extract(context.Context, string, []string) (FrozenExtraction, error)
}

type DeliveryProvider interface {
	CreateTask(context.Context, string, TickTickTaskInput) (TickTickCreatedTask, error)
	CreateNote(context.Context, TickTickNoteInput) (TickTickCreatedTask, error)
	ReconcileItem(context.Context, TickTickReconciliationInput) (TickTickReconciliationResult, error)
}

type WorkerConfig struct {
	Owner                 string
	TimeZone              string
	LeaseDuration         time.Duration
	PollInterval          time.Duration
	RetryBase             time.Duration
	RetryMaximum          time.Duration
	ExtractionMaxAttempts int
	DeliveryMaxAttempts   int
	ReconcileMaxAttempts  int
	ProjectAliases        []string
	Jitter                func(time.Duration) time.Duration
	Logger                *slog.Logger
}

type Worker struct {
	store     *Store
	extractor ExtractionProvider
	deliverer DeliveryProvider
	config    WorkerConfig
}

func NewWorker(store *Store, extractor ExtractionProvider, deliverer DeliveryProvider, config WorkerConfig) (*Worker, error) {
	if store == nil || extractor == nil || deliverer == nil {
		return nil, fmt.Errorf("store and providers are required")
	}
	config.Owner = strings.TrimSpace(config.Owner)
	if config.Owner == "" {
		return nil, fmt.Errorf("worker owner is required")
	}
	config.TimeZone = strings.TrimSpace(config.TimeZone)
	if config.TimeZone == "" {
		config.TimeZone = defaultDeepSeekTimeZone
	}
	normalizedTimeZone, err := normalizeTimeZone(config.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("worker time zone is invalid")
	}
	config.TimeZone = normalizedTimeZone
	if config.LeaseDuration <= 0 || config.PollInterval <= 0 || config.RetryBase <= 0 || config.RetryMaximum < config.RetryBase {
		return nil, fmt.Errorf("worker durations are invalid")
	}
	if config.ExtractionMaxAttempts <= 0 || config.DeliveryMaxAttempts <= 0 || config.ReconcileMaxAttempts <= 0 {
		return nil, fmt.Errorf("worker attempt limits are invalid")
	}
	if config.Jitter == nil {
		return nil, fmt.Errorf("worker jitter is required")
	}
	if config.Logger == nil {
		return nil, fmt.Errorf("worker logger is required")
	}
	aliases, err := normalizeProjectAliases(config.ProjectAliases)
	if err != nil {
		return nil, fmt.Errorf("worker project aliases are invalid")
	}
	config.ProjectAliases = aliases
	return &Worker{store: store, extractor: extractor, deliverer: deliverer, config: config}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	if err := w.store.WorkerStarted(ctx, w.config.Owner); err != nil {
		return err
	}
	defer func() {
		if err := w.store.WorkerStopped(context.WithoutCancel(ctx), w.config.Owner); err != nil {
			w.config.Logger.Warn("worker health update failed")
		}
	}()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		worked, err := w.RunOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			w.config.Logger.Error("worker cycle failed")
		}
		if ctx.Err() != nil {
			return nil
		}
		delay := w.config.PollInterval
		if worked {
			delay = 0
		}
		timer.Reset(delay)
	}
}

func (w *Worker) RunOnce(ctx context.Context) (worked bool, cycleErr error) {
	if err := w.store.WorkerCycleStarted(ctx, w.config.Owner); err != nil {
		w.config.Logger.Warn("worker health update failed")
	}
	defer func() {
		if err := w.store.WorkerCycleCompleted(context.WithoutCancel(ctx), w.config.Owner, cycleErr != nil); err != nil {
			w.config.Logger.Warn("worker health update failed")
		}
	}()
	claim, err := w.store.ClaimExtraction(ctx, w.config.Owner, w.config.LeaseDuration)
	if err != nil {
		return false, err
	}
	if claim != nil {
		return true, w.processExtraction(ctx, claim)
	}
	delivery, err := w.store.ClaimDelivery(ctx, w.config.Owner, w.config.LeaseDuration)
	if err != nil {
		return false, err
	}
	if delivery != nil {
		return true, w.processDelivery(ctx, delivery)
	}
	return false, nil
}

func (w *Worker) processExtraction(ctx context.Context, claim *ExtractionClaim) error {
	started := time.Now()
	extraction, err := w.extractor.Extract(ctx, claim.Transcription, w.config.ProjectAliases)
	w.recordProviderLatency(ctx, "deepseek", time.Since(started), err != nil)
	if err == nil {
		return w.store.FreezeExtraction(ctx, claim.RecordingID, w.config.Owner, extraction)
	}
	w.logDeepSeekFailure(claim.RecordingID, err)
	kind := classifyDeepSeekError(err)
	switch kind {
	case DeepSeekErrorAuthentication:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, OutcomeAuthentication, "blocked_auth")
	case DeepSeekErrorReview:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, OutcomeReview, "needs_review")
	case DeepSeekErrorMalformed, DeepSeekErrorTerminal:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, OutcomeMalformed, "dead_letter")
	case DeepSeekErrorRetryable:
		if claim.CycleAttemptNumber >= w.config.ExtractionMaxAttempts {
			return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, OutcomeRetryable, "dead_letter")
		}
		return w.store.RetryExtraction(ctx, claim.RecordingID, w.config.Owner, OutcomeRetryable, w.retryAt(claim.CycleAttemptNumber))
	default:
		return w.store.BlockExtraction(ctx, claim.RecordingID, w.config.Owner, OutcomeMalformed, "dead_letter")
	}
}

func (w *Worker) logDeepSeekFailure(recordingID int64, err error) {
	kind := classifyDeepSeekError(err)
	attributes := []any{
		"recording_id", recordingID,
		"classification", kind,
		"reason_code", deepSeekFailureReason(err),
	}
	var providerError *DeepSeekError
	if errors.As(err, &providerError) && providerError.StatusCode != 0 {
		attributes = append(attributes, "status_code", providerError.StatusCode)
	}
	w.config.Logger.Warn("deepseek extraction failed", attributes...)
}

func deepSeekFailureReason(err error) string {
	var providerError *DeepSeekError
	if !errors.As(err, &providerError) {
		return "unexpected_error"
	}
	if providerError.StatusCode != 0 {
		return "provider_http_error"
	}
	switch providerError.Kind {
	case DeepSeekErrorAuthentication:
		return "provider_authentication"
	case DeepSeekErrorRetryable:
		return "provider_transport"
	case DeepSeekErrorReview:
		return "provider_refusal"
	case DeepSeekErrorMalformed:
		switch {
		case strings.HasPrefix(providerError.Detail, "structured output"):
			return "structured_output"
		case strings.HasPrefix(providerError.Detail, "success response") || strings.HasPrefix(providerError.Detail, "response"):
			return "response_envelope"
		case strings.Contains(providerError.Detail, "item") || strings.Contains(providerError.Detail, "due date"):
			return "item_validation"
		default:
			return "client_validation"
		}
	case DeepSeekErrorTerminal:
		return "provider_terminal"
	default:
		return "unknown_provider_error"
	}
}

func (w *Worker) processDelivery(ctx context.Context, claim *DeliveryClaim) error {
	if claim.Kind != ItemKindTask && claim.Kind != ItemKindNote {
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	}
	if claim.RecoveredLease || claim.LastClassification == OutcomeAmbiguous {
		return w.reconcileDelivery(ctx, claim)
	}
	var created TickTickCreatedTask
	var err error
	started := time.Now()
	switch claim.Kind {
	case ItemKindTask:
		created, err = w.deliverer.CreateTask(ctx, claim.ProjectAlias, TickTickTaskInput{
			Title: claim.Title, Content: claim.Notes, RecordingHash: claim.RecordingHash,
			TaskIndex: claim.TaskIndex, Priority: claim.Priority, Tags: claim.Tags,
			Due: claim.Due, AllDay: claim.AllDay, TimeZone: w.config.TimeZone,
		})
	case ItemKindNote:
		created, err = w.deliverer.CreateNote(ctx, TickTickNoteInput{
			Title: claim.Title, Content: claim.Notes, RecordingHash: claim.RecordingHash,
			TaskIndex: claim.TaskIndex,
		})
	}
	w.recordProviderLatency(ctx, "ticktick", time.Since(started), err != nil)
	if err == nil {
		return w.store.CompleteDelivery(ctx, DeliveryCompletion{
			TaskID: claim.ID, LeaseOwner: w.config.Owner, Classification: OutcomeCreated,
			TickTickTaskID: created.ID, TickTickProjectID: created.ProjectID,
		})
	}
	return w.handleDeliveryError(ctx, claim, err)
}

func (w *Worker) reconcileDelivery(ctx context.Context, claim *DeliveryClaim) error {
	started := time.Now()
	result, err := w.deliverer.ReconcileItem(ctx, TickTickReconciliationInput{
		Kind: claim.Kind, ProjectAlias: claim.ProjectAlias, Marker: claim.Marker,
		Title: claim.Title, Content: claim.Notes,
	})
	w.recordProviderLatency(ctx, "ticktick", time.Since(started), err != nil)
	if err != nil {
		return w.handleReconciliationError(ctx, claim, err)
	}
	switch result.Status {
	case TickTickReconciliationConfirmed:
		if strings.TrimSpace(result.TaskID) == "" || strings.TrimSpace(result.ProjectID) == "" {
			return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
		}
		return w.store.CompleteDelivery(ctx, DeliveryCompletion{
			TaskID: claim.ID, LeaseOwner: w.config.Owner, Classification: OutcomeReconciled,
			TickTickTaskID: result.TaskID, TickTickProjectID: result.ProjectID,
		})
	case TickTickReconciliationReview:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
	case TickTickReconciliationUnconfirmed:
		if claim.CycleAttemptNumber >= w.config.DeliveryMaxAttempts {
			return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
		}
		return w.store.RetryDelivery(ctx, claim.ID, w.config.Owner, OutcomeRetryable, w.store.now())
	default:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	}
}

func (w *Worker) handleReconciliationError(ctx context.Context, claim *DeliveryClaim, err error) error {
	kind := classifyTickTickError(err)
	switch kind {
	case TickTickErrorAuthentication:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "blocked_auth")
	case TickTickErrorConfiguration, TickTickErrorMalformed:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
	case TickTickErrorRetryable, TickTickErrorAmbiguous:
		if claim.ReconcileAttemptCount+1 >= w.config.ReconcileMaxAttempts {
			return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, "needs_review")
		}
		return w.store.RetryDeliveryReconciliation(ctx, claim.ID, w.config.Owner, w.retryAt(claim.ReconcileAttemptCount+1))
	default:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeReview, "needs_review")
	}
}

func (w *Worker) recordProviderLatency(ctx context.Context, provider string, duration time.Duration, failed bool) {
	if err := w.store.RecordProviderLatency(context.WithoutCancel(ctx), w.config.Owner, provider, duration, failed); err != nil {
		w.config.Logger.Warn("worker health update failed")
	}
}

func (w *Worker) handleDeliveryError(ctx context.Context, claim *DeliveryClaim, err error) error {
	kind := classifyTickTickError(err)
	switch kind {
	case TickTickErrorAuthentication:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeAuthentication, "blocked_auth")
	case TickTickErrorConfiguration:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeConfiguration, "needs_review")
	case TickTickErrorMalformed:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	case TickTickErrorAmbiguous:
		return w.store.RetryDelivery(ctx, claim.ID, w.config.Owner, OutcomeAmbiguous, w.retryAt(claim.CycleAttemptNumber))
	case TickTickErrorRetryable:
		if claim.CycleAttemptNumber >= w.config.DeliveryMaxAttempts {
			return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeRetryable, "dead_letter")
		}
		return w.store.RetryDelivery(ctx, claim.ID, w.config.Owner, OutcomeRetryable, w.retryAt(claim.CycleAttemptNumber))
	default:
		return w.store.BlockDelivery(ctx, claim.ID, w.config.Owner, OutcomeMalformed, "dead_letter")
	}
}

func (w *Worker) retryAt(attempt int) time.Time {
	delay := w.config.RetryBase
	for index := 1; index < attempt && delay < w.config.RetryMaximum; index++ {
		if delay > w.config.RetryMaximum/2 {
			delay = w.config.RetryMaximum
			break
		}
		delay *= 2
	}
	if delay > w.config.RetryMaximum {
		delay = w.config.RetryMaximum
	}
	jitterLimit := delay / 4
	if remaining := w.config.RetryMaximum - delay; jitterLimit > remaining {
		jitterLimit = remaining
	}
	jitter := w.config.Jitter(jitterLimit)
	if jitter < 0 || jitter > jitterLimit {
		jitter = 0
	}
	return w.store.now().UTC().Add(delay + jitter)
}

func classifyDeepSeekError(err error) DeepSeekErrorKind {
	var typed *DeepSeekError
	if errors.As(err, &typed) {
		return typed.Kind
	}
	return DeepSeekErrorMalformed
}

func classifyTickTickError(err error) TickTickErrorKind {
	var typed *TickTickError
	if errors.As(err, &typed) {
		return typed.Kind
	}
	return TickTickErrorMalformed
}
